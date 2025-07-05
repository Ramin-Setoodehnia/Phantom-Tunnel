#!/bin/bash

set -e

if [ "$(id -u)" -ne 0 ]; then
  echo "This script must be run as root. Please use sudo." >&2
  exit 1
fi

echo "Checking for dependencies..."
if ! command -v go &> /dev/null; then
  echo "Go compiler not found. Attempting to install..."
  if command -v apt-get &> /dev/null; then
    apt-get update
    apt-get install -y golang-go
  elif command -v yum &> /dev/null; then
    yum install -y golang
  else
    echo "Could not find a package manager (apt/yum) to install Go." >&2
    echo "Please install Go manually and run this script again." >&2
    exit 1
  fi
  echo "Go installed successfully."
fi

echo "Compiling the 'better-tunnel' application..."
go build -ldflags="-s -w" -o better-tunnel tunnel.go

echo "Compilation successful."

INSTALL_PATH="/usr/local/bin"
echo "Installing 'better-tunnel' to $INSTALL_PATH..."
mv better-tunnel "$INSTALL_PATH/"
chmod +x "$INSTALL_PATH/better-tunnel"

echo ""
echo "✅ 'better-tunnel' has been installed successfully!"
echo ""
echo "You can now run it from anywhere using the 'better-tunnel' command."
echo "Example Usage:"
echo "  # On the server:"
echo "  better-tunnel server -listen :443 -public :8000 -path /your-secret-path"
echo ""
echo "  # On the client:"
echo "  better-tunnel client -server wss://your.server.ip/your-secret-path -local localhost:3000"
echo ""

exit 0
