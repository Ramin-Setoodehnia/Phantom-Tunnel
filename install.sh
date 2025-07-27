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

ARCH=$(uname -m)
case $ARCH in
    x86_64) ASSET_NAME="phantom-amd64" ;;
    aarch64 | arm64) ASSET_NAME="phantom-arm64" ;;
    *) print_error "Unsupported architecture: $ARCH. Only x86_64 and aarch64 are supported for pre-compiled binaries." ;;
esac

LATEST_TAG=$(curl -s "https://api.github.com/repos/${GITHUB_REPO}/releases/latest" | grep -oP '"tag_name": "\K[^"]+')
if [ -z "$LATEST_TAG" ]; then
    print_error "Failed to fetch the latest release tag from GitHub."
fi

DOWNLOAD_URL="https://github.com/${GITHUB_REPO}/releases/download/${LATEST_TAG}/${ASSET_NAME}"

print_info "Downloading the latest binary (${ASSET_NAME}) for ${ARCH} architecture..."
TMP_DIR=$(mktemp -d); trap 'rm -rf -- "$TMP_DIR"' EXIT; cd "$TMP_DIR"
if ! curl -sSLf -o "$EXECUTABLE_NAME" "$DOWNLOAD_URL"; then
    print_error "Download failed. Please check the repository releases and your internet connection."
fi
print_success "Binary downloaded successfully."

if systemctl is-active --quiet $SERVICE_NAME; then
    print_warning "An existing Phantom service is running. It will be stopped and updated."
    sudo systemctl stop $SERVICE_NAME
fi

print_info "Installing executable to ${INSTALL_PATH}..."
mkdir -p "$WORKING_DIR"
mv "$EXECUTABLE_NAME" "$INSTALL_PATH/"
chmod +x "$INSTALL_PATH/$EXECUTABLE_NAME"
print_success "Phantom application binary installed."

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
print_success "Systemd service file created at ${SERVICE_FILE}"

if [ ! -f "${WORKING_DIR}/config.db" ]; then
    print_info "Please provide the initial configuration for the panel."
    read -p "Enter the port for the web panel (e.g., 8080): " PANEL_PORT
    if ! [[ "$PANEL_PORT" =~ ^[0-9]+$ ]]; then
        print_error "Invalid port number provided. Please enter a valid number."
    fi

    read -p "Enter the admin username for the panel [default: admin]: " PANEL_USER
    PANEL_USER=${PANEL_USER:-admin}

    read -s -p "Enter the admin password for the panel [default: admin]: " PANEL_PASS
    echo
    PANEL_PASS=${PANEL_PASS:-admin}
    echo

    print_info "Running initial setup to configure the database..."
    sudo "${INSTALL_PATH}/${EXECUTABLE_NAME}" --setup-port="$PANEL_PORT" --setup-user="$PANEL_USER" --setup-pass="$PANEL_PASS"
else
    print_info "Existing configuration found, skipping setup questions."
fi


print_info "Enabling and starting the Phantom service..."
sudo systemctl enable --now ${SERVICE_NAME}
print_success "Service has been enabled and started."

echo ""
print_success "Installation complete!"
echo "------------------------------------------------------------"

sleep 2
if systemctl is-active --quiet $SERVICE_NAME; then
    print_success "Phantom Tunnel is now RUNNING!"
    echo "Panel Access: http://<YOUR_SERVER_IP>:<PANEL_PORT>"
    echo "(Use https:// if you later configure SSL)"
    if [ -n "$PANEL_USER" ]; then
      echo "Username: $PANEL_USER"
    fi
    echo "------------------------------------------------------------"
    echo "To manage the service, use:"
    echo "  sudo systemctl status ${SERVICE_NAME}"
    echo "  sudo systemctl stop ${SERVICE_NAME}"
    echo "  sudo systemctl restart ${SERVICE_NAME}"
    echo "To view live logs, use: journalctl -u ${SERVICE_NAME} -f"
else
    print_error "The service failed to start. Please check logs:"
    echo "journalctl -u ${SERVICE_NAME}"
fi
echo "------------------------------------------------------------"

exit 0
