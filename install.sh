#!/bin/bash

set -e

# --- Configuration ---
GITHUB_REPO="webwizards-team/Phantom-Tunnel"
ASSET_NAME="phantom"
EXECUTABLE_NAME="phantom"
INSTALL_PATH="/usr/local/bin"
SERVICE_NAME="phantom.service"
WORKING_DIR="/etc/phantom"
# ---------------------

print_info() { echo -e "\e[34m[INFO]\e[0m $1"; }
print_success() { echo -e "\e[32m[SUCCESS]\e[0m $1"; }
print_error() { echo -e "\e[31m[ERROR]\e[0m $1" >&2; exit 1; }

print_info "Starting Phantom Tunnel Installation..."

# --- Root Check ---
if [ "$(id -u)" -ne 0 ]; then
  print_error "This script must be run as root. Please use 'sudo'."
fi

# --- Dependency Check ---
print_info "Checking for curl..."
if command -v apt-get &> /dev/null; then
    apt-get update -y > /dev/null && apt-get install -y curl
elif command -v yum &> /dev/null; then
    yum install -y curl
else
    print_error "Unsupported package manager. Please install 'curl' manually."
fi

# --- Download ---
DOWNLOAD_URL="https://github.com/${GITHUB_REPO}/releases/download/3.0.1/${ASSET_NAME}"

print_info "Downloading the latest version from: ${DOWNLOAD_URL}"
TMP_DIR=$(mktemp -d); trap 'rm -rf -- "$TMP_DIR"' EXIT; cd "$TMP_DIR"
if ! curl -sSLf -o "$EXECUTABLE_NAME" "$DOWNLOAD_URL"; then
    print_error "Download failed. Please check the URL and ensure the asset exists in the GitHub release."
fi

# --- Installation ---
print_info "Installing executable to ${INSTALL_PATH}..."
mkdir -p "$WORKING_DIR"
mv "$EXECUTABLE_NAME" "$INSTALL_PATH/"
chmod +x "$INSTALL_PATH/$EXECUTABLE_NAME"
print_success "Phantom application binary installed."

# --- Systemd Service Setup ---
print_info "Configuring systemd service..."
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}"
cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=Phantom Tunnel Panel Service
After=network-online.target
Wants=network-online.target

[Service]
# Using ExecStartPre to wait for network is not a perfect solution but works in many cases.
ExecStartPre=/bin/sleep 10
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

# --- Final Instructions ---
echo ""
print_success "Installation complete!"
echo "--------------------------------------------------"
echo -e "\e[31m[IMPORTANT]\e[0m You must now perform the initial setup."
echo ""
print_info "1. Navigate to the working directory:"
echo "   cd ${WORKING_DIR}"
echo ""
print_info "2. Run the interactive setup to create credentials and set the panel port:"
echo "   sudo ${INSTALL_PATH}/${EXECUTABLE_NAME}"
echo ""
print_info "3. After setting the port, you can enable and start the service to run in the background:"
echo "   sudo systemctl enable --now ${SERVICE_NAME}"
echo ""
print_info "After that, you can manage the service with:"
echo "  sudo systemctl start ${SERVICE_NAME}"
echo "  sudo systemctl stop ${SERVICE_NAME}"
echo "  sudo systemctl status ${SERVICE_NAME}"
echo "--------------------------------------------------"

exit 0
