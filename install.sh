#!/bin/bash

set -e

# --- پیکربندی ---
GITHUB_REPO="webwizards-team/phantom-tunnel"
INSTALL_PATH="/usr/local/bin"
EXECUTABLE_NAME="phantom-tunnel"
SOURCE_FILE_NAME="phantom.go"

# --- توابع کمکی ---
print_info() { echo -e "\e[34m[INFO]\e[0m $1"; }
print_success() { echo -e "\e[32m[SUCCESS]\e[0m $1"; }
print_error() { echo -e "\e[31m[ERROR]\e[0m $1" >&2; }

# --- شروع اسکریپت ---
print_info "Starting Phantom Tunnel Installation..."

# ۱. بررسی دسترسی روت
if [ "$(id -u)" -ne 0 ]; then
  print_error "This script must be run as root. Please use 'sudo'."
  exit 1
fi

# ۲. نصب وابستگی‌های اصلی
print_info "Checking for core dependencies (Go, Git, curl)..."
PACKAGE_MANAGER=""
if command -v apt-get &> /dev/null; then PACKAGE_MANAGER="apt-get"; elif command -v yum &> /dev/null; then PACKAGE_MANAGER="yum"; else print_error "Unsupported package manager."; exit 1; fi
if ! command -v go &> /dev/null; then print_info "Go not found. Installing..."; if [ "$PACKAGE_MANAGER" = "apt-get" ]; then apt-get update && apt-get install -y golang-go; else yum install -y golang; fi; fi
if ! command -v git &> /dev/null; then print_info "Git not found. Installing..."; if [ "$PACKAGE_MANAGER" = "apt-get" ]; then apt-get install -y git; else yum install -y git; fi; fi
if ! command -v curl &> /dev/null; then print_info "curl not found. Installing..."; if [ "$PACKAGE_MANAGER" = "apt-get" ]; then apt-get install -y curl; else yum install -y curl; fi; fi

# ۳. کامپایل برنامه اصلی
print_info "Downloading and compiling the Phantom Tunnel application..."
TMP_DIR=$(mktemp -d); trap 'rm -rf -- "$TMP_DIR"' EXIT; cd "$TMP_DIR"
SOURCE_FILE_URL="https://raw.githubusercontent.com/webwizards-team/${GITHUB_REPO}/main/phantom.go}"
curl -sSL -o "${SOURCE_FILE_NAME}" "$SOURCE_FILE_URL"
export GOPROXY=direct; go mod init phantom-tunnel &>/dev/null || true
go get nhooyr.io/websocket &>/dev/null; go get github.com/hashicorp/yamux &>/dev/null; go mod tidy &>/dev/null
go build -ldflags="-s -w" -o "$EXECUTABLE_NAME" "${SOURCE_FILE_NAME}"
mv "$EXECUTABLE_NAME" "$INSTALL_PATH/"; chmod +x "$INSTALL_PATH/$EXECUTABLE_NAME"
print_success "Phantom Tunnel application compiled and installed."

# ۴. بهینه‌سازی سیستم‌عامل برای عملکرد بالا
print_info "Optimizing system for high concurrency..."
LIMITS_CONF="/etc/security/limits.conf"
if ! grep -q "phantom-tunnel-optimizations" "$LIMITS_CONF"; then
    print_info "Increasing file descriptor limits..."
    cat >> "$LIMITS_CONF" <<EOF

# BEGIN: phantom-tunnel-optimizations
* soft nofile 65536
* hard nofile 65536
# END: phantom-tunnel-optimizations
EOF
    print_success "System limits optimized."
else
    print_info "System limits already optimized. Skipping."
fi

# --- پایان ---
echo ""
print_success "Installation and optimization is complete!"
echo "--------------------------------------------------"
echo "To apply new system limits, please log out and log back in, or reboot the server."
echo "Then, to run the tunnel, simply type this command anywhere:"
echo "  $EXECUTABLE_NAME"
echo "--------------------------------------------------"

exit 0
