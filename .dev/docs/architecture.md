# Project Architecture: Go-Pet-HSAgent

This project consists of a system monitoring agent and a Telegram bot that interacts with it. The communication between the two components is handled via gRPC.

## Components

### 1. Agent (`agent/`)
The Agent is a background service responsible for monitoring system resources and performing requested tasks.

- **Core Responsibilities**:
    - Periodically monitor system metrics (Battery, CPU Temperature, Disk Usage).
    - Provide a gRPC server for the Bot to query status and trigger actions.
    - Stream real-time alerts/events to connected clients.
- **Key Files**:
    - `main.go`: Entry point; initializes configuration, starts the gRPC server, and launches monitoring loops.
    - `server.go`: Implements the `MonitorService` gRPC interface.
    - `monitor.go`: Contains the logic for various monitoring loops (battery, CPU, disk) and sends events to the event channel.
    - `config.go`: Manages Agent-specific configuration.
- **Communication Interface**: Defined in `proto/hsagent/v1/hsagent.proto`.

### 2. Telegram Bot (`bot/`)
The Bot acts as the user interface, allowing users to interact with the Agent via Telegram commands and receive real-time notifications.

- **Core Responsibilities**:
    - Listen for Telegram commands (e.g., `/status`, `/test`, `/backup`).
    - Map commands to gRPC calls to the Agent.
    - Maintain a persistent gRPC stream to receive real-time alerts from the Agent.
    - Ensure reliable delivery of notifications using a disk-persistent message queue.
- **Key Files**:
    - `main.go`: Entry point; initializes the bot and starts the update loop and the alarm stream listener.
    - `handler.go`: Contains the logic for handling Telegram commands, managing the gRPC stream, and the notification queue.
    - `config.go`: Manages Bot-specific configuration.

### 3. Protobuf (`proto/`)
Defines the gRPC service contract between the Agent and the Bot.

- **Service**: `MonitorService`
- **Methods**:
    - `GetSystemStatus`: Returns current battery, CPU, and disk information.
    - `StreamEvents`: Provides a stream of real-time system events/alerts.
    - `TestSystems`: Triggers a system self-test.
    - `TriggerBackup`: Triggers a manual backup process.

---

## Extension Guide

### How to expand the list of monitored resources
1.  **Update Protobuf**: Add new fields to the relevant message in `proto/hsagent/v1/hsagent.proto` (e.g., `GetSystemStatusResponse`).
2.  **Implement Monitoring Logic**: In `agent/monitor.go`, add a new monitoring loop (e.g., `monitorMemoryLoop`) that reads the required data and sends alerts via `s.eventChan` if necessary.
3.  **Update Agent Server**: In `agent/server.go`, update the corresponding gRPC method (e.g., `GetSystemStatus`) to include the new data in the response.
4.  **Update Bot Handler**: In `bot/handler.go`, update the `handleCommand` function (specifically the `status` case) to format and display the new information in the Telegram message.

### How to add support for new Telegram commands
1.  **Update Protobuf (If needed)**: If the new command requires a new action on the Agent, add a new RPC method to `MonitorService` in `proto/hsagent/v1/hsagent.proto`.
2.  **Implement Agent Logic**: If a new RPC was added, implement it in `agent/server.go`.
3.  **Update Bot Handler**: In `bot/handler.go`, add a new `case` in the `switch msg.Command()` block within `handleCommand` to call the new gRPC method and handle the response.
4.  **Update Help Command**: Update the `help` case in `bot/handler.go` to include the new command in the documentation.

### How to add new information to server responses
1.  **Update Protobuf**: Add new fields to the appropriate message in `proto/hsagent/v1/hsagent.proto`.
2.  **Update Agent Server**: In `agent/server.go`, update the implementation of the corresponding gRPC method to populate the new fields.
3.  **Update Bot Handler**: In `bot/handler.go`, update the `handleCommand` function to include the new information in the Telegram response.
