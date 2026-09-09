#!/bin/bash

# Единый источник правды для версии (можно переопределить или оставить здесь)
VERSION="v1.0.0"
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

GOOS=linux \
GOARCH=amd64 \
go build -ldflags="-s -w -X main.version=$VERSION -X main.gitCommit=$COMMIT -X main.buildDate=$DATE" \
-o bot_bin \
./bot

# Проверка того, что ни одна из переменных не осталась unknown
if [ "$VERSION" = "unknown" ] || [ "$COMMIT" = "unknown" ] || [ "$DATE" = "unknown" ]; then
    echo "❌ Error: One or more build version fields are 'unknown' (VERSION: $VERSION, COMMIT: $COMMIT, DATE: $DATE). Aborting build!" >&2
    exit 1
fi

./bot_bin --version

echo "Build completed successfully!"


