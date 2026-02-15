#!/bin/sh
# install.sh — Install or upgrade bamgate.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/bamgate/bamgate/main/install.sh | sh
#
# Environment variables:
#   BAMGATE_VERSION   Install a specific version (e.g. "0.5.0"). Default: latest.
#   INSTALL_DIR       Install directory. Default: /usr/local/bin.
#
set -e

REPO="bamgate/bamgate"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
BINARY_NAME="bamgate"

main() {
    detect_platform
    detect_version
    download_and_install
    post_install
    print_next_steps
}

detect_platform() {
    OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
    ARCH="$(uname -m)"

    case "$OS" in
        linux)  OS="linux" ;;
        darwin) OS="darwin" ;;
        *)      fatal "Unsupported operating system: $OS" ;;
    esac

    case "$ARCH" in
        x86_64|amd64)   ARCH="amd64" ;;
        aarch64|arm64)  ARCH="arm64" ;;
        *)              fatal "Unsupported architecture: $ARCH" ;;
    esac

    log "Platform: ${OS}/${ARCH}"
}

detect_version() {
    if [ -n "$BAMGATE_VERSION" ]; then
        VERSION="$BAMGATE_VERSION"
        log "Version: v${VERSION} (pinned)"
        return
    fi

    log "Checking latest version..."
    VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
        | grep '"tag_name"' \
        | sed -E 's/.*"v?([^"]+)".*/\1/')"

    if [ -z "$VERSION" ]; then
        fatal "Could not determine latest version from GitHub"
    fi

    log "Version: v${VERSION} (latest)"
}

download_and_install() {
    TARBALL="bamgate_${VERSION}_${OS}_${ARCH}.tar.gz"
    URL="https://github.com/${REPO}/releases/download/v${VERSION}/${TARBALL}"

    log "Downloading ${TARBALL}..."

    TMPDIR="$(mktemp -d)"
    trap 'rm -rf "$TMPDIR"' EXIT

    curl -fsSL "$URL" -o "${TMPDIR}/${TARBALL}"

    log "Extracting..."
    tar -xzf "${TMPDIR}/${TARBALL}" -C "$TMPDIR"

    # Find the binary (may be at top level or in a subdirectory).
    BINARY_PATH="$(find "$TMPDIR" -name "$BINARY_NAME" -type f | head -1)"
    if [ -z "$BINARY_PATH" ]; then
        fatal "Binary not found in tarball"
    fi

    # Install — may need sudo.
    DEST="${INSTALL_DIR}/${BINARY_NAME}"
    if [ -w "$INSTALL_DIR" ]; then
        cp "$BINARY_PATH" "$DEST"
        chmod 755 "$DEST"
    else
        log "Installing to ${DEST} (requires sudo)..."
        sudo cp "$BINARY_PATH" "$DEST"
        sudo chmod 755 "$DEST"
    fi

    log "Installed ${BINARY_NAME} v${VERSION} to ${DEST}"
}

post_install() {
    DEST="${INSTALL_DIR}/${BINARY_NAME}"

    case "$OS" in
        darwin)
            # Remove macOS quarantine attribute.
            xattr -dr com.apple.quarantine "$DEST" 2>/dev/null || true
            ;;
    esac

    # If a systemd service is active, restart it.
    if [ "$OS" = "linux" ] && command -v systemctl >/dev/null 2>&1; then
        if systemctl is-active --quiet bamgate 2>/dev/null; then
            log "Restarting systemd service..."
            sudo systemctl restart bamgate
            log "Service restarted."
        fi
    fi

    # If a launchd service is loaded, restart it.
    if [ "$OS" = "darwin" ] && [ -f "/Library/LaunchDaemons/com.bamgate.bamgate.plist" ]; then
        if launchctl list com.bamgate.bamgate >/dev/null 2>&1; then
            log "Restarting launchd service..."
            sudo launchctl unload /Library/LaunchDaemons/com.bamgate.bamgate.plist
            sudo launchctl load -w /Library/LaunchDaemons/com.bamgate.bamgate.plist
            log "Service restarted."
        fi
    fi
}

print_next_steps() {
    echo ""
    echo "bamgate v${VERSION} installed successfully!"
    echo ""

    # Check if this is an upgrade (config already exists).
    if [ -f "/etc/bamgate/config.toml" ]; then
        echo "Upgrade complete."
    else
        echo "Next steps:"
        echo "  sudo bamgate setup    # Configure this device"
        echo "  sudo bamgate up       # Connect (foreground)"
        echo "  sudo bamgate up -d    # Connect (background service)"
        echo ""
        echo "For more info: https://github.com/${REPO}"
    fi
}

log() {
    echo "bamgate: $*" >&2
}

fatal() {
    echo "bamgate: ERROR: $*" >&2
    exit 1
}

main "$@"
