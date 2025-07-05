#!/bin/bash

set -e

# --- Configuration ---
GITHUB_REPO="webwizards-team/phantom-tunnel"
SOURCE_FILE_URL="https://raw.githubusercontent.com/${GITHUB_REPO}/main/phantom.go"

if [ "$(id -u)" -ne 0 ]; then
  echo "This script must be run as root. Please use sudo." >&2
  exit 1
fi

echo "Checking for dependencies (Go, curl/wget)..."
if ! command -v go &> /dev/null; then
  echo "Go compiler not found. Installing..."
  if command -v apt-get &> /dev/null; then apt-get update && apt-get install -y golang-go;
  elif command -v yum &> /dev/null; then yum install -y golang;
  else echo "Cannot install Go automatically. Please install it manually."; exit 1; fi
fi
if ! command -v curl &> /dev/null && ! command -v wget &> /dev/null; then
    echo "Error: This script requires either curl or wget." >&2; exit 1;
fi

echo "Downloading the latest source code..."
TMP_DIR=$(mktemp -d)
trap 'rm -rf -- "$TMP_DIR"' EXIT
if command -v curl &> /dev/null; then curl -sSL -o "$TMP_DIR/phantom.go" "$SOURCE_FILE_URL"
else wget -q -O "$TMP_DIR/phantom.go" "$SOURCE_FILE_URL"; fi
cd "$TMP_DIR"

echo "Initializing Go module and fetching dependencies..."
go mod init phantom-tunnel || true # اگر فایل go.mod از قبل وجود دارد، خطا ندهد

# --- خط جدیدی که اضافه می‌کنید ---
go get nhooyr.io/websocket@v1.8.6 # یا v1.8.7 اگر 1.8.6 کار نکرد
# --- پایان خط جدید ---

go mod tidy

echo "Compiling the 'phantom-tunnel' application..."
go build -ldflags="-s -w" -o phantom-tunnel phantom.go

INSTALL_PATH="/usr/local/bin"
echo "Installing 'phantom-tunnel' to $INSTALL_PATH..."
mv phantom-tunnel "$INSTALL_PATH/"
chmod +x "$INSTALL_PATH/phantom-tunnel"

echo ""
echo "✅ Phantom Tunnel has been installed successfully!"
echo ""
echo "Just run 'phantom-tunnel' anywhere on your system to start the interactive setup."
echo ""

exit 0
