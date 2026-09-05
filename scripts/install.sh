#!/bin/bash
set -e

# NanoContainer One-Line Installer
# Usage: curl -fsSL https://raw.githubusercontent.com/cokovali10-sketch/NanoContainer/master/scripts/install.sh | bash

GITHUB_REPO="cokovali10-sketch/NanoContainer"
INSTALL_DIR="/usr/local/bin"
BINARY_NAME="nanocontainer"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

echo -e "${CYAN}"
echo "  _   _                      _                    ___ _             _   "
echo " | \ | | ___  _ __   ___  / \   _ __  _ __ ___  / _ \ |_ __ _ ____| |_ "
echo " |  \| |/ _ \| '_ \ / _ \/ _ \ | '_ \| '__/ _ \| | | | __/ _ |_  / __|"
echo " | |\  | (_) | | | |  __/ ___ \| |_) | | | (_) | |_| | || (_| |/ /| |_ "
echo " |_| \_|\___/|_| |_|\___/_/   \_\ .__/|_|  \___/ \___/ \__\__,_/___|\__|"
echo "                                  |_|                                    "
echo -e "${NC}"

# Check root
if [ "$EUID" -ne 0 ]; then
    echo -e "${YELLOW}Warning: Not running as root. Some features may require root.${NC}"
fi

# Detect architecture
ARCH=$(uname -m)
case $ARCH in
    x86_64)  ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
    armv7l)  ARCH="arm" ;;
    *)       echo -e "${RED}Unsupported architecture: $ARCH${NC}"; exit 1 ;;
esac

echo -e "${CYAN}[1/4]${NC} Detected architecture: ${ARCH}"

# Check if already installed
if command -v $BINARY_NAME &> /dev/null; then
    CURRENT_VERSION=$($BINARY_NAME --version 2>/dev/null || echo "unknown")
    echo -e "${YELLOW}NanoContainer is already installed (version: $CURRENT_VERSION)${NC}"
    read -p "Update to latest? (y/n) " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        exit 0
    fi
fi

# Get latest release
echo -e "${CYAN}[2/4]${NC} Fetching latest release..."
LATEST=$(curl -s "https://api.github.com/repos/$GITHUB_REPO/releases/latest" | grep -o '"tag_name": "[^"]*"' | cut -d'"' -f4)

if [ -z "$LATEST" ]; then
    LATEST="master"
    DOWNLOAD_URL="https://github.com/$GITHUB_REPO/archive/refs/heads/master.tar.gz"
else
    DOWNLOAD_URL="https://github.com/$GITHUB_REPO/releases/download/$LATEST/nanocontainer-linux-${ARCH}"
fi

echo -e "${CYAN}[3/4]${NC} Downloading NanoContainer ${LATEST}..."

if [[ $DOWNLOAD_URL == *.tar.gz ]]; then
    # Building from source
    TEMP_DIR=$(mktemp -d)
    curl -sL "$DOWNLOAD_URL" | tar xz -C "$TEMP_DIR"
    cd "$TEMP_DIR/NanoContainer-"*
    GOOS=linux GOARCH=$ARCH go build -o $BINARY_NAME ./cmd/nanocontainer
    cp $BINARY_NAME "$INSTALL_DIR/"
    cd -
    rm -rf "$TEMP_DIR"
else
    # Downloading binary
    curl -sL "$DOWNLOAD_URL" -o "$INSTALL_DIR/$BINARY_NAME"
    chmod +x "$INSTALL_DIR/$BINARY_NAME"
fi

echo -e "${CYAN}[4/4]${NC} Verifying installation..."
if command -v $BINARY_NAME &> /dev/null; then
    echo -e "${GREEN}✓ NanoContainer installed successfully!${NC}"
    echo ""
    echo -e "  Version:  ${GREEN}$($BINARY_NAME --version 2>/dev/null || echo $LATEST)${NC}"
    echo -e "  Location: ${GREEN}$INSTALL_DIR/$BINARY_NAME${NC}"
    echo ""
    echo -e "  Quick start:"
    echo -e "    ${CYAN}sudo $BINARY_NAME run alpine /bin/sh${NC}"
    echo ""
    echo -e "  Or start the dashboard:"
    echo -e "    ${CYAN}sudo $BINARY_NAME serve --addr :8080${NC}"
    echo ""
    echo -e "  Documentation: https://github.com/$GITHUB_REPO#readme"
    echo ""
else
    echo -e "${RED}✗ Installation failed. Please check permissions.${NC}"
    exit 1
fi
