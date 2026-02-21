#!/bin/bash

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Configuration
REPO="ZhongWwwHhh/Ops-System"
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

# shellcheck disable=SC1091
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

echo -e "${YELLOW}Config setup...${NC}"

prompt() {
    local message="$1"
    if [ ! -t 0 ] && [ ! -t 1 ] && [ ! -t 2 ]; then
        echo -e "${RED}Error: no TTY available for interactive input.${NC}"
        echo -e "${YELLOW}Run: sudo bash install.sh (not via pipe)${NC}"
        exit 1
    fi
    printf "%s" "$message" > /dev/tty
    IFS= read -r reply < /dev/tty
    printf "%s" "$reply"
}

INPUT_SYSTEM_PUB=$(prompt "Enter system_pubkey (leave empty to create new system): ")
INPUT_CLUSTER=$(prompt "Enter cluster_id (default: default): ")
if [ -z "$INPUT_CLUSTER" ]; then
    INPUT_CLUSTER="default"
fi

if [ -z "$INPUT_SYSTEM_PUB" ]; then
    echo -e "${YELLOW}Creating new system keys and admin proof...${NC}"
    INPUT_VALID_TO=$(prompt "Admin proof valid_to (RFC3339, default: 9999-12-31T00:00:00Z): ")
    if [ -z "$INPUT_VALID_TO" ]; then
        INPUT_VALID_TO="9999-12-31T00:00:00Z"
    fi
    KEYGEN_OUTPUT=$("${BINARY_PATH}" keygen --new-system --cluster-id "${INPUT_CLUSTER}" --admin-valid-to "${INPUT_VALID_TO}")
else
    KEYGEN_OUTPUT=$("${BINARY_PATH}" keygen --cluster-id "${INPUT_CLUSTER}")
fi

if echo "${KEYGEN_OUTPUT}" | grep -q "^ERR="; then
    echo -e "${RED}Key generation failed:${NC}"
    echo "${KEYGEN_OUTPUT}"
    exit 1
fi

while IFS= read -r line; do
    case "$line" in
        NODE_PRIV_B64=*) NODE_PRIV_B64="${line#NODE_PRIV_B64=}" ;;
        NODE_PEER_ID=*) NODE_PEER_ID="${line#NODE_PEER_ID=}" ;;
        SYSTEM_PRIV_B64=*) SYSTEM_PRIV_B64="${line#SYSTEM_PRIV_B64=}" ;;
        SYSTEM_PUB_B64=*) SYSTEM_PUB_B64="${line#SYSTEM_PUB_B64=}" ;;
        ADMIN_PRIV_B64=*) ADMIN_PRIV_B64="${line#ADMIN_PRIV_B64=}" ;;
        ADMIN_PEER_ID=*) ADMIN_PEER_ID="${line#ADMIN_PEER_ID=}" ;;
        ADMIN_PROOF_CLUSTER_ID=*) ADMIN_PROOF_CLUSTER_ID="${line#ADMIN_PROOF_CLUSTER_ID=}" ;;
        ADMIN_PROOF_PEER_ID=*) ADMIN_PROOF_PEER_ID="${line#ADMIN_PROOF_PEER_ID=}" ;;
        ADMIN_PROOF_ROLE=*) ADMIN_PROOF_ROLE="${line#ADMIN_PROOF_ROLE=}" ;;
        ADMIN_PROOF_VALID_FROM=*) ADMIN_PROOF_VALID_FROM="${line#ADMIN_PROOF_VALID_FROM=}" ;;
        ADMIN_PROOF_VALID_TO=*) ADMIN_PROOF_VALID_TO="${line#ADMIN_PROOF_VALID_TO=}" ;;
        ADMIN_PROOF_SIG=*) ADMIN_PROOF_SIG="${line#ADMIN_PROOF_SIG=}" ;;
    esac
done << EOF
${KEYGEN_OUTPUT}
EOF

if [ -z "$INPUT_SYSTEM_PUB" ]; then
    SYSTEM_PUB_KEY="${SYSTEM_PUB_B64}"
else
    SYSTEM_PUB_KEY="${INPUT_SYSTEM_PUB}"
fi

echo -e "${YELLOW}Generating config.json...${NC}"
cat > "${CONFIG_FILE}" << EOF
{
  "init_connections": [
    {
      "type": "dns",
      "address": "init.p2pos.zhongwwwhhh.cc"
    }
  ],
  "cluster_id": "${INPUT_CLUSTER}",
  "system_pubkey": "${SYSTEM_PUB_KEY}",
  "members": [],
  "admin_proof": {
    "cluster_id": "",
    "peer_id": "",
    "role": "",
    "valid_from": "",
    "valid_to": "",
    "sig": ""
  },
  "node_private_key": "${NODE_PRIV_B64}"
}
EOF
echo -e "${GREEN}Config file written at ${CONFIG_FILE}${NC}"

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
echo -e "${YELLOW}Node peer_id: ${NODE_PEER_ID}${NC}"
if [ -z "$INPUT_SYSTEM_PUB" ]; then
    echo -e "${YELLOW}System public key:${NC} ${SYSTEM_PUB_B64}"
    echo -e "${YELLOW}System private key:${NC} ${SYSTEM_PRIV_B64}"
    echo -e "${YELLOW}Admin node private key:${NC} ${ADMIN_PRIV_B64}"
    echo -e "${YELLOW}Admin peer_id:${NC} ${ADMIN_PEER_ID}"
    echo -e "${YELLOW}Admin proof:${NC}"
    cat << EOF
{
  "cluster_id": "${ADMIN_PROOF_CLUSTER_ID}",
  "peer_id": "${ADMIN_PROOF_PEER_ID}",
  "role": "${ADMIN_PROOF_ROLE}",
  "valid_from": "${ADMIN_PROOF_VALID_FROM}",
  "valid_to": "${ADMIN_PROOF_VALID_TO}",
  "sig": "${ADMIN_PROOF_SIG}"
}
EOF
fi
echo -e "${YELLOW}Service management commands:${NC}"
echo "  systemctl status p2pos       - Check service status"
echo "  systemctl restart p2pos      - Restart service"
echo "  systemctl stop p2pos         - Stop service"
echo "  journalctl -u p2pos -f       - View logs"
