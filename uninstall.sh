#!/bin/bash

# This script must be run as root
if [ "$(id -u)" -ne 0 ]; then
  echo "This script must be run as root. Please use 'sudo'." >&2
  exit 1
fi

# --- Configuration ---
EXECUTABLE_NAME="phantom"
INSTALL_PATH="/usr/local/bin"
SERVICE_NAME="phantom.service"
WORKING_DIR="/etc/phantom"
# ---------------------

print_info() { echo -e "\e[34m[INFO]\e[0m $1"; }
print_success() { echo -e "\e[32m[SUCCESS]\e[0m $1"; }

echo "----------------------------------------------"
echo "--- Uninstalling Phantom Tunnel Completely ---"
echo "----------------------------------------------"
echo "WARNING: This will remove the binary, all configuration files, and the systemd service."

# Use a command line argument to bypass confirmation for non-interactive execution
if [ "$1" != "--no-confirm" ]; then
    read -p "Are you sure you want to continue? [y/N]: " confirmation
    if [[ "$confirmation" != "y" && "$confirmation" != "Y" ]]; then
        echo "Uninstallation cancelled."
        exit 0
    fi
fi

# 1. Stop and disable the systemd service
print_info "Stopping and disabling the Phantom service..."
if systemctl list-units --full -all | grep -Fq "${SERVICE_NAME}"; then
    if systemctl is-active --quiet "$SERVICE_NAME"; then
        systemctl stop "$SERVICE_NAME"
    fi
    if systemctl is-enabled --quiet "$SERVICE_NAME"; then
        systemctl disable "$SERVICE_NAME"
    fi
fi

# 2. Kill any remaining processes to be safe
print_info "Killing any remaining 'phantom' processes..."
pkill -f "$EXECUTABLE_NAME" || true

# 3. Remove systemd service file
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}"
if [ -f "$SERVICE_FILE" ]; then
    print_info "Removing systemd service file..."
    rm -f "$SERVICE_FILE"
    systemctl daemon-reload
fi

# 4. Remove application binary
EXECUTABLE_PATH="${INSTALL_PATH}/${EXECUTABLE_NAME}"
if [ -f "$EXECUTABLE_PATH" ]; then
    print_info "Removing executable: ${EXECUTABLE_PATH}"
    rm -f "$EXECUTABLE_PATH"
fi

# 5. Remove working directory and all its contents
if [ -d "$WORKING_DIR" ]; then
    print_info "Removing working directory and all configurations: ${WORKING_DIR}"
    rm -rf "$WORKING_DIR"
fi

# 6. Remove temporary log and PID files
print_info "Cleaning up temporary files..."
rm -f /tmp/phantom.pid
rm -f /tmp/phantom-panel.log
rm -f /tmp/phantom-tunnel.log

echo ""
print_success "Phantom Tunnel has been completely uninstalled from your system."

# Self-destruct if called from Go application
if [ "$1" == "--no-confirm" ]; then
    rm -f "$0"
fi

exit 0
