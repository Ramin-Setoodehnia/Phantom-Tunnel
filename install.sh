#!/bin/bash

set -e

# تنظیم GOPROXY به direct برای جلوگیری از خطای 403 هنگام دانلود ماژول‌ها
export GOPROXY=direct

# --- Configuration ---
GITHUB_REPO="webwizards-team/phantom-tunnel"
SOURCE_FILE_URL="https://raw.githubusercontent.com/${GITHUB_REPO}/main/phantom.go"

# بررسی اینکه اسکریپت با دسترسی روت اجرا شده است
if [ "$(id -u)" -ne 0 ]; then
  echo "This script must be run as root. Please use sudo." >&2
  exit 1
fi

echo "Checking for dependencies (Go, curl/wget, git)..."
# بررسی و نصب Go compiler
if ! command -v go &> /dev/null; then
  echo "Go compiler not found. Installing..."
  if command -v apt-get &> /dev/null; then
    apt-get update && apt-get install -y golang-go
  elif command -v yum &> /dev/null; then
    yum install -y golang
  else
    echo "Cannot install Go automatically. Please install it manually."
    exit 1
  fi
fi

# بررسی وجود curl یا wget
if ! command -v curl &> /dev/null && ! command -v wget &> /dev/null; then
    echo "Error: This script requires either curl or wget." >&2
    exit 1
fi

# اضافه کردن بررسی و نصب git
if ! command -v git &> /dev/null; then
  echo "Git not found. Installing..."
  if command -v apt-get &> /dev/null; then
    apt-get update && apt-get install -y git
  elif command -v yum &> /dev/null; then
    yum install -y git
  else
    echo "Cannot install Git automatically. Please install it manually."
    exit 1
  fi
fi


echo "Downloading the latest source code..."
# ایجاد دایرکتوری موقت و تنظیم trap برای حذف آن
TMP_DIR=$(mktemp -d)
trap 'rm -rf -- "$TMP_DIR"' EXIT
# دانلود فایل phantom.go به دایرکتوری موقت
if command -v curl &> /dev/null; then
  curl -sSL -o "$TMP_DIR/phantom.go" "$SOURCE_FILE_URL"
else
  wget -q -O "$TMP_DIR/phantom.go" "$SOURCE_FILE_URL"
fi
# تغییر دایرکتوری به دایرکتوری موقت
cd "$TMP_DIR"

echo "Initializing Go module and fetching dependencies..."
# مقداردهی اولیه ماژول Go (اگر وجود نداشته باشد)
go mod init phantom-tunnel || true
# دانلود نسخه مناسب nhooyr.io/websocket
go get nhooyr.io/websocket
# تمیز کردن و دانلود سایر وابستگی‌ها
go mod tidy

echo "Compiling the 'phantom-tunnel' application..."
# کامپایل برنامه
go build -ldflags="-s -w" -o phantom-tunnel phantom.go

INSTALL_PATH="/usr/local/bin"
echo "Installing 'phantom-tunnel' to $INSTALL_PATH..."
# انتقال فایل اجرایی به مسیر نصب
mv phantom-tunnel "$INSTALL_PATH/"
# اعطای مجوز اجرایی
chmod +x "$INSTALL_PATH/phantom-tunnel"

echo ""
echo "✅ Phantom Tunnel has been installed successfully!"
echo ""
echo "Just run 'phantom-tunnel' anywhere on your system to start the interactive setup."
echo ""

exit 0
