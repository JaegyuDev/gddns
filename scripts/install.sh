#!/bin/bash
set -e

if [ "$(id -u)" -ne 0 ]; then
    echo "Please run as root"
    exit 1
fi

# Create dedicated service user if it doesn't exist
if ! id -u gddns &>/dev/null; then
    useradd --system --no-create-home --shell /usr/sbin/nologin gddns
    echo "Created gddns user."
fi

mkdir -p /etc/gddns

if [ ! -f /etc/gddns/config.json ]; then
    cp config/config.example.json /etc/gddns/config.json
fi
if [ ! -f /etc/gddns/.env ]; then
    cp config/.env.example /etc/gddns/.env
fi

cp gddns /usr/local/bin/gddns
chown -R gddns:gddns /etc/gddns
chmod 600 /etc/gddns/.env /etc/gddns/config.json

cp systemd/gddns.* /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now gddns.timer

echo "Installed. Edit /etc/gddns/config.json and /etc/gddns/.env before starting."
echo "To read config files without sudo, run: sudo usermod -aG gddns \$USER"