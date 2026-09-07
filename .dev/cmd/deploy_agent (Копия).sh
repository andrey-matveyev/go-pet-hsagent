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
# =============================================================================

# Configuration variables (can be overridden via environment variables or edited here)
REMOTE_USER="${REMOTE_USER:-andrey}"
REMOTE_HOST="${REMOTE_HOST:-192.168.50.201}"
REMOTE_DIR="${REMOTE_DIR:-/root/homemonitor}"
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

echo "=== 2. Stopping remote service ==="
ssh -t "${REMOTE_USER}@${REMOTE_HOST}" "sudo systemctl stop hsagent || true"

echo "=== 3. Uploading binary and config to remote server ==="
ssh -t "${REMOTE_USER}@${REMOTE_HOST}" "sudo mkdir -p ${REMOTE_DIR} && sudo chown -R ${REMOTE_USER}:${REMOTE_USER} ${REMOTE_DIR}"
scp "$LOCAL_BINARY_PATH" "${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_DIR}/$BINARY_NAME"
if [ -f "agent/config.toml" ]; then
    scp "agent/config.toml" "${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_DIR}/config.toml"
fi
if [ -f "agent/hsagent.service" ]; then
    scp "agent/hsagent.service" "${REMOTE_USER}@${REMOTE_HOST}:/tmp/hsagent.service"
    ssh -t "${REMOTE_USER}@${REMOTE_HOST}" "sudo mv /tmp/hsagent.service /etc/systemd/system/hsagent.service && sudo systemctl daemon-reload"
fi

echo "=== 4. Setting permissions and starting service ==="
ssh -t "${REMOTE_USER}@${REMOTE_HOST}" "chmod +x ${REMOTE_DIR}/$BINARY_NAME && sudo systemctl start hsagent && sudo systemctl enable hsagent"

echo "=== 5. Checking service status ==="
ssh -t "${REMOTE_USER}@${REMOTE_HOST}" "sudo systemctl status hsagent --no-pager"

echo "=== Deployment completed successfully! ==="
