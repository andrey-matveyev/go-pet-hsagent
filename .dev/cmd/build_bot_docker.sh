#!/bin/bash
set -e

IMAGE_NAME="hsagent-bot:latest"

# Если локальный бинарник bot_bin отсутствует — автоматически собираем его
if [ ! -f "./bot_bin" ]; then
    echo "⚠️ bot_bin not found locally. Running build_bot.sh first..."
    .dev/cmd/build_bot.sh
fi

# Получаем версию из скомпилированного бинарника.
# Если бинарник упадет или вернет ненулевой код — скрипт завалится прямо здесь.
VERSION_OUTPUT=$(./bot_bin --version)

VERSION=$(echo "$VERSION_OUTPUT" | awk '{print $3}')
COMMIT=$(echo "$VERSION_OUTPUT" | grep -o 'commit: [^,]*' | awk '{print $2}')
DATE=$(echo "$VERSION_OUTPUT" | grep -o 'built at: [^)]*' | cut -d' ' -f3-)

# Дополнительная защита: проверяем, что распарсенные переменные не пустые
if [ -z "$VERSION" ] || [ -z "$COMMIT" ] || [ -z "$DATE" ]; then
    echo "❌ Error: Failed to parse version info from ./bot_bin output!"
    exit 1
fi

echo "Building Docker image ${IMAGE_NAME}..."
echo "Using version info -> Version: ${VERSION}, Commit: ${COMMIT}, Date: ${DATE}"
echo "----------"
echo "Docker-context:"
rsync -rcvn \
  --exclude-from='.dockerignore' \
  . /tmp/dummy | \
  sed -e '1,2d' \
      -e '/^$/d' \
      -e '/building file list/d' \
      -e '/sent .* bytes/d' \
      -e '/total size/d'
echo "----------"

docker build \
    --build-arg VERSION="$VERSION" \
    --build-arg COMMIT="$COMMIT" \
    --build-arg DATE="$DATE" \
    -f bot/dockerfile \
    -t "$IMAGE_NAME" \
    .

echo "----------"
echo "Docker image build completed successfully!"
docker run --rm "$IMAGE_NAME" ./bot_bin --version

