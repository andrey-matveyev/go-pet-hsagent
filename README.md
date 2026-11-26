# HomeServerAgent

Home server monitoring system with a Telegram bot. The agent collects system metrics (battery, CPU temperature, core usage, RAM, swap, disks, uptime), and the bot lets you check status and control the server via Telegram commands. Communication between the bot and the agent is handled by gRPC.

## How it works (Telegram message examples)

Below are examples of how interacting with the bot looks in Telegram. Emojis are not used in messages — only text, numbers, and timestamps.

**Command /status**
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

**Command /test**
```
Self-test completed: all systems OK.
2024-01-15T08:31:10Z
```

**Command /backup**
```
Backup completed successfully.
2024-01-15T08:32:05Z
```

**Command /reboot** (requires confirmation)
```
Are you sure you want to reboot the server?
[Yes] [No]
```
After pressing "Yes":
```
Server reboot initiated (in 1 minute).
2024-01-15T08:35:00Z
```

**Automatic notification from the agent**
```
ALERT: CPU temperature exceeded threshold (92.1°C > 85.0°C).
2024-01-15T09:10:00Z
```

## Features

- Monitoring of battery, CPU temperature, core load, RAM, swap, disks, and uptime
- gRPC server in the agent and gRPC client in the bot
- Real-time event stream (push notifications for alerts)
- Disk-backed notification queue: messages are not lost on bot restart
- Server reboot confirmation via inline buttons
- Agent runs as a systemd service on Ubuntu
- Bot runs in Docker (including CasaOS)

## Architecture

Two components interact via gRPC using the protocol defined in `proto/hsagent/v1/hsagent.proto`.

- **Agent** (`agent/`) — background service on Ubuntu, collects metrics, stores configuration in `agent/config.toml`, exposes gRPC API.
- **Telegram Bot** (`bot/`) — user interface, handles Telegram commands, calls agent gRPC methods, maintains a persistent event stream.

Full architecture: [`.dev/docs/architecture.md`](.dev/docs/architecture.md)

## Quick Start

### Requirements
- Go 1.21+
- Docker and Docker Compose (for the bot)
- Telegram bot token and Chat ID

### Build
```bash
# Agent
./.dev/cmd/build_agent.sh

# Bot (local binary)
./.dev/cmd/build_bot.sh

# Bot (Docker image)
./.dev/cmd/build_bot_docker.sh
```

### Configuration
- Agent: `agent/config.toml` — gRPC address, monitoring parameters, backup paths.
- Bot: copy `bot/.env.template` to `bot/.env` and set `TG_TOKEN` and `TG_CHAT_ID`.

## Deployment

| Component | Environment | Instructions |
|-----------|-------------|--------------|
| Agent | Ubuntu, systemd | [`.dev/docs/deploy_agent.md`](.dev/docs/deploy_agent.md) |
| Bot | Docker, CasaOS | [`.dev/docs/deploy_bot.md`](.dev/docs/deploy_bot.md) |

## Telegram Bot Commands

| Command | Description |
|---------|-------------|
| `/status` | Current server state (battery, CPU, RAM, disk, uptime) |
| `/test` | Forced system self-test |
| `/backup` | Trigger storage backup |
| `/reboot` | Reboot the server (requires confirmation via buttons) |
| `/help` | List of supported commands |

## Project Structure

```
agent/        # Monitoring agent (systemd service)
bot/          # Telegram bot (gRPC client)
proto/        # Protobuf gRPC service definition
e2e_test/     # Integration tests
.dev/cmd/     # Build and deploy scripts
.dev/docs/    # Documentation (architecture, deploy, workflow)
```

## Documentation

- [System architecture](.dev/docs/architecture.md)
- [Agent deployment (systemd/Ubuntu)](.dev/docs/deploy_agent.md)
- [Bot deployment (Docker/CasaOS)](.dev/docs/deploy_bot.md)
- [Development workflow](.dev/docs/workflow.md)
- Example usage / CasaOS installation (with screenshots) — see the "How it works" section above.
