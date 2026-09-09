package main

import (
	"fmt"
	"log"
	"os"
	"strconv"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func main() {
	// Support --version / -v flag
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("hsagent-bot version: %s (commit: %s, built at: %s)\n", version, gitCommit, buildDate)
		os.Exit(0)
	}

	// Read configuration from environment variables
	tgBotToken = os.Getenv("TG_TOKEN")
	idRaw := os.Getenv("TG_CHAT_ID")
	chatID, _ = strconv.ParseInt(idRaw, 10, 64)

	if tgBotToken == "" || chatID == 0 {
		log.Fatalf("❌ Error: Environment variables TG_TOKEN and TG_CHAT_ID must be set!")
	}

	// Initialize Telegram API
	bot, err := tgbotapi.NewBotAPI(tgBotToken)
	if err != nil {
		log.Fatalf("failed to init tg bot: %v", err)
	}
	log.Printf("🤖 Bot successfully authorized as: %s", bot.Self.UserName)

	// Connect to gRPC Agent
	client, conn, err := initGRPCClient()
	if err != nil {
		log.Fatalf("did not connect to grpc agent: %v", err)
	}
	defer conn.Close()

	// 🛡️ FAULT TOLERANCE: Start background gRPC alarm stream
	startAlarmStream(client, bot)

	// Start processing incoming Telegram chat commands
	startTelegramBotLoop(client, bot)
}
