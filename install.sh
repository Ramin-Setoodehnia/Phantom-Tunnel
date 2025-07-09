#!/bin/bash

set -e

# --- پیکربندی ---
GITHUB_REPO="webwizards-team/phantom-tunnel"
INSTALL_PATH="/usr/local/bin"
EXECUTABLE_NAME="phantom-tunnel"
GO_VERSION="1.22.5" # می‌توانید این نسخه را در آینده آپدیت کنید

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

# ۲. نصب وابستگی‌های پایه
print_info "Checking for base dependencies (Git, curl)..."
if ! command -v apt-get &> /dev/null; then
  print_error "This script currently supports Debian-based systems (apt) only."
  exit 1
fi
apt-get update
apt-get install -y git curl

# ۳. اصلاح شده: حذف کامل نسخه قدیمی Go و نصب آخرین نسخه پایدار
GO_INSTALL_DIR="/usr/local"
GO_BIN_PATH="${GO_INSTALL_DIR}/go/bin/go"

# حذف نسخه قدیمی که توسط apt نصب شده
print_info "Checking for and removing old Go versions from APT..."
if dpkg -s golang-go &> /dev/null; then
    print_info "Found old 'golang-go' package. Purging it..."
    apt-get purge -y golang-go
    apt-get autoremove -y
    print_success "Old Go package removed."
fi

# بررسی اینکه آیا نسخه صحیح Go قبلاً نصب شده است یا خیر
INSTALL_GO=true
if command -v go &> /dev/null; then
    # اگر دستور go وجود دارد، نسخه آن را بررسی کن
    if [[ $(go version) == *"go${GO_VERSION}"* ]]; then
        print_info "Correct Go version (v${GO_VERSION}) is already installed."
        INSTALL_GO=false
    else
        print_info "An incorrect version of Go was found. It will be replaced."
        # حذف هر نسخه قدیمی دیگری که ممکن است در /usr/local/go باشد
        rm -rf "${GO_INSTALL_DIR}/go"
    fi
else
    print_info "Go not found. Proceeding with installation."
fi

if [ "$INSTALL_GO" = true ]; then
    ARCH=$(dpkg --print-architecture)
    GO_TARBALL="go${GO_VERSION}.linux-${ARCH}.tar.gz"
    GO_URL="https://go.dev/dl/${GO_TARBALL}"
    
    print_info "Downloading Go v${GO_VERSION}..."
    if ! curl -sSL -o "/tmp/${GO_TARBALL}" "$GO_URL"; then
        print_error "Failed to download Go tarball. Please check your network connection."
        exit 1
    fi
    
    print_info "Installing Go v${GO_VERSION} to ${GO_INSTALL_DIR}..."
    tar -C "$GO_INSTALL_DIR" -xzf "/tmp/${GO_TARBALL}"
    rm "/tmp/${GO_TARBALL}"
    
    # اطمینان از اینکه Go در PATH قرار دارد (این کار باعث می‌شود نیاز به لاگ اوت نباشد)
    if ! grep -q "${GO_INSTALL_DIR}/go/bin" /etc/profile; then
        echo "export PATH=\$PATH:${GO_INSTALL_DIR}/go/bin" >> /etc/profile
    fi
    print_success "Go v${GO_VERSION} installed successfully."
fi

# برای استفاده در همین session، مسیر صحیح را به PATH اضافه می‌کنیم
export PATH=$PATH:${GO_INSTALL_DIR}/go/bin

# ۴. کامپایل برنامه اصلی
print_info "Downloading and compiling the Phantom Tunnel application..."
TMP_DIR=$(mktemp -d); trap 'rm -rf -- "$TMP_DIR"' EXIT; cd "$TMP_DIR"
SOURCE_FILE_URL="https://raw.githubusercontent.com/${GITHUB_REPO}/main/phantom.go"
curl -sSL -o "phantom.go" "$SOURCE_FILE_URL"

export GOPROXY=direct
# با نسخه جدید Go، این دستورات باید بدون مشکل اجرا شوند
go mod init phantom-tunnel &>/dev/null || true
# قفل کردن روی یک نسخه سازگار برای اطمینان بیشتر
go get github.com/quic-go/quic-go@v0.45.1
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
echo "IMPORTANT: To apply new system limits, please log out and log back in, or run 'source /etc/profile'."
echo ""
echo "After that, to run the tunnel, simply type this command anywhere:"
echo "  $EXECUTABLE_NAME"
echo "--------------------------------------------------"

exit 0
