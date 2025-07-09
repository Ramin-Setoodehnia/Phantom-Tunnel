#!/bin/bash

set -e

# --- پیکربندی ---
GITHUB_REPO="webwizards-team/phantom-tunnel"
INSTALL_PATH="/usr/local/bin"
EXECUTABLE_NAME="phantom-tunnel"

# --- توابع کمکی ---
print_info() { echo -e "\e[34m[INFO]\e[0m $1"; }
print_success() { echo -e "\e[32m[SUCCESS]\e[0m $1"; }
print_error() { echo -e "\e[31m[ERROR]\e[0m $1" >&2; }

# --- شروع اسکریپت ---
print_info "Starting Phantom Tunnel Installation..."

# ۱. بررسی دسترسی روت
if [ "$(id -u)" -ne 0 ]; then
  print_error "This script must be run as root. Please use 'sudo'."
  exit 1
fi

# ۲. نصب وابستگی‌های پایه (Git, Curl)
print_info "Checking for base dependencies (Git, curl)..."
if ! command -v apt-get &> /dev/null; then
  print_error "This script currently supports Debian-based systems (apt) only."
  exit 1
fi
apt-get update
apt-get install -y git curl

# ۳. اصلاح شده: نصب/آپدیت Go به آخرین نسخه پایدار
GO_VERSION="1.22.5" # می‌توانید این نسخه را در آینده آپدیت کنید
ARCH=$(dpkg --print-architecture)
GO_TARBALL="go${GO_VERSION}.linux-${ARCH}.tar.gz"
GO_URL="https://go.dev/dl/${GO_TARBALL}"
GO_INSTALL_DIR="/usr/local"

print_info "Checking Go version..."
if ! command -v go &> /dev/null || [[ $(go version | awk '{print $3}') != "go${GO_VERSION}" ]]; then
    print_info "Go v${GO_VERSION} not found. Installing/Updating..."
    
    # دانلود Go
    if ! curl -sSL -o "/tmp/${GO_TARBALL}" "$GO_URL"; then
        print_error "Failed to download Go tarball. Please check your network connection."
        exit 1
    fi
    
    # حذف نسخه قدیمی (اگر وجود داشته باشد) و نصب نسخه جدید
    rm -rf "${GO_INSTALL_DIR}/go"
    tar -C "$GO_INSTALL_DIR" -xzf "/tmp/${GO_TARBALL}"
    rm "/tmp/${GO_TARBALL}"
    
    # اطمینان از اینکه Go در PATH قرار دارد
    if ! grep -q "${GO_INSTALL_DIR}/go/bin" /etc/profile; then
        echo "export PATH=\$PATH:${GO_INSTALL_DIR}/go/bin" >> /etc/profile
    fi
    # برای استفاده در همین session
    export PATH=$PATH:${GO_INSTALL_DIR}/go/bin

    print_success "Go v${GO_VERSION} installed successfully."
else
    print_info "Correct Go version is already installed."
fi


# ۴. کامپایل برنامه اصلی
print_info "Downloading and compiling the Phantom Tunnel application..."
TMP_DIR=$(mktemp -d); trap 'rm -rf -- "$TMP_DIR"' EXIT; cd "$TMP_DIR"
SOURCE_FILE_URL="https://raw.githubusercontent.com/${GITHUB_REPO}/main/phantom.go"
curl -sSL -o "phantom.go" "$SOURCE_FILE_URL"

export GOPROXY=direct
# با نسخه جدید Go، این دستورات بسیار سریع‌تر و مطمئن‌تر اجرا می‌شوند
go mod init phantom-tunnel &>/dev/null || true
go get github.com/quic-go/quic-go@v0.45.1 # قفل کردن روی یک نسخه سازگار
go mod tidy

go build -ldflags="-s -w" -o "$EXECUTABLE_NAME" phantom.go
mv "$EXECUTABLE_NAME" "$INSTALL_PATH/"
chmod +x "$INSTALL_PATH/$EXECUTABLE_NAME"
print_success "Phantom Tunnel application compiled and installed."

# ۵. بهینه‌سازی سیستم‌عامل برای عملکرد بالا
print_info "Optimizing system for high concurrency..."
LIMITS_CONF="/etc/security/limits.conf"
if ! grep -q "phantom-tunnel-optimizations" "$LIMITS_CONF"; then
    print_info "Increasing file descriptor limits..."
    cat >> "$LIMITS_CONF" <<EOF

# BEGIN: phantom-tunnel-optimizations
* soft nofile 65536
* hard nofile 65536
# END: phantom-tunnel-optimizations
EOF
    print_success "System limits optimized."
else
    print_info "System limits already optimized. Skipping."
fi

# --- پایان ---
echo ""
print_success "Installation and optimization is complete!"
echo "--------------------------------------------------"
echo "IMPORTANT: To apply new system limits and PATH changes, please log out and log back in, or run 'source /etc/profile'."
echo ""
echo "After that, to run the tunnel, simply type this command anywhere:"
echo "  $EXECUTABLE_NAME"
echo "--------------------------------------------------"

exit 0
