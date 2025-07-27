#!/bin/bash

set -e

GITHUB_REPO="webwizards-team/Phantom-Tunnel"
EXECUTABLE_NAME="phantom"
INSTALL_PATH="/usr/local/bin"
SERVICE_NAME="phantom.service"
WORKING_DIR="/etc/phantom"

print_info() { echo -e "\e[34m[INFO]\e[0m $1"; }
print_success() { echo -e "\e[32m[SUCCESS]\e[0m $1"; }
print_error() { echo -e "\e[31m[ERROR]\e[0m $1" >&2; exit 1; }
print_warning() { echo -e "\e[33m⚠️ WARNING: $1\033[0m"; }

clear
print_info "Starting Phantom Tunnel Installation..."

if [ "$(id -u)" -ne 0 ]; then
  print_error "This script must be run as root. Please use 'sudo'."
fi

print_info "Checking for dependencies (curl, grep)..."
if command -v apt-get &> /dev/null; then
    apt-get update -y > /dev/null && apt-get install -y -qq curl grep > /dev/null
elif command -v yum &> /dev/null; then
    yum install -y curl grep > /dev/null
else
    print_warning "Unsupported package manager. Assuming 'curl' and 'grep' are installed."
fi
print_success "Dependencies are satisfied."

print_info "Detecting architecture and latest version..."
ARCH=$(uname -m)
case $ARCH in
    x86_64) ASSET_NAME="phantom-amd64" ;;
    aarch64 | arm64) ASSET_NAME="phantom-arm64" ;;
    *) print_error "Unsupported architecture: $ARCH. Only x86_64 and aarch64 are supported." ;;
esac

LATEST_TAG=$(curl -s "https://api.github.com/repos/${GITHUB_REPO}/releases/latest" | grep -oP '"tag_name": "\K[^"]+')
if [ -z "$LATEST_TAG" ]; then
    print_error "Failed to fetch the latest release tag from GitHub."
fi
print_success "Latest version is ${LATEST_TAG}. Architecture: ${ARCH}."

DOWNLOAD_URL="https://github.com/${GITHUB_REPO}/releases/download/${LATEST_TAG}/${ASSET_NAME}"

print_info "Downloading Phantom Tunnel binary (${ASSET_NAME})..."
if ! curl -sSLf -o "${INSTALL_PATH}/${EXECUTABLE_NAME}" "$DOWNLOAD_URL"; then
    print_error "Download failed. Please check the repository releases and your internet connection."
fi
chmod +x "${INSTALL_PATH}/${EXECUTABLE_NAME}"
print_success "Binary downloaded and installed successfully."

if systemctl is-active --quiet $SERVICE_NAME; then
    print_info "An existing Phantom service is running. Stopping it for update."
    sudo systemctl stop $SERVICE_NAME
fi

mkdir -p "$WORKING_DIR"

print_info "Configuring systemd service..."
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}"
cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=Phantom Tunnel Panel Service
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=${INSTALL_PATH}/${EXECUTABLE_NAME} --start-panel
WorkingDirectory=${WORKING_DIR}
Restart=always
RestartSec=5
LimitNOFILE=65536
User=root
Group=root

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
print_success "Systemd service file created/updated."

if [ ! -f "${WORKING_DIR}/config.db" ]; then
    print_info "No previous configuration found. Please provide initial setup details."
    read -p "Enter the port for the web panel (e.g., 8080): " PANEL_PORT
    if ! [[ "$PANEL_PORT" =~ ^[0-9]+$ ]]; then
        print_error "Invalid port number provided."
    fi

    read -p "Enter the admin username for the panel [default: admin]: " PANEL_USER
    PANEL_USER=${PANEL_USER:-admin}

    read -s -p "Enter the admin password for the panel [default: admin]: " PANEL_PASS
    echo
    PANEL_PASS=${PANEL_PASS:-admin}
    echo

    print_info "Running initial setup to configure the database..."
    "${INSTALL_PATH}/${EXECUTABLE_NAME}" --setup-port="$PANEL_PORT" --setup-user="$PANEL_USER" --setup-pass="$PANEL_PASS"
else
    print_info "Existing configuration found. Skipping initial setup."
fi

print_info "Enabling and starting the Phantom service..."
sudo systemctl enable --now ${SERVICE_NAME}

echo ""
print_success "Installation/Update complete!"
echo "------------------------------------------------------------"

sleep 2
if systemctl is-active --quiet $SERVICE_NAME; then
    PANEL_PORT=$(sudo "${INSTALL_PATH}/${EXECUTABLE_NAME}" --show-port)
    PANEL_USER=$(sudo "${INSTALL_PATH}/${EXECUTABLE_NAME}" --show-user)
    print_success "Phantom Tunnel is now RUNNING!"
    echo "Panel Access: http://<YOUR_SERVER_IP>:$PANEL_PORT"
    echo "(Use https:// if you later configure SSL)"
    echo "Username: $PANEL_USER"
    echo "------------------------------------------------------------"
    echo "To manage the service, use:"
    echo "  sudo systemctl status ${SERVICE_NAME}"
    echo "  sudo systemctl restart ${SERVICE_NAME}"
    echo "To view live logs, use: journalctl -u ${SERVICE_NAME} -f"
else
    print_error "The service failed to start. Please check logs:"
    echo "journalctl -u ${SERVICE_NAME}"
fi
echo "------------------------------------------------------------"

exit 0
