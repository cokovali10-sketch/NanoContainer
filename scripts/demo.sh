#!/bin/bash
set -e

# NanoContainer Interactive Demo
# This script demonstrates all major features

GITHUB_REPO="cokovali10-sketch/NanoContainer"
BINARY="nanocontainer"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
MAGENTA='\033[0;35m'
NC='\033[0m'

print_header() {
    echo ""
    echo -e "${CYAN}══════════════════════════════════════════════════════════════════════════════${NC}"
    echo -e "${CYAN}  $1${NC}"
    echo -e "${CYAN}══════════════════════════════════════════════════════════════════════════════${NC}"
    echo ""
}

print_step() {
    echo -e "${MAGENTA}[$1]${NC} $2"
}

print_success() {
    echo -e "${GREEN}  ✓ $1${NC}"
}

print_info() {
    echo -e "${CYAN}  ℹ $1${NC}"
}

print_warning() {
    echo -e "${YELLOW}  ⚠ $1${NC}"
}

# Check if running as root
if [ "$EUID" -ne 0 ]; then
    echo -e "${YELLOW}Warning: This demo requires root for full functionality.${NC}"
    echo -e "${YELLOW}Run with: sudo ./scripts/demo.sh${NC}"
    echo ""
fi

print_header "NanoContainer Interactive Demo"

echo -e "${GREEN}Welcome to NanoContainer!${NC}"
echo -e "This demo will guide you through all major features."
echo ""
read -p "Press Enter to start..."

# Step 1: Check installation
print_step "1/8" "Checking installation..."
if command -v $BINARY &> /dev/null; then
    VERSION=$($BINARY --version 2>/dev/null || echo "unknown")
    print_success "NanoContainer installed (version: $VERSION)"
else
    print_warning "NanoContainer not found. Building from source..."
    GOOS=linux GOARCH=amd64 go build -o $BINARY ./cmd/nanocontainer
    print_success "Built nanocontainer binary"
fi

# Step 2: Prepare image
print_step "2/8" "Preparing Alpine Linux image..."
IMAGES_DIR="/var/lib/nanocontainer/images"
sudo mkdir -p $IMAGES_DIR

if [ ! -f "$IMAGES_DIR/alpine.tar.gz" ]; then
    print_info "Downloading Alpine Linux rootfs..."
    sudo wget -q -O "$IMAGES_DIR/alpine.tar.gz" \
        "https://dl-cdn.alpinelinux.org/alpine/v3.19/releases/x86_64/alpine-minirootfs-3.19.1-x86_64.tar.gz"
    print_success "Alpine Linux downloaded"
else
    print_success "Alpine Linux already present"
fi

# Step 3: List images
print_step "3/8" "Listing available images..."
./$BINARY images

# Step 4: Run first container
print_step "4/8" "Running your first container..."
echo -e "${CYAN}  Creating container with interactive shell...${NC}"
CONTAINER_ID=$(./$BINARY run -d alpine /bin/sh -c "echo 'Hello from NanoContainer!'; sleep 30")
print_success "Container started: $CONTAINER_ID"

# Step 5: List containers
print_step "5/8" "Listing running containers..."
./$BINARY ps

# Step 6: View logs
print_step "6/8" "Viewing container logs..."
./$BINARY logs $CONTAINER_ID 2>/dev/null || print_info "Logs will be available shortly"

# Step 7: Inspect container
print_step "7/8" "Inspecting container..."
./$BINARY inspect $CONTAINER_ID 2>/dev/null | head -20 || print_info "Inspect data available"

# Step 8: Stop container
print_step "8/8" "Stopping container..."
./$BINARY stop $CONTAINER_ID 2>/dev/null || true
print_success "Container stopped"

# Summary
print_header "Demo Complete!"

echo -e "${GREEN}You've successfully run NanoContainer!${NC}"
echo ""
echo -e "What you can do next:"
echo -e "  ${CYAN}1.${NC} Start the web dashboard:"
echo -e "     ${GREEN}sudo ./nanocontainer serve --addr :8080${NC}"
echo ""
echo -e "  ${CYAN}2.${NC} Run more containers:"
echo -e "     ${GREEN}sudo ./nanocontainer run alpine /bin/sh${NC}"
echo ""
echo -e "  ${CYAN}3.${NC} Deploy with compose:"
echo -e "     ${GREEN}sudo ./nanocontainer compose up nanocontainer-compose.yml${NC}"
echo ""
echo -e "  ${CYAN}4.${NC} Build images from Dockerfiles:"
echo -e "     ${GREEN}sudo ./nanocontainer build --file Dockerfile --tag myapp${NC}"
echo ""
echo -e "Documentation: https://github.com/$GITHUB_REPO#readme"
echo ""
echo -e "${GREEN}Star ⭐ the repo if you like it!${NC}"
echo ""
