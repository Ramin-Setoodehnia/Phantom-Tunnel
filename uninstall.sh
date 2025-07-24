#!/bin/bash

if [ "$(id -u)" -ne 0 ]; then
  echo "This script must be run as root. Please use 'sudo bash $0'." >&2
  exit 1
fi

set -e

EXECUTABLE_NAMES=("phantom" "phantom-tunnel")
INSTALL_PATH="/usr/local/bin"
SERVICE_NAME="phantom.service"

WORKING_DIR="/etc/phantom"

LEGACY_WORKING_DIR="/root"
LEGACY_FILES=(
  "credentials.json"
  "config.json"
  "phantom.db"
  "license.key"
  "server.crt"
  "server.key"
)

print_info() { echo -e "\e[34m[INFO]\e[0m $1"; }
print_success() { echo -e "\e[32m[SUCCESS]\e[0m $1"; }
print_warn() { echo -e "\e[33m[WARN]\e[0m $1"; }

echo "----------------------------------------------"
echo "--- Uninstalling Phantom Tunnel Completely ---"
echo "----------------------------------------------"
echo "WARNING: This will remove the binary, all configuration files (from all versions),"
echo "databases, and the systemd service. This cannot be undone."

if [ "$1" != "--no-confirm" ]; then
    read -p "Are you sure you want to continue? [y/N]: " confirmation
    if [[ "$confirmation" != "y" && "$confirmation" != "Y" ]]; then
        echo "Uninstallation cancelled."
        exit 0
    fi
fi

print_info "Stopping and disabling the Phantom service..."
if systemctl list-units --full -all | grep -Fq "${SERVICE_NAME}"; then
    if systemctl is-active --quiet "$SERVICE_NAME"; then
        systemctl stop "$SERVICE_NAME"
        print_info "Service stopped."
    fi
    if systemctl is-enabled --quiet "$SERVICE_NAME"; then
        systemctl disable "$SERVICE_NAME"
        print_info "Service disabled."
    fi
else
    print_warn "Phantom service not found. Skipping."
fi

print_info "Killing any remaining 'phantom' processes..."
for name in "${EXECUTABLE_NAMES[@]}"; do
    pkill -f "$name" || true
done

SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}"
if [ -f "$SERVICE_FILE" ]; then
    print_info "Removing systemd service file..."
    rm -f "$SERVICE_FILE"
    systemctl daemon-reload
fi

for name in "${EXECUTABLE_NAMES[@]}"; do
    EXECUTABLE_PATH="${INSTALL_PATH}/${name}"
    if [ -f "$EXECUTABLE_PATH" ]; then
        print_info "Removing executable: ${EXECUTABLE_PATH}"
        rm -f "$EXECUTABLE_PATH"
    fi
done

if [ -d "$WORKING_DIR" ]; then
    print_info "Removing new data directory and all its contents: ${WORKING_DIR}"
    rm -rf "$WORKING_DIR"
fi

print_info "Searching for and removing legacy files from ${LEGACY_WORKING_DIR}..."
for file in "${LEGACY_FILES[@]}"; do
    if [ -f "${LEGACY_WORKING_DIR}/${file}" ]; then
        print_info "  - Removing legacy file: ${LEGACY_WORKING_DIR}/${file}"
        rm -f "${LEGACY_WORKING_DIR}/${file}"
    fi
done

print_info "Cleaning up temporary files..."
rm -f /tmp/phantom.pid
rm -f /tmp/phantom-panel.log
rm -f /tmp/phantom-tunnel.log

echo ""
print_success "Phantom Tunnel has been completely uninstalled from your system."
echo "If you installed the executable in a non-standard path, please remove it manually."

if [ "$1" == "--no-confirm" ]; then
    (sleep 1 && rm -f "$0") &
fi

exit 0
