#!/bin/bash
set -e

if [ "$(id -u)" -ne 0 ]; then
    echo "Please run as root"
    exit 1
fi

mkdir -p /etc/gddns

if [ ! -f /etc/gddns/config.json ]; then
    cp config/config.example.json /etc/gddns/config.json
fi
if [ ! -f /etc/gddns/.env ]; then
    cp config/.env.example /etc/gddns/.env
fi

cp gddns /usr/local/bin/gddns
chown -R nobody:nogroup /etc/gddns
chmod 600 /etc/gddns/.env /etc/gddns/config.json

cp systemd/gddns.* /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now gddns.timer

echo "Installed. Edit /etc/gddns/config.json and /etc/gddns/.env before starting."