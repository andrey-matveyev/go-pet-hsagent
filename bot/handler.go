package main

import (
	"context"
	"fmt"
	"log"
	"time"

	pb "go-pet-hsagent/proto"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// startAlarmStream runs a background goroutine to stream alarms from the gRPC agent
func startAlarmStream(client pb.MonitorServiceClient, bot *tgbotapi.BotAPI) {
	go func() {
		for {
			log.Println("🔄 Attempting to connect to gRPC alarm stream...")
			stream, err := client.StreamEvents(context.Background(), &pb.Empty{})
			if err != nil {
				log.Printf("❌ Failed to open stream: %v. Retrying in 5 seconds...", err)
				time.Sleep(5 * time.Second)
				continue
			}

			log.Println("✅ Permanent gRPC notification stream established!")
			for {
				event, err := stream.Recv()
				if err != nil {
					log.Printf("⚠️ Stream broken (%v). Reconnecting...", err)
					break // Break to outer loop for auto-reconnect
				}
				// Alarm received from Agent -> send push message to Telegram
				msg := tgbotapi.NewMessage(chatID, event.Message)
				bot.Send(msg)
			}
			time.Sleep(5 * time.Second)
		}
	}()
}

// startTelegramBotLoop handles incoming updates and commands from Telegram
func startTelegramBotLoop(client pb.MonitorServiceClient, bot *tgbotapi.BotAPI) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}

		// 🔒 STRICT SECURITY CHECK: Ignore everyone except the owner
		if update.Message.Chat.ID != chatID {
			log.Printf("⚠️ Unauthorized access attempt from ChatID: %d, Text: %s",
				update.Message.Chat.ID, update.Message.Text)
			continue
		}

		switch update.Message.Text {
		case "/status": // On-demand metrics request
			res, err := client.GetBatteryStatus(context.Background(), &pb.Empty{})
			var text string
			if err != nil {
				text = "❌ Failed to fetch data from Agent. Please check if the agent binary is running."
			} else {
				text = fmt.Sprintf("📊 **HP 4740s Server Status:**\n\n"+
					"🔋 **Battery:** %s (%d%%)\n"+
					"🌡 **CPU Temperature:** %.1f°C\n\n"+
					"💾 **Disk Usage:**\n%s",
					res.BatteryStatus, res.BatteryCapacity, res.CpuTemperature, res.DiskUsageInfo)
			}
			msg := tgbotapi.NewMessage(chatID, text)
			msg.ParseMode = "Markdown"
			bot.Send(msg)

		case "/test": // 🧪 Forced self-test run
			res, err := client.TestSystems(context.Background(), &pb.Empty{})
			var text string
			if err != nil {
				text = "❌ gRPC error while invoking self-test."
			} else {
				text = res.ResultMessage
			}
			msg := tgbotapi.NewMessage(chatID, text)
			msg.ParseMode = "Markdown"
			bot.Send(msg)
		}
	}
}
