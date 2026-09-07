#!/bin/bash

IMAGE_NAME="hsagent-bot:latest"

# Если локальный бинарник bot_bin отсутствует — автоматически собираем его
if [ ! -f "./bot_bin" ]; then
    echo "⚠️ bot_bin not found locally. Running build_bot.sh first..."
    .dev/cmd/build_bot.sh
fi

# Получаем версию из скомпилированного бинарника (формат: "hsagent-bot version: v1.0.0 (commit: abc1234, built at: 2026-09-07T...)")
VERSION_OUTPUT=$(./bot_bin --version 2>/dev/null)
if [ $? -eq 0 ]; then
    VERSION=$(echo "$VERSION_OUTPUT" | awk '{print $3}')
    COMMIT=$(echo "$VERSION_OUTPUT" | grep -o 'commit: [^,]*' | awk '{print $2}')
    DATE=$(echo "$VERSION_OUTPUT" | grep -o 'built at: [^)]*' | cut -d' ' -f3-)
else
    # Fallback на случай ошибок
    VERSION="v1.0.0"
    COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "none")
    DATE=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
fi

echo "Building Docker image ${IMAGE_NAME}..."
echo "Using version info -> Version: ${VERSION}, Commit: ${COMMIT}, Date: ${DATE}"

docker build \
    --build-arg VERSION="$VERSION" \
    --build-arg COMMIT="$COMMIT" \
    --build-arg DATE="$DATE" \
    -f bot/dockerfile \
    -t "$IMAGE_NAME" \
    .

echo "Docker image build completed successfully!"
docker run --rm "$IMAGE_NAME" ./bot_bin --version

