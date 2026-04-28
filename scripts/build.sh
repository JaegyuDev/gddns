#!/bin/bash
set -e

go build -ldflags="-X main.devMode=false" -o gddns .
echo "Built successfully. Run install.sh as root to install."