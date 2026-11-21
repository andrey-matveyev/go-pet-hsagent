package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	pb "go-pet-hsagent/proto/hsagent/v1"

	"github.com/andrey-matveyev/go-library-queue/queue"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// TelegramSender — интерфейс-обертка над tgbotapi.BotAPI для удобного мокинга в тестах
type TelegramSender interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
}

// Переменная вместо константы позволяет менять путь к файлу очереди в юнит-тестах
var queueFilePath = "queue_backup.json"

// QueueItem используется для сериализации/десериализации сообщений очереди в JSON
type QueueItem struct {
	ChatID    int64  `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode"`
}

// sendTextMessage отправляет текстовое сообщение в Telegram с ParseMode Markdown
func sendTextMessage(bot TelegramSender, chatID int64, text string) error {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	_, err := bot.Send(msg)
	return err
}

// startAlarmStream принимает интерфейс TelegramSender вместо жесткой зависимости от *tgbotapi.BotAPI
// startupMsg — сообщение о запуске бота (отправляется перед обработкой очереди)
// shutdownMsg — сообщение о выгрузке бота (отправляется перед сохранением очереди)
func startAlarmStream(ctx context.Context, client pb.MonitorServiceClient, bot TelegramSender, startupMsg, shutdownMsg string) {
	listQueue := queue.NewListQueue[QueueItem]()

	// 1. ЗАГРУЗКА ИЗ ФАЙЛА (при старте)
	loadQueueFromFile(listQueue)

	// 1.1. СООБЩЕНИЕ О ЗАГРУЗКЕ БОТА (первым делом, до обработки очереди)
	if startupMsg != "" {
		if err := sendTextMessage(bot, chatID, startupMsg); err != nil {
			log.Printf("⚠️ Failed to send startup message, queuing: %v", err)
			listQueue.Push(QueueItem{ChatID: chatID, Text: startupMsg})
		}
	}

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

		// 4.1. СООБЩЕНИЕ О ВЫГРУЗКЕ БОТА (перед сохранением очереди)
		if shutdownMsg != "" {
			if err := sendTextMessage(bot, chatID, shutdownMsg); err != nil {
				log.Printf("⚠️ Failed to send shutdown message, queuing: %v", err)
				listQueue.Push(QueueItem{ChatID: chatID, Text: shutdownMsg})
			}
		}

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
		handleCorruptedFile(queueFilePath, "read error")
		return
	}

	if len(data) == 0 {
		// Пустой файл просто удаляем
		_ = os.Remove(queueFilePath)
		return
	}

	err = queue.Import(q, data, func(b []byte) ([]QueueItem, error) {
		var items []QueueItem
		err := json.Unmarshal(b, &items)
		return items, err
	})

	if err != nil {
		log.Printf("❌ CRITICAL: Failed to import/unmarshal queue backup file (corrupted data): %v", err)
		handleCorruptedFile(queueFilePath, "unmarshal error")
		return
	}

	log.Printf("📦 Successfully restored %d pending messages from disk queue.", q.Len())

	// Удаляем успешно прочитанный файл
	if err := os.Remove(queueFilePath); err != nil {
		log.Printf("⚠️ Warning: Could not remove processed queue backup file: %v", err)
	}
}

// handleCorruptedFile переименовывает битый файл, чтобы он не мешал работе и был доступен для анализа
func handleCorruptedFile(filePath string, reason string) {
	corruptedPath := fmt.Sprintf("%s.corrupted_%d", filePath, time.Now().Unix())
	if err := os.Rename(filePath, corruptedPath); err != nil {
		log.Printf("❌ Failed to rename corrupted queue file from %s to %s: %v", filePath, corruptedPath, err)
		return
	}
	log.Printf("⚠️ Corrupted queue backup file was renamed to '%s' (reason: %s) for analysis.", corruptedPath, reason)
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

	dir := filepath.Dir(queueFilePath)
	if dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0755)
	}

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

