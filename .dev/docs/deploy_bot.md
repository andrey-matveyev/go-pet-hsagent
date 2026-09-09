# Инструкция по деплою Telegram-бота мониторинга (hsagent-bot) на домашний сервер (Docker / CasaOS)

Данная инструкция описывает процесс деплоя и обновления Telegram-бота (`hsagent-bot`) на домашнем сервере под управлением Ubuntu Linux с использованием Docker / CasaOS. Деплой выполняется с помощью автоматизированного скрипта `.dev/cmd/deploy_bot_docker.sh` или вручную по шагам.

Работа с сервером осуществляется под выделенным пользователем (например, `andrey`) с ограниченными правами через `sudo` (без необходимости полного доступа `root`).

---

## Предварительные требования

1. Доступ к серверу по SSH (настроенный SSH-ключ).
2. Настроенный пользователь на удаленном сервере (например, `andrey`) с правами на запись в рабочие директории и выполнение определенных команд через `sudo` без пароля (см. ниже настройку sudoers).
3. Установленный `Docker`, `Docker Compose`, `rsync` и `ssh` на локальной машине и сервере.
4. Установленная панель CasaOS на сервере (приложение управляется через интеграцию с CasaOS).

---

## Настройка каталогов и прав `sudo` на сервере (один раз)

### 1. Создание необходимых директорий и выдача прав
На удаленном сервере создайте рабочие директории и назначьте владельца (замените `andrey` на имя вашего пользователя):

```bash
sudo mkdir -p /opt/hsagent-bot /var/lib/casaos/apps/hsagent-bot
sudo chown -R andrey:andrey /opt/hsagent-bot /var/lib/casaos/apps/hsagent-bot
```

### 2. Создание файла конфигурации `.env`
В директории приложения CasaOS необходимо создать обязательный файл окружения `.env` с токеном Telegram-бота и ID чата:

```bash
nano /var/lib/casaos/apps/hsagent-bot/.env
```
Пример содержимого файла:
```env
TG_TOKEN=your_telegram_bot_token_here
TG_CHAT_ID=your_telegram_chat_id_here
```

### 3. Настройка прав `sudo` через `visudo`
Для того чтобы скрипт деплоя мог загружать Docker-образ, управлять контейнерами через `docker compose` и обновлять конфигурацию без предоставления полного доступа `root`, настройте права:

```bash
sudo visudo
```

Добавьте в конец файла следующие строки (замените `andrey` на имя вашего пользователя):

```sudo
# Declare a list of allowed commands for hsagent-bot deployment:
Cmnd_Alias BOT_CMDS = /usr/bin/docker load -i /opt/hsagent-bot/hsagent-bot.tar, \
                      /usr/bin/docker compose *, \
                      /usr/bin/docker rm -f hs_bot_container, \
                      /usr/bin/docker ps *, \
                      /usr/bin/cp /opt/hsagent-bot/docker-compose.yml /var/lib/casaos/apps/hsagent-bot/docker-compose.yml

# Assign access rights to user:
andrey ALL=(ALL) NOPASSWD: BOT_CMDS
```

---

## Вариант А. Автоматический деплой с помощью скрипта (рекомендуется)

В проекте предусмотрен готовый скрипт деплоя `.dev/cmd/deploy_bot_docker.sh`, который автоматически собирает локальный бинарник бота, упаковывает его в Docker-образ, сохраняет в `.tar`-архив, синхронизирует файлы на удаленный сервер через `rsync`, проверяет наличие `.env`, обновляет `docker-compose.yml` в каталоге CasaOS и перезапускает контейнер.

1. Запустите скрипт деплоя с параметрами по умолчанию (или задайте свои через переменные окружения):
   ```bash
   ./.dev/cmd/deploy_bot_docker.sh
   ```

2. При необходимости переопределить хост или пользователя:
   ```bash
   REMOTE_HOST=192.168.50.201 REMOTE_USER=andrey ./.dev/cmd/deploy_bot_docker.sh
   ```

Скрипт выполняет следующие шаги:
- Сборку бинарника бота (`bot_bin`) и Docker-образа `hsagent-bot:latest` локально (`.dev/cmd/build_bot_docker.sh`).
- Сохранение Docker-образа в архив `/tmp/hsagent-bot.tar`.
- Синхронизацию `.tar`-архива и `bot/docker-compose.yml` в удаленную директорию `/opt/hsagent-bot` через `rsync`.
- Проверку наличия обязательного файла `/var/lib/casaos/apps/hsagent-bot/.env`.
- Остановку старого контейнера, загрузку нового образа (`docker load`), копирование `docker-compose.yml` в каталог CasaOS и запуск через `docker compose up -d`.
- Удаление временных файлов и вывод текущего статуса контейнера.

---

## Вариант Б. Ручной деплой

Если вы хотите выполнить деплой бота вручную по шагам:

### Шаг 1. Сборка Docker-образа и сохранение в архив на локальной машине
```bash
# Сборка бинарника и Docker-образа
.dev/cmd/build_bot_docker.sh

# Сохранение образа в tar-архив
docker save hsagent-bot:latest -o /tmp/hsagent-bot.tar
```

### Шаг 2. Загрузка файлов на удаленный сервер
Отправьте архив с образом и файл `docker-compose.yml` в `/opt/hsagent-bot`:
```bash
rsync -avz /tmp/hsagent-bot.tar andrey@192.168.50.201:/opt/hsagent-bot/hsagent-bot.tar
rsync -avz bot/docker-compose.yml andrey@192.168.50.201:/opt/hsagent-bot/docker-compose.yml
```

### Шаг 3. Подключение к серверу и развертывание
Подключитесь к серверу по SSH и выполните команды деплоя:
```bash
ssh andrey@192.168.50.201
```

На сервере:
```bash
# Удаление старого контейнера (если остался)
sudo docker rm -f hs_bot_container || true

# Загрузка Docker-образа из архива
sudo docker load -i /opt/hsagent-bot/hsagent-bot.tar

# Копирование docker-compose.yml в каталог приложения CasaOS
sudo cp /opt/hsagent-bot/docker-compose.yml /var/lib/casaos/apps/hsagent-bot/docker-compose.yml

# Переход в каталог CasaOS и запуск через docker compose (с подтягиванием .env)
cd /var/lib/casaos/apps/hsagent-bot
sudo docker compose down --remove-orphans || true
sudo docker compose up -d
```

---

## Проверка работоспособности и логов

1. Проверить статус контейнера:
   ```bash
   sudo docker ps --filter name=hs_bot_container
   ```
   *Контейнер должен быть в статусе `Up`.*

2. Просмотреть логи бота в реальном времени:
   ```bash
   sudo docker logs -f hs_bot_container
   ```
