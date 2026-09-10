package main

import (
	"context"
	"fmt"
	"log"
	"time"

	pb "go-pet-hsagent/proto/hsagent/v1"

	"github.com/andrey-matveyev/go-library-queue/queue"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// startAlarmStream runs a background goroutine to stream alarms from the gRPC agent
func startAlarmStream(ctx context.Context, client pb.MonitorServiceClient, bot *tgbotapi.BotAPI) {
	listQueue := queue.NewListQueue[tgbotapi.Chattable]()
	inpChan := make(chan tgbotapi.Chattable, 100)
	outChan := queue.AddQueue(ctx, listQueue, inpChan)

	// Worker goroutine responsible for sending messages to Telegram one by one strictly
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-outChan:
				if !ok {
					return
				}
				for {
					select {
					case <-ctx.Done():
						return
					default:
					}

					if _, err := bot.Send(msg); err != nil {
						log.Printf("⚠️ Failed to send Telegram message, retrying in 3s: %v", err)
						time.Sleep(3 * time.Second)
						continue
					}
					break
				}
			}
		}
	}()

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

			time.Sleep(5 * time.Second)
		}
	}()
}

func processStream(ctx context.Context, client pb.MonitorServiceClient, inpChan chan<- tgbotapi.Chattable) error {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel() // Ресурсы освободятся ровно при завершении processStream

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

		msg := tgbotapi.NewMessage(chatID, event.Message)
		inpChan <- msg
	}
}

// startTelegramBotLoop handles incoming updates and commands from Telegram
func startTelegramBotLoop(ctx context.Context, client pb.MonitorServiceClient, bot *tgbotapi.BotAPI) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	// Гарантируем остановку получения апдейтов при выходе из функции
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

			// 🔒 STRICT SECURITY CHECK: Ignore everyone except the owner
			if update.Message.Chat.ID != chatID {
				log.Printf("⚠️ Unauthorized access attempt from ChatID: %d, Text: %s",
					update.Message.Chat.ID, update.Message.Text)
				continue
			}

			// Обрабатываем только команды
			if !update.Message.IsCommand() {
				continue
			}

			// Запускаем обработку команды
			handleCommand(ctx, client, bot, update.Message)
		}
	}
}

// Выносим обработку команд в отдельную функцию для чистоты и удобства defer/timeouts
func handleCommand(parentCtx context.Context, client pb.MonitorServiceClient, bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	// Ограничиваем время ожидания ответа от gRPC агента 5 секундами
	reqCtx, cancel := context.WithTimeout(parentCtx, 5*time.Second)
	defer cancel()

	var text string

	switch msg.Command() {
	case "status": // /status
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

	case "test": // /test
		res, err := client.TestSystems(reqCtx, &pb.TestSystemsRequest{})
		if err != nil {
			log.Printf("⚠️ TestSystems RPC error: %v", err)
			text = "❌ gRPC error while invoking self-test."
		} else {
			text = res.ResultMessage
		}
	case "backup": // /backup
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
