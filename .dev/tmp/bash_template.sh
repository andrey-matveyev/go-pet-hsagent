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