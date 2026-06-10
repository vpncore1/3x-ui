#!/usr/bin/env bash
# Install 3x-ui (official) then apply subscription + balancer patch.
set -euo pipefail

REPO_RAW="${REPO_RAW:-https://raw.githubusercontent.com/sader21/3x-ui-sub-balancer/main}"

red='\033[0;31m'
green='\033[0;32m'
yellow='\033[0;33m'
plain='\033[0m'

[[ $EUID -ne 0 ]] && echo -e "${red}Run as root${plain}" && exit 1

echo -e "${green}==> Step 1: Install base 3x-ui (if needed)${plain}"
if [[ ! -f /usr/local/x-ui/x-ui ]]; then
  echo -e "${yellow}3x-ui not found — installing official panel first...${plain}"
  bash <(curl -Ls https://raw.githubusercontent.com/MHSanaei/3x-ui/master/install.sh)
else
  echo "3x-ui already installed, skipping base install."
fi

echo -e "${green}==> Step 2: Apply subscription + balancer patch${plain}"
bash <(curl -Ls "$REPO_RAW/upgrade.sh")

echo -e "${green}Done. Open the panel URL shown during install.${plain}"
