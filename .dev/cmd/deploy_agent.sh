#!/usr/bin/env bash
set -e

# =============================================================================
# Deployment Script for Home Monitor Agent (hsagent)
#
# Examples:
#   # Using default remote host (192.168.50.201) and user (andrey):
#   ./.dev/cmd/deploy_agent.sh
#
#   # Specifying remote host and user explicitly via environment variables:
#   REMOTE_HOST=192.168.50.201 REMOTE_USER=andrey ./.dev/cmd/deploy_agent.sh
#
#
# Modifying the /etc/sudoers file via visudo:
# sudo visudo
#
# Go to end of file
# Declare a list of allowed commands:
# Cmnd_Alias HSAGENT_CMDS = /usr/bin/systemctl start hsagent, \
#                           /usr/bin/systemctl stop hsagent, \
#                           /usr/bin/systemctl restart hsagent, \
#                           /usr/bin/systemctl status hsagent *, \
#                           /usr/bin/systemctl daemon-reload, \
#                           /usr/bin/cp /opt/homemonitor/hsagent.service /etc/systemd/system/hsagent.service
#
# Assign access rights to a user:
# andrey ALL=(ALL) NOPASSWD: HSAGENT_CMDS
# =============================================================================

# Configuration variables (can be overridden via environment variables or edited here)
REMOTE_USER="${REMOTE_USER:-andrey}"
REMOTE_HOST="${REMOTE_HOST:-192.168.50.201}"
REMOTE_DIR="${REMOTE_DIR:-/opt/homemonitor}"
BINARY_NAME="agent_bin"
LOCAL_BINARY_PATH="./$BINARY_NAME"

if [ -z "$REMOTE_HOST" ]; then
    echo "Error: REMOTE_HOST is not set."
    echo "Usage: REMOTE_HOST=192.168.50.201 ./.dev/cmd/deploy_agent.sh"
    exit 1
fi

echo "=== 1. Building Go binary for Linux amd64 ==="
# GOOS=linux GOARCH=amd64 go build -o "$LOCAL_BINARY_PATH" .
.dev/cmd/build_agent.sh

echo "=== 2. Syncing binary and config via rsync ==="
# Убеждаемся, что директория существует (права у пользователя andrey)
ssh "${REMOTE_USER}@${REMOTE_HOST}" "mkdir -p ${REMOTE_DIR}"

# rsync передает только изменившиеся файлы по SSH-ключу
rsync -avz "$LOCAL_BINARY_PATH" "${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_DIR}/$BINARY_NAME"

if [ -f "agent/config.toml" ]; then
    rsync -avz "agent/config.toml" "${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_DIR}/config.toml"
fi

if [ -f "agent/hsagent.service" ]; then
    # Синхронизируем сервис прямо в рабочую папку
    rsync -avz "agent/hsagent.service" "${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_DIR}/hsagent.service"
    
    # Копируем в системную папку systemd
    ssh "${REMOTE_USER}@${REMOTE_HOST}" "sudo cp ${REMOTE_DIR}/hsagent.service /etc/systemd/system/hsagent.service && sudo systemctl daemon-reload"
fi

echo "=== 3. Setting execution permissions ==="
ssh "${REMOTE_USER}@${REMOTE_HOST}" "chmod +x ${REMOTE_DIR}/$BINARY_NAME"

echo "=== 4. Seamless service restart ==="
# Сервис перезапускается атомарно за доли секунды без предварительной остановки
ssh "${REMOTE_USER}@${REMOTE_HOST}" "sudo systemctl restart hsagent"

echo "=== 5. Checking service status ==="
ssh "${REMOTE_USER}@${REMOTE_HOST}" "sudo systemctl status hsagent --no-pager"

echo "=== Deployment completed successfully! ==="