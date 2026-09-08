#!/usr/bin/env bash
set -e

# =============================================================================
# Deployment Script for Server Monitor Bot (hsagent-bot Docker via CasaOS)
#
# Examples:
#   # Using default remote host (192.168.50.201) and user (andrey):
#   ./.dev/cmd/deploy_bot_docker.sh
#
#   # Specifying remote host and user explicitly via environment variables:
#   REMOTE_HOST=192.168.50.201 REMOTE_USER=andrey ./.dev/cmd/deploy_bot_docker.sh
# =============================================================================

REMOTE_USER="${REMOTE_USER:-andrey}"
REMOTE_HOST="${REMOTE_HOST:-192.168.50.201}"
REMOTE_DIR="${REMOTE_DIR:-/opt/hsagent-bot}"
CASAOS_APP_DIR="${CASAOS_APP_DIR:-/var/lib/casaos/apps/hsagent-bot}"
IMAGE_NAME="hsagent-bot:latest"
TAR_NAME="hsagent-bot.tar"

if [ -z "$REMOTE_HOST" ]; then
    echo "Error: REMOTE_HOST is not set."
    echo "Usage: REMOTE_HOST=192.168.50.201 ./.dev/cmd/deploy_bot_docker.sh"
    exit 1
fi

echo "=== 1. Building Docker image locally ==="
.dev/cmd/build_bot_docker.sh

echo "=== 2. Saving Docker image to tar archive ==="
LOCAL_TAR="/tmp/${TAR_NAME}"
docker save "$IMAGE_NAME" -o "$LOCAL_TAR"

echo "=== 3. Preparing remote directories via SSH ==="
ssh "${REMOTE_USER}@${REMOTE_HOST}" "sudo mkdir -p ${REMOTE_DIR} ${CASAOS_APP_DIR} && sudo chown -R ${REMOTE_USER}:${REMOTE_USER} ${REMOTE_DIR}"

echo "=== 4. Syncing docker-compose.yml and image archive via rsync ==="
rsync -avz "$LOCAL_TAR" "${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_DIR}/${TAR_NAME}"
rsync -avz "bot/docker-compose.yml" "${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_DIR}/docker-compose.yml"

echo "=== 5. Verifying mandatory .env file on remote server ==="
ssh "${REMOTE_USER}@${REMOTE_HOST}" "if [ ! -f ${CASAOS_APP_DIR}/.env ]; then echo 'Error: Required .env file is missing at ${CASAOS_APP_DIR}/.env on remote server!' >&2; exit 1; fi"

echo "=== 6. Deploying on remote server ==="
ssh "${REMOTE_USER}@${REMOTE_HOST}" "sudo docker load -i ${REMOTE_DIR}/${TAR_NAME}"

# Копируем docker-compose.yml в папку приложения CasaOS
ssh "${REMOTE_USER}@${REMOTE_HOST}" "cp ${REMOTE_DIR}/docker-compose.yml ${CASAOS_APP_DIR}/docker-compose.yml"

# Запускаем через docker compose в папке CasaOS (чтобы подтянулся .env)
ssh "${REMOTE_USER}@${REMOTE_HOST}" "cd ${CASAOS_APP_DIR} && sudo docker compose down --remove-orphans || true && sudo docker compose up -d"

echo "=== 7. Cleaning up temporary tar file on remote server ==="
ssh "${REMOTE_USER}@${REMOTE_HOST}" "rm -f ${REMOTE_DIR}/${TAR_NAME}"
rm -f "$LOCAL_TAR"

echo "=== 8. Checking container status ==="
ssh "${REMOTE_USER}@${REMOTE_HOST}" "sudo docker ps --filter name=hs_bot_container"

echo "=== Bot Docker deployment completed successfully! ==="

