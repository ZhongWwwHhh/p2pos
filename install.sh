#!/bin/bash

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Configuration
REPO="ZhongWwwHhh/Ops-System"
APP_NAME="p2pos"
SERVICE_NAME="p2pos"
INSTALL_DIR="$(pwd)"
CONFIG_FILE="${INSTALL_DIR}/config.json"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"

echo -e "${YELLOW}Starting P2POS installation...${NC}"

# Check if running as root
if [ "$EUID" -ne 0 ]; then
    echo -e "${RED}Error: This script must be run as root${NC}"
    echo -e "${YELLOW}Please run: sudo bash $0${NC}"
    exit 1
fi

# Check if running on Ubuntu 24.04
if [ ! -f /etc/os-release ]; then
    echo -e "${RED}Error: Cannot determine OS version${NC}"
    exit 1
fi

source /etc/os-release
if [[ "$VERSION_ID" != "24.04" || "$ID" != "ubuntu" ]]; then
    echo -e "${RED}Error: This script only supports Ubuntu 24.04${NC}"
    echo -e "${RED}Detected: $ID $VERSION_ID${NC}"
    exit 1
fi

# Binary name for Ubuntu 24.04
BINARY_NAME="p2pos-linux"

# Get the latest release
echo -e "${YELLOW}Fetching latest release...${NC}"
LATEST_RELEASE=$(curl -s "https://api.github.com/repos/${REPO}/releases/latest")
DOWNLOAD_URL=$(echo "${LATEST_RELEASE}" | grep "browser_download_url.*${BINARY_NAME}" | cut -d '"' -f 4)

if [ -z "$DOWNLOAD_URL" ]; then
    echo -e "${RED}Error: Could not find release for ${BINARY_NAME}${NC}"
    exit 1
fi

RELEASE_VERSION=$(echo "${LATEST_RELEASE}" | grep '"tag_name"' | head -1 | cut -d '"' -f 4)
echo -e "${GREEN}Found release: ${RELEASE_VERSION}${NC}"
echo -e "${YELLOW}Download URL: ${DOWNLOAD_URL}${NC}"

# Download binary
echo -e "${YELLOW}Downloading binary...${NC}"
BINARY_PATH="${INSTALL_DIR}/${BINARY_NAME}"
curl -L -o "${BINARY_PATH}" "${DOWNLOAD_URL}"

if [ ! -f "${BINARY_PATH}" ]; then
    echo -e "${RED}Error: Failed to download binary${NC}"
    exit 1
fi

chmod +x "${BINARY_PATH}"
echo -e "${GREEN}Binary downloaded and made executable${NC}"

# Create config.json if it doesn't exist
if [ ! -f "${CONFIG_FILE}" ]; then
    echo -e "${YELLOW}Creating config.json...${NC}"
    cat > "${CONFIG_FILE}" << 'EOF'
{
  "init_connections": [
    {
      "type": "dns",
      "address": "init.p2pos.zhongwwwhhh.cc"
    }
  ]
}
EOF
    echo -e "${GREEN}Config file created at ${CONFIG_FILE}${NC}"
else
    echo -e "${YELLOW}Config file already exists at ${CONFIG_FILE}${NC}"
fi

# Create systemd service
echo -e "${YELLOW}Creating systemd service...${NC}"

cat > "${SERVICE_FILE}" << EOF
[Unit]
Description=P2POS Service
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=${INSTALL_DIR}
ExecStart=${BINARY_PATH}
Restart=always
RestartSec=2
StartLimitIntervalSec=0
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable "${SERVICE_NAME}"
systemctl restart "${SERVICE_NAME}"

echo -e "${GREEN}Service installed and started${NC}"
echo -e "${YELLOW}Service status:${NC}"
systemctl status "${SERVICE_NAME}" --no-pager

echo ""
echo -e "${GREEN}Installation complete!${NC}"
echo -e "${YELLOW}Binary location: ${BINARY_PATH}${NC}"
echo -e "${YELLOW}Config location: ${CONFIG_FILE}${NC}"
echo ""
echo -e "${YELLOW}Service management commands:${NC}"
echo "  systemctl status p2pos       - Check service status"
echo "  systemctl restart p2pos      - Restart service"
echo "  systemctl stop p2pos         - Stop service"
echo "  journalctl -u p2pos -f       - View logs"
