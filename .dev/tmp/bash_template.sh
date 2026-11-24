#!/bin/bash

# set script as executable
chmod +x .dev/templates/bash_template.sh

# update and upgrade packages
sudo apt update
sudo apt upgrade
# почистить неиспользуемые пакеты:
# список к удалению без удаления
sudo apt autoremove --dry-run
# удалить неиспользуемые пакеты
sudo apt autoremove
# почистить кэш скачанных .deb-файлов
sudo apt clean

# realtime hsagent's logs from home-server
journalctl -u hsagent.service -f
# полезные варианты:
journalctl -u hsagent.service -n 50 -f             # Только последние 50 строк + новые в реальном времени
journalctl -u hsagent.service --since today -f     # С начала сегодняшнего дня
journalctl -u hsagent.service -f -o cat            # Без служебных строк systemd (только stdout/stderr самого агента)
ssh home-server "journalctl -u hsagent.service -f" # Если нужен доступ по SSH
