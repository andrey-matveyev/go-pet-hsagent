# Инструкция по деплою агента Home Monitor (hsagent) на домашний сервер (Ubuntu)

Данная инструкция описывает процесс деплоя и обновления сервиса `hsagent` на домашнем сервер под управлением Ubuntu Linux с использованием автоматизированного скрипта деплоя (`.dev/cmd/deploy_agent.sh`) или вручную.

Новая рабочая директория сервиса на сервере — `/opt/homemonitor` (ранее располагалась в `/root/homemonitor`), работа с которой осуществляется под выделенным пользователем (например, `andrey`) с ограниченными правами через `sudo` (без необходимости полного доступа `root`).

---

## Предварительные требования

1. Доступ к серверу по SSH (настроенный SSH-ключ).
2. Настроенный пользователь на удаленном сервере (например, `andrey`) с правами на запись в `/opt/homemonitor` и выполнение определенных команд через `sudo` без пароля (см. ниже настройку sudoers).
3. Установленный `rsync` и `ssh` на локальной машине.

---

## Настройка прав `sudo` на сервере (один раз)

Для того чтобы скрипт деплоя мог управлять службой `systemd` и обновлять unit-файл без предоставления полного доступа `root`, настройте права через `visudo`:

```bash
sudo visudo
```

Добавьте в конец файла следующие строки (замените `andrey` на имя вашего пользователя):

```sudo
# Declare a list of allowed commands for hsagent:
Cmnd_Alias HSAGENT_CMDS = /usr/bin/systemctl start hsagent, \
                          /usr/bin/systemctl stop hsagent, \
                          /usr/bin/systemctl restart hsagent, \
                          /usr/bin/systemctl status hsagent *, \
                          /usr/bin/systemctl daemon-reload, \
                          /usr/bin/cp /opt/homemonitor/hsagent.service /etc/systemd/system/hsagent.service

# Assign access rights to user:
andrey ALL=(ALL) NOPASSWD: HSAGENT_CMDS
```

---

## Вариант А. Автоматический деплой с помощью скрипта (рекомендуется)

В проекте предусмотрен готовый скрипт деплоя `.dev/cmd/deploy_agent.sh`, который автоматически собирает бинарник под Linux amd64, синхронизирует файлы через `rsync` в `/opt/homemonitor`, обновляет `systemd` unit-файл, устанавливает права и бесшовно перезапускает сервис.

1. Запустите скрипт деплоя с параметрами по умолчанию (или задайте свои через переменные окружения):
   ```bash
   ./.dev/cmd/deploy_agent.sh
   ```

2. При необходимости переопределить хост или пользователя:
   ```bash
   REMOTE_HOST=192.168.50.201 REMOTE_USER=andrey ./.dev/cmd/deploy_agent.sh
   ```

Скрипт выполняет следующие шаги:
- Сборку бинарника `agent_bin` для Linux (`GOOS=linux GOARCH=amd64`).
- Создание папки `/opt/homemonitor` (если она еще не создана).
- Синхронизацию бинарника, конфигурации (`agent/config.toml`) и службы (`agent/hsagent.service`) через `rsync`.
- Копирование `hsagent.service` в `/etc/systemd/system/` и вызов `sudo systemctl daemon-reload`.
- Выставление прав `chmod +x` на бинарный файл.
- Бесшовный перезапуск службы (`sudo systemctl restart hsagent`) и вывод её текущего статуса.

---

## Вариант Б. Ручной деплой

Если вы хотите выполнить деплой вручную по шагам:

### Шаг 1. Подключение к серверу по SSH
```bash
ssh andrey@192.168.50.201
```

### Шаг 2. Замена файлов на сервере (через rsync или scp)
С локальной машины отправьте файлы в `/opt/homemonitor`:
```bash
scp agent_bin andrey@192.168.50.201:/opt/homemonitor/agent_bin
scp agent/config.toml andrey@192.168.50.201:/opt/homemonitor/config.toml
scp agent/hsagent.service andrey@192.168.50.201:/opt/homemonitor/hsagent.service
```

### Шаг 3. Установка прав и обновление systemd
На сервере выполните:
```bash
chmod +x /opt/homemonitor/agent_bin
sudo cp /opt/homemonitor/hsagent.service /etc/systemd/system/hsagent.service
sudo systemctl daemon-reload
```

### Шаг 4. Перезапуск и проверка службы
```bash
sudo systemctl restart hsagent
sudo systemctl status hsagent --no-pager
```

---

## Проверка работоспособности и логов

1. Проверить статус сервиса:
   ```bash
   sudo systemctl status hsagent
   ```
   *Статус должен быть `active (running)`.*

2. Просмотреть логи сервиса в реальном времени:
   ```bash
   sudo journalctl -u hsagent -f -n 100
   ```

