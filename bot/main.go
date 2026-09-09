package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"

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

	var err error
	chatID, err = strconv.ParseInt(idRaw, 10, 64)
	if tgBotToken == "" || err != nil || chatID == 0 {
		log.Fatalf("❌ Error: TG_TOKEN and a valid non-zero TG_CHAT_ID environment variables must be set!")
	}

	// 1. Создаем контекст, который отменится при SIGINT (Ctrl+C) или SIGTERM (docker stop)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

	// 🛡️ FAULT TOLERANCE: Передаем ctx первым аргументом
	startAlarmStream(ctx, client, bot)

	// Start processing incoming Telegram chat commands
	// (Идеально сюда тоже передать ctx, если ваша функция это поддерживает)
	startTelegramBotLoop(ctx, client, bot)
}
