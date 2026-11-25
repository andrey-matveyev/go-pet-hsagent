package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	core "go-pet-hsagent/bot/core"
)

var (
	version   = "unknown" // Overwritten via -ldflags
	gitCommit = "unknown" // Overwritten via -ldflags
	buildDate = "unknown" // Overwritten via -ldflags
)

func main() {
	// Support --version / -v flag
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("hsagent-bot version: %s (commit: %s, built at: %s)\n", version, gitCommit, buildDate)
		os.Exit(0)
	}

	// Read configuration from environment variables
	core.TgBotToken = os.Getenv("TG_TOKEN")
	idRaw := os.Getenv("TG_CHAT_ID")

	var err error
	core.ChatID, err = strconv.ParseInt(idRaw, 10, 64)
	if core.TgBotToken == "" || err != nil || core.ChatID == 0 {
		log.Fatalf("❌ Error: TG_TOKEN and a valid non-zero TG_CHAT_ID environment variables must be set!")
	}

	// 1. Создаем контекст, который отменится при SIGINT (Ctrl+C) или SIGTERM (docker stop)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Initialize Telegram API
	bot, err := tgbotapi.NewBotAPI(core.TgBotToken)
	if err != nil {
		log.Fatalf("failed to init tg bot: %v", err)
	}
	log.Printf("🤖 Bot successfully authorized as: %s", bot.Self.UserName)

	// Connect to gRPC Agent
	client, conn, err := core.InitGRPCClient()
	if err != nil {
		log.Fatalf("did not connect to grpc agent: %v", err)
	}
	defer conn.Close()

	// Формируем сообщения о загрузке/выгрузке с текущим временем
	startupMsg := fmt.Sprintf("Bot loaded and ready.\n%s", time.Now().Format(time.RFC3339))
	shutdownMsg := fmt.Sprintf("Bot is shutting down.\n%s", time.Now().Format(time.RFC3339))

	// 🛡️ FAULT TOLERANCE: Передаем ctx первым аргументом
	shutdownDone := core.StartAlarmStream(ctx, client, bot, startupMsg, shutdownMsg)

	// Start processing incoming Telegram chat commands
	core.StartTelegramBotLoop(ctx, client, bot)

	// Ждем завершения цикла обновлений Telegram
	<-ctx.Done()

	// Ждем завершения всех операций выгрузки (отправка сообщения о выгрузке + сохранение очереди)
	<-shutdownDone

	// Небольшая задержка перед завершением процесса main для гарантии сброса файла очереди на диск
	time.Sleep(500 * time.Millisecond)
}
