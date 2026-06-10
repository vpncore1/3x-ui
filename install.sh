#!/usr/bin/env bash
# Install 3x-ui (official) then apply subscription + balancer patch.
set -euo pipefail

red='\033[0;31m'
green='\033[0;32m'
yellow='\033[0;33m'
plain='\033[0m'

[[ $EUID -ne 0 ]] && echo -e "${red}Run as root${plain}" && exit 1

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo -e "${green}==> Step 1: Install base 3x-ui (if needed)${plain}"
if [[ ! -f /usr/local/x-ui/x-ui ]]; then
  echo -e "${yellow}3x-ui not found — installing official panel first...${plain}"
  bash <(curl -Ls https://raw.githubusercontent.com/MHSanaei/3x-ui/master/install.sh)
else
  echo "3x-ui already installed, skipping base install."
fi

echo -e "${green}==> Step 2: Apply subscription + balancer patch${plain}"
if [[ -f "$SCRIPT_DIR/upgrade.sh" ]]; then
  bash "$SCRIPT_DIR/upgrade.sh"
elif [[ -f /opt/3x-ui-sub-balancer/upgrade.sh ]]; then
  bash /opt/3x-ui-sub-balancer/upgrade.sh
else
  echo -e "${red}upgrade.sh not found. Clone the private repo first:${plain}"
  echo "  export GITHUB_TOKEN=ghp_xxxxxxxx"
  echo "  git clone https://github.com/sader21/3x-ui-sub-balancer.git /opt/3x-ui-sub-balancer"
  echo "  bash /opt/3x-ui-sub-balancer/install.sh"
  exit 1
fi

echo -e "${green}Done. Open the panel URL shown during install.${plain}"
