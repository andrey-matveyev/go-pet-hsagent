#!/bin/bash

VERSION="v1.0.0"
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

GOOS=linux \
GOARCH=amd64 \
go build -ldflags="-s -w -X main.version=$VERSION -X main.gitCommit=$COMMIT -X main.buildDate=$DATE" \
-o bot_bin \
./bot

echo "Build completed successfully!"
./bot_bin --version
