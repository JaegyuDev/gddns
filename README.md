# GDDNS
> A Simple Dynamic DNS service using the cloudflare api

# Install 
sudo cp systemd/gddns.* /etc/systemd/system
sudo systemctl daemon-reload
sudo systemctl enable --now gddns.timer

#
Verify
systemctl status gddns.timer
systemctl list-timers gddns.timer