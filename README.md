# Go-Pet-HSAgent

Система мониторинга домашнего сервера с Telegram-ботом. Агент собирает системные метрики (батарея, температура CPU, загрузка ядер, RAM, swap, диски, uptime), а бот позволяет получать статус и управлять сервером через команды Telegram. Связь между ботом и агентом осуществляется по gRPC.

## Как это работает (примеры сообщений в Telegram)

Ниже приведены примеры того, как выглядит взаимодействие с ботом в Telegram. Эмодзи в сообщениях не используются — только текст, цифры и временные метки.

**Команда /status**
```
Server Status:
Model: HP ProLiant DL360 Gen10
Uptime: 12 days, 4:15:22
Battery: 100% (Charging)
Reboot required: NO

CPU Usage:
Overall (%): 12.5 (58.3°C)
Cores (%): [ 8 / 15 / 10 / 5 ]

Memory Usage:
RAM (GB): 8.2 / 32.0 (25.6%)
Swap (GB): 0.0 / 16.0 (0.0%)

Disk Usage:
/mnt/storage (GB): 450 / 1000 (45.0%)

2024-01-15T08:30:00Z
```

**Команда /test**
```
Self-test completed: all systems OK.
2024-01-15T08:31:10Z
```

**Команда /backup**
```
Backup completed successfully.
2024-01-15T08:32:05Z
```

**Команда /reboot** (требует подтверждения)
```
Are you sure you want to reboot the server?
[Да] [Нет]
```
После нажатия «Да»:
```
Server reboot initiated (in 1 minute).
2024-01-15T08:35:00Z
```

**Автоматическое уведомление от агента**
```
ALERT: CPU temperature exceeded threshold (92.1°C > 85.0°C).
2024-01-15T09:10:00Z
```

## Возможности

- Мониторинг батареи, температуры CPU, загрузки ядер, RAM, swap, дисков и uptime
- gRPC-сервер в агенте и gRPC-клиент в боте
- Поток событий в реальном времени (push-уведомления об алертах)
- Дисковая очередь уведомлений: сообщения не теряются при перезапуске бота
- Подтверждение перезагрузки сервера через инлайн-кнопки
- Агент запускается как systemd-сервис на Ubuntu
- Бот разворачивается в Docker (включая CasaOS)

## Архитектура

Два компонента взаимодействуют через gRPC по протоколу, определённому в `proto/hsagent/v1/hsagent.proto`.

- **Agent** (`agent/`) — фоновый сервис на Ubuntu, собирает метрики, хранит конфигурацию в `agent/config.toml`, предоставляет gRPC API.
- **Telegram Bot** (`bot/`) — пользовательский интерфейс, обрабатывает команды Telegram, вызывает gRPC-методы агента, ведёт постоянный поток событий.

Подробная архитектура: [`.dev/docs/architecture.md`](.dev/docs/architecture.md)

## Быстрый старт

### Требования
- Go 1.21+
- Docker и Docker Compose (для бота)
- Токен и Chat ID Telegram-бота

### Сборка
```bash
# Агент
./.dev/cmd/build_agent.sh

# Бот (локальный бинарник)
./.dev/cmd/build_bot.sh

# Бот (Docker-образ)
./.dev/cmd/build_bot_docker.sh
```

### Конфигурация
- Агент: `agent/config.toml` — адрес gRPC, параметры мониторинга, пути к бэкапу.
- Бот: скопируйте `bot/.env.template` в `bot/.env` и укажите `TG_TOKEN` и `TG_CHAT_ID`.

## Развертывание

| Компонент | Окружение | Инструкция |
|-----------|-----------|------------|
| Agent | Ubuntu, systemd | [`.dev/docs/deploy_agent.md`](.dev/docs/deploy_agent.md) |
| Bot | Docker, CasaOS | [`.dev/docs/deploy_bot.md`](.dev/docs/deploy_bot.md) |

## Команды Telegram-бота

| Команда | Описание |
|---------|----------|
| `/status` | Текущее состояние сервера (батарея, CPU, RAM, диск, uptime) |
| `/test` | Принудительная самодиагностика системы |
| `/backup` | Запуск бэкапа хранилища |
| `/reboot` | Перезагрузка сервера (требует подтверждения кнопками) |
| `/help` | Список поддерживаемых команд |

## Структура проекта

```
agent/        # Агент мониторинга (systemd-сервис)
bot/          # Telegram-бот (gRPC-клиент)
proto/        # Protobuf-определение gRPC-сервиса
e2e_test/     # Интеграционные тесты
.dev/cmd/     # Скрипты сборки и деплоя
.dev/docs/    # Документация (архитектура, деплой, workflow)
```

## Документация

- [Архитектура системы](.dev/docs/architecture.md)
- [Деплой агента (systemd/Ubuntu)](.dev/docs/deploy_agent.md)
- [Деплой бота (Docker/CasaOS)](.dev/docs/deploy_bot.md)
- [Workflow разработки](.dev/docs/workflow.md)
- Пример использования / установка в CasaOS (со скриншотами) — см. раздел «Как это работает» выше.