func startTelegramBotLoop(ctx context.Context, client pb.MonitorServiceClient, bot TelegramSender) {
	// Если передана реальная структура tgbotapi.BotAPI, включаем получение апдейтов
	if realBot, ok := bot.(*tgbotapi.BotAPI); ok {
		u := tgbotapi.NewUpdate(0)
		u.Timeout = 60

		updates := realBot.GetUpdatesChan(u)
		defer realBot.StopReceivingUpdates()

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
}

func handleCommand(parentCtx context.Context, client pb.MonitorServiceClient, bot TelegramSender, msg *tgbotapi.Message) {
	reqCtx, cancel := context.WithTimeout(parentCtx, 5*time.Second)
	defer cancel()

	var text string

	switch msg.Command() {
	case "status":
		res, err := client.GetSystemStatus(reqCtx, &pb.GetSystemStatusRequest{})
		if err != nil {
			log.Printf("⚠️ GetSystemStatus RPC error: %v", err)
			text = "❌ Failed to fetch data from Agent. Please check if the agent binary is running."
		} else {
			var uptimeLine string
			if res.Uptime != "" {
				uptimeLine = fmt.Sprintf("• Uptime: %s", res.Uptime)
			}

			batteryLine := fmt.Sprintf("• Battery: %d%% (%s)", res.BatteryCapacity, res.BatteryStatus)

			// Find overall CPU usage and temperature
			var overallCpuUsage float64
			var coreUsages []float64
			for _, cu := range res.CpuUsage {
				if cu.CoreId == "Overall" {
					overallCpuUsage = cu.UsagePct
				} else {
					coreUsages = append(coreUsages, cu.UsagePct)
				}
			}
			cpuLine := fmt.Sprintf("• Overall (%%): %.1f (%.1f°C)", overallCpuUsage, res.CpuTemperature)
			coresLine := fmt.Sprintf("• Cores (%%): [ %s ]", formatCoreUsages(coreUsages))

			var ramLine, swapLine string
			if res.Ram != nil {
				ramLine = fmt.Sprintf("• RAM (GB): %.1f / %.1f (%.1f%%)", res.Ram.UsedBytes/(1024*1024*1024), res.Ram.TotalBytes/(1024*1024*1024), res.Ram.UsagePct)
			}
			if res.Swap != nil {
				swapLine = fmt.Sprintf("• Swap (GB): %.1f / %.1f (%.1f%%)", res.Swap.UsedBytes/(1024*1024*1024), res.Swap.TotalBytes/(1024*1024*1024), res.Swap.UsagePct)
			}

			var diskLines []string
			for _, d := range res.Disks {
				diskLines = append(diskLines, fmt.Sprintf("• %s (GB): %.0f / %.0f (%.1f%%)", d.Name, d.UsedGb, d.TotalGb, d.UsagePct))
			}

			text = fmt.Sprintf("%s Server Status::\n%s\n%s\n\nCPU Usage:\n%s\n%s\n\nMemory Usage:\n%s\n%s\n\nDisk Usage:\n%s\n\n%s",
					res.ServerModel, uptimeLine, batteryLine, cpuLine, coresLine, ramLine, swapLine, strings.Join(diskLines, "\n"), res.Timestamp)
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

	case "help":
		text = "📋 **Supported commands:**\n\n" +
			"/status — Get battery status, CPU temperature, CPU usage per core, memory, disk usage, and uptime\n" +
			"/test — Run forced system self-test\n" +
			"/backup — Trigger forced storage backup\n"

	default:
		text = "❌ **Unknown command.**\n\n" +
			"📋 **Supported commands:**\n" +
			"/status — Get battery status, CPU temperature, CPU usage per core, memory, and disk usage\n" +
			"/test — Run forced system self-test\n" +
			"/backup — Trigger forced storage backup\n"
	}

	replyMsg := tgbotapi.NewMessage(chatID, text)
	replyMsg.ParseMode = "Markdown"
	if _, err := bot.Send(replyMsg); err != nil {
		log.Printf("⚠️ Failed to send Telegram response: %v", err)
	}
}

// formatCoreUsages formats a slice of CPU core usage values as a string like "[ 15 / 8 / 20 / 5 ]"
func formatCoreUsages(usages []float64) string {
	if len(usages) == 0 {
		return ""
	}
	parts := make([]string, len(usages))
	for i, u := range usages {
		parts[i] = fmt.Sprintf("%.0f", u)
	}
	return strings.Join(parts, " / ")
}