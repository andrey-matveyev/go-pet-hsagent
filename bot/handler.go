package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	pb "go-pet-hsagent/proto/hsagent/v1"

	"github.com/andrey-matveyev/go-library-queue/queue"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const queueFilePath = "queue_backup.json"

// QueueItem используется для сериализации/десериализации сообщений очереди в JSON
type QueueItem struct {
	ChatID    int64  `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode"`
}

func startAlarmStream(ctx context.Context, client pb.MonitorServiceClient, bot *tgbotapi.BotAPI) {
	listQueue := queue.NewListQueue[QueueItem]()

	// 1. ЗАГРУЗКА ИЗ ФАЙЛА (при старте)
	loadQueueFromFile(listQueue)

	inpChan := make(chan QueueItem, 100)
	outChan := queue.AddQueue(ctx, listQueue, inpChan)

	// Канал для передачи не отправленного текущего сообщения назад при завершении
	pendingMsgChan := make(chan *QueueItem, 1)

	// 2. Worker Goroutine: Отправка сообщений в Telegram
	go func() {
		defer close(pendingMsgChan)
		for {
			select {
			case <-ctx.Done():
				return
			case item, ok := <-outChan:
				if !ok {
					return
				}

				// Фиксируем текущее отправляемое сообщение
				currentMsg := item

				sent := false
				for !sent {
					select {
					case <-ctx.Done():
						// Контекст отменен во время ожидания/повтора отправки
						pendingMsgChan <- &currentMsg
						return
					default:
					}

					msg := tgbotapi.NewMessage(currentMsg.ChatID, currentMsg.Text)
					if currentMsg.ParseMode != "" {
						msg.ParseMode = currentMsg.ParseMode
					}

					if _, err := bot.Send(msg); err != nil {
						log.Printf("⚠️ Failed to send Telegram message, retrying in 3s: %v", err)
						select {
						case <-time.After(3 * time.Second):
						case <-ctx.Done():
							pendingMsgChan <- &currentMsg
							return
						}
						continue
					}
					sent = true
				}
			}
		}
	}()

	// 3. gRPC Stream Listener Goroutine
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			if err := processStream(ctx, client, inpChan); err != nil {
				log.Printf("⚠️ Stream connection issue: %v. Reconnecting in 5s...", err)
			}

			select {
			case <-time.After(5 * time.Second):
			case <-ctx.Done():
				return
			}
		}
	}()

	// 4. СОХРАНЕНИЕ НА ДИСК (при остановке приложения)
	go func() {
		<-ctx.Done()

		// Ждем завершения воркера и забираем неотправленное сообщение (если оно было)
		var pendingMsg *QueueItem
		if msg, ok := <-pendingMsgChan; ok {
			pendingMsg = msg
		}

		saveQueueToFile(listQueue, pendingMsg)
	}()
}

func loadQueueFromFile(q queue.Queue[QueueItem]) {
	if _, err := os.Stat(queueFilePath); os.IsNotExist(err) {
		return
	}

	data, err := os.ReadFile(queueFilePath)
	if err != nil {
		log.Printf("⚠️ Failed to read queue backup file: %v", err)
		return
	}

	if len(data) == 0 {
		return
	}

	err = queue.Import(q, data, func(b []byte) ([]QueueItem, error) {
		var items []QueueItem
		err := json.Unmarshal(b, &items)
		return items, err
	})

	if err != nil {
		log.Printf("⚠️ Failed to import queue from backup: %v", err)
		return
	}

	log.Printf("📦 Successfully restored %d pending messages from disk queue.", q.Len())

	// Удаляем файл после успешной загрузки в память
	_ = os.Remove(queueFilePath)
}

