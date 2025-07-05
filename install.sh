#!/bin/bash

# install.sh: A smart script to download, compile, and install better-tunnel.

# Exit immediately if a command exits with a non-zero status.
set -e

# --- Configuration ---
# !!! IMPORTANT: Replace this with your own GitHub repository details !!!
GITHUB_REPO="webwizards-team/Phantom-Tunnel"
SOURCE_FILE_URL="https://raw.githubusercontent.com/${GITHUB_REPO}/main/tunnel.go"

# --- Check for Root Privileges ---
if [ "$(id -u)" -ne 0 ]; then
  echo "This script must be run as root. Please use sudo." >&2
  exit 1
fi

# --- Install Dependencies (Go & Git) ---
echo "Checking for dependencies..."
# Check for Go
if ! command -v go &> /dev/null; then
  echo "Go compiler not found. Attempting to install..."
  if command -v apt-get &> /dev/null; then
    apt-get update && apt-get install -y golang-go
  elif command -v yum &> /dev/null; then
    yum install -y golang
  else
    echo "Could not find a package manager (apt/yum) to install Go. Please install it manually." >&2
    exit 1
  fi
fi
# Check for a download tool
if ! command -v curl &> /dev/null && ! command -v wget &> /dev/null; then
    echo "Error: This script requires either curl or wget to be installed." >&2
    exit 1
fi

# --- Download the Source Code ---
echo "Downloading the latest source code from GitHub..."
TMP_DIR=$(mktemp -d)
trap 'rm -rf -- "$TMP_DIR"' EXIT # Clean up the temp directory on exit

if command -v curl &> /dev/null; then
    curl -sSL -o "$TMP_DIR/tunnel.go" "$SOURCE_FILE_URL"
else
    wget -q -O "$TMP_DIR/tunnel.go" "$SOURCE_FILE_URL"
fi
cd "$TMP_DIR"

# --- Compile the Application ---
echo "Compiling the 'better-tunnel' application..."
# The -ldflags="-s -w" part makes the binary smaller by stripping debug symbols.
go build -ldflags="-s -w" -o better-tunnel tunnel.go
echo "Compilation successful."

# --- Install the Binary ---
INSTALL_PATH="/usr/local/bin"
echo "Installing 'better-tunnel' to $INSTALL_PATH..."
mv better-tunnel "$INSTALL_PATH/"
chmod +x "$INSTALL_PATH/better-tunnel"

# --- Final Message ---
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
