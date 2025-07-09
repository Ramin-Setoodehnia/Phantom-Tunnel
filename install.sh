#!/bin/bash

# این اسکریپت با مشاهده هرگونه خطا، اجرا را متوقف می‌کند
set -e

# --- پیکربندی ---
GITHUB_REPO="webwizards-team/phantom-tunnel"
INSTALL_PATH="/usr/local/bin"
EXECUTABLE_NAME="phantom-tunnel"

# --- توابع کمکی برای خوانایی بهتر ---
print_info() {
    echo -e "\e[34m[INFO]\e[0m $1"
}

print_success() {
    echo -e "\e[32m[SUCCESS]\e[0m $1"
}

print_error() {
    echo -e "\e[31m[ERROR]\e[0m $1" >&2
}

# --- شروع اسکریپت ---
print_info "Starting Phantom Tunnel Installation..."

# ۱. بررسی دسترسی روت
if [ "$(id -u)" -ne 0 ]; then
  print_error "This script must be run as root. Please use 'sudo'."
  exit 1
fi

# ۲. بررسی و نصب وابستگی‌های اصلی (Go, Git, Curl)
print_info "Checking for core dependencies (Go, Git, curl)..."
PACKAGE_MANAGER=""
if command -v apt-get &> /dev/null; then
  PACKAGE_MANAGER="apt-get"
elif command -v yum &> /dev/null; then
  PACKAGE_MANAGER="yum"
else
  print_error "Unsupported package manager. Please install dependencies manually."
  exit 1
fi

# نصب Go
if ! command -v go &> /dev/null; then
  print_info "Go compiler not found. Installing..."
  if [ "$PACKAGE_MANAGER" = "apt-get" ]; then
    apt-get update && apt-get install -y golang-go
  else
    yum install -y golang
  fi
fi

# نصب Git
if ! command -v git &> /dev/null; then
  print_info "Git not found. Installing..."
  if [ "$PACKAGE_MANAGER" = "apt-get" ]; then
    apt-get install -y git
  else
    yum install -y git
  fi
fi

# نصب Curl
if ! command -v curl &> /dev/null; then
  print_info "curl not found. Installing..."
  if [ "$PACKAGE_MANAGER" = "apt-get" ]; then
    apt-get install -y curl
  else
    yum install -y curl
  fi
fi

# ۳. کامپایل کردن برنامه اصلی از سورس
print_info "Downloading and compiling the Phantom Tunnel application..."
TMP_DIR=$(mktemp -d)
trap 'rm -rf -- "$TMP_DIR"' EXIT # اطمینان از پاک شدن دایرکتوری موقت در انتها
cd "$TMP_DIR"

SOURCE_FILE_URL="https://raw.githubusercontent.com/${GITHUB_REPO}/main/phantom.go"
curl -sSL -o "phantom.go" "$SOURCE_FILE_URL"

export GOPROXY=direct # جلوگیری از خطای 403 در برخی سرورها
go mod init phantom-tunnel &>/dev/null || true
go get nhooyr.io/websocket &>/dev/null
go get github.com/hashicorp/yamux &>/dev/null
go mod tidy &>/dev/null

go build -ldflags="-s -w" -o "$EXECUTABLE_NAME" phantom.go
mv "$EXECUTABLE_NAME" "$INSTALL_PATH/"
chmod +x "$INSTALL_PATH/$EXECUTABLE_NAME"
print_success "Phantom Tunnel application compiled and installed."

# --- پایان ---
echo ""
print_success "Installation is complete!"
echo "--------------------------------------------------"
echo "To run the tunnel, simply type this command anywhere:"
echo "  $EXECUTABLE_NAME"
echo "--------------------------------------------------"

exit 0