func saveQueueToFile(q queue.Queue[QueueItem], pendingMsg *QueueItem) {
	// Собираем неотправленные элементы
	var remainingItems []QueueItem

	// Если воркер не успел отправить текущее сообщение, ставим его ПЕРВЫМ в очередь
	if pendingMsg != nil {
		remainingItems = append(remainingItems, *pendingMsg)
	}

	// Экспортируем остальные элементы из структуры очереди с помощью queue.Export
	exportedData, err := queue.Export(q, func(items []QueueItem) ([]byte, error) {
		return json.Marshal(items)
	})

	if err != nil {
		log.Printf("❌ Failed to export queue: %v", err)
		return
	}

	var queueItems []QueueItem
	if len(exportedData) > 0 {
		_ = json.Unmarshal(exportedData, &queueItems)
	}

	// Объединяем текущее неотправленное сообщение и остальные элементы очереди
	remainingItems = append(remainingItems, queueItems...)

	if len(remainingItems) == 0 {
		return
	}

	finalData, err := json.MarshalIndent(remainingItems, "", "  ")
	if err != nil {
		log.Printf("❌ Failed to marshal remaining queue items: %v", err)
		return
	}

	// Безопасная запись на диск
	tmpPath := queueFilePath + ".tmp"
	if err := os.WriteFile(tmpPath, finalData, 0644); err != nil {
		log.Printf("❌ Failed to write queue to temp file: %v", err)
		return
	}

	if err := os.Rename(tmpPath, queueFilePath); err != nil {
		_ = os.WriteFile(queueFilePath, finalData, 0644)
	}

	log.Printf("💾 Saved %d unsent messages to disk (%s).", len(remainingItems), queueFilePath)
}

func processStream(ctx context.Context, client pb.MonitorServiceClient, inpChan chan<- QueueItem) error {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	log.Println("🔄 Attempting to connect to gRPC alarm stream...")
	stream, err := client.StreamEvents(streamCtx, &pb.StreamEventsRequest{})
	if err != nil {
		return fmt.Errorf("failed to open stream: %w", err)
	}

	log.Println("✅ Permanent gRPC notification stream established!")
	for {
		event, err := stream.Recv()
		if err != nil {
			return fmt.Errorf("stream read error: %w", err)
		}

		inpChan <- QueueItem{
			ChatID: chatID,
			Text:   event.Message,
		}
	}
}

func startTelegramBotLoop(ctx context.Context, client pb.MonitorServiceClient, bot *tgbotapi.BotAPI) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)
	defer bot.StopReceivingUpdates()

	log.Println("🚀 Telegram bot update loop started listening...")

	for {
		select {
		case <-ctx.Done():
			log.Println("🛑 Stopping Telegram bot update loop...")
			return

		case update, ok := <-updates:
			if !ok {
				log.Println("⚠️ Telegram updates channel closed.")
				return
			}

			if update.Message == nil {
				continue
			}

			if update.Message.Chat.ID != chatID {
				log.Printf("⚠️ Unauthorized access attempt from ChatID: %d, Text: %s",
					update.Message.Chat.ID, update.Message.Text)
				continue
			}

			if !update.Message.IsCommand() {
				continue
			}

			handleCommand(ctx, client, bot, update.Message)
		}
	}
}

func handleCommand(parentCtx context.Context, client pb.MonitorServiceClient, bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	reqCtx, cancel := context.WithTimeout(parentCtx, 5*time.Second)
	defer cancel()

	var text string

	switch msg.Command() {
	case "status":
		res, err := client.GetBatteryStatus(reqCtx, &pb.GetBatteryStatusRequest{})
		if err != nil {
			log.Printf("⚠️ GetBatteryStatus RPC error: %v", err)
			text = "❌ Failed to fetch data from Agent. Please check if the agent binary is running."
		} else {
			text = fmt.Sprintf("📊 **HP 4740s Server Status:**\n\n"+
				"🔋 **Battery:** %s (%d%%)\n"+
				"🌡 **CPU Temperature:** %.1f°C\n\n"+
				"💾 **Disk Usage:**\n%s",
				res.BatteryStatus, res.BatteryCapacity, res.CpuTemperature, res.DiskUsageInfo)
		}

	case "test":
		res, err := client.TestSystems(reqCtx, &pb.TestSystemsRequest{})
		if err != nil {
			log.Printf("⚠️ TestSystems RPC error: %v", err)
			text = "❌ gRPC error while invoking self-test."
		} else {
			text = res.ResultMessage
		}
	case "backup":
		res, err := client.TriggerBackup(reqCtx, &pb.TriggerBackupRequest{})
		if err != nil {
			log.Printf("⚠️ TriggerBackup RPC error: %v", err)
			text = "❌ gRPC error while triggering backup."
		} else {
			text = res.ResultMessage
		}

	default:
		text = "❌ **Unknown command.**\n\n" +
			"📋 **Supported commands:**\n" +
			"/status — Get battery status, CPU temperature, and disk usage\n" +
			"/test — Run forced system self-test\n" +
			"/backup — Trigger forced storage backup"
	}

	replyMsg := tgbotapi.NewMessage(chatID, text)
	replyMsg.ParseMode = "Markdown"
	if _, err := bot.Send(replyMsg); err != nil {
		log.Printf("⚠️ Failed to send Telegram response: %v", err)
	}
}
