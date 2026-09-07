package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	pb "go-pet-hsagent/proto" // Имя корневого модуля + путь к proto

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	tgBotToken string
	chatID     int64
	// Для локального теста в системе используем 127.0.0.1
	// Для CasaOS позже сменить этот адрес на host.docker.internal:50051
	agentAddr = "host.docker.internal:50051"
)

func main() {
	// Считываем конфиги из переменных окружения
	tgBotToken = os.Getenv("TG_TOKEN")
	idRaw := os.Getenv("TG_CHAT_ID")
	chatID, _ = strconv.ParseInt(idRaw, 10, 64)

	if tgBotToken == "" || chatID == 0 {
		log.Fatalf("❌ Ошибка: Переменные окружения TG_TOKEN и TG_CHAT_ID должны быть заданы!")
	}

	// Инициализация Telegram API
	bot, err := tgbotapi.NewBotAPI(tgBotToken)
	if err != nil {
		log.Fatalf("failed to init tg bot: %v", err)
	}
	log.Printf("🤖 Бот успешно авторизован под именем: %s", bot.Self.UserName)

	// Подключение к gRPC Агенту
	conn, err := grpc.Dial(agentAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("did not connect to grpc agent: %v", err)
	}
	defer conn.Close()
	client := pb.NewMonitorServiceClient(conn)

	// 🛡️ ЗАЩИТА ОТ СБОЕВ: Потоковое удержание gRPC-стрима алармов
	go func() {
		for {
			log.Println("🔄 Попытка подключения к gRPC стриму алармов...")
			stream, err := client.StreamEvents(context.Background(), &pb.Empty{})
			if err != nil {
				log.Printf("❌ Не удалось открыть стрим: %v. Повтор через 5 секунд...", err)
				time.Sleep(5 * time.Second)
				continue
			}

			log.Println("✅ Постоянный gRPC стрим уведомлений установлен!")
			for {
				event, err := stream.Recv()
				if err != nil {
					log.Printf("⚠️ Стрим разорван (%v). Попытка переподключения...", err)
					break // Выход во внешний цикл для автоматического реконнекта
				}
				// Прилетел аларм из Агента -> отправляем push-сообщение в Телеграм
				msg := tgbotapi.NewMessage(chatID, event.Message)
				bot.Send(msg)
			}
			time.Sleep(5 * time.Second)
		}
	}()

	// Обработка входящих команд из Телеграм-чата
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}

		// 🔒 ЖЕСТКАЯ ПРОВЕРКА БЕЗОПАСНОСТИ: Игнор абсолютно всех, кроме хозяина
		if update.Message.Chat.ID != chatID {
			log.Printf("⚠️ Попытка несанкционированного доступа от ChatID: %d, Текст: %s",
				update.Message.Chat.ID, update.Message.Text)
			continue
		}

		switch update.Message.Text {
		case "/status": // Запрос метрик «по требованию»
			res, err := client.GetBatteryStatus(context.Background(), &pb.Empty{})
			var text string
			if err != nil {
				text = "❌ Не удалось получить данные от Агента. Проверьте, работает ли бинарник агента."
			} else {
				text = fmt.Sprintf("📊 **Статус сервера HP 4740s:**\n\n"+
					"🔋 **Батарея:** %s (%d%%)\n"+
					"🌡 **Температура CPU:** %.1f°C\n\n"+
					"💾 **Использование дисков:**\n%s",
					res.BatteryStatus, res.BatteryCapacity, res.CpuTemperature, res.DiskUsageInfo)
			}
			msg := tgbotapi.NewMessage(chatID, text)
			msg.ParseMode = "Markdown"
			bot.Send(msg)

		case "/test": // 🧪 Принудительный прогон тестов
			res, err := client.TestSystems(context.Background(), &pb.Empty{})
			var text string
			if err != nil {
				text = "❌ Ошибка gRPC при вызове самодиагностики."
			} else {
				text = res.ResultMessage
			}
			msg := tgbotapi.NewMessage(chatID, text)
			msg.ParseMode = "Markdown"
			bot.Send(msg)
		}
	}
}
