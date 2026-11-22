#!/usr/bin/env bash
# ManToManAgent installer — downloads the latest binary from GitHub Releases.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/nextlevelbuilder/goclaw/main/scripts/install.sh | bash
#   curl -fsSL ... | bash -s -- --version v1.30.0
#   curl -fsSL ... | bash -s -- --dir /opt/mantomanagent
#
# Supported: Linux (amd64/arm64), macOS (amd64/arm64)

set -euo pipefail

REPO="nextlevelbuilder/goclaw"
INSTALL_DIR="${MANTOMANAGENT_INSTALL_DIR:-/usr/local/bin}"
MIGRATIONS_DIR="/usr/local/share/mantomanagent/migrations"
VERSION=""

# ── Parse args ──
while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    --dir)     INSTALL_DIR="$2"; shift 2 ;;
    --help|-h)
      echo "Usage: install.sh [--version v1.x.x] [--dir /path]"
      exit 0
      ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

# ── Detect OS/arch ──
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

case "$OS" in
  linux|darwin) ;;
  *) echo "Unsupported OS: $OS"; exit 1 ;;
esac

# ── Resolve version ──
if [ -z "$VERSION" ]; then
  echo "Fetching latest release..."
  VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')"
fi
echo "Installing ManToManAgent ${VERSION} (${OS}/${ARCH})..."

# ── Download ──
ASSET="mantomanagent-${VERSION#v}-${OS}-${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "Downloading ${URL}..."
curl -fsSL -o "${TMP}/${ASSET}" "$URL"

# ── Extract & install ──
tar -xzf "${TMP}/${ASSET}" -C "$TMP"

# Check write permission, use sudo if needed
if [ -w "$INSTALL_DIR" ]; then
  cp "${TMP}/mantomanagent" "${INSTALL_DIR}/mantomanagent"
  chmod +x "${INSTALL_DIR}/mantomanagent"
  mkdir -p "${MIGRATIONS_DIR}"
  cp -r "${TMP}/migrations/"* "${MIGRATIONS_DIR}/"
else
  echo "Installing to ${INSTALL_DIR} (requires sudo)..."
  sudo cp "${TMP}/mantomanagent" "${INSTALL_DIR}/mantomanagent"
  sudo chmod +x "${INSTALL_DIR}/mantomanagent"
  sudo mkdir -p "${MIGRATIONS_DIR}"
  sudo cp -r "${TMP}/migrations/"* "${MIGRATIONS_DIR}/"
fi

echo ""
echo "ManToManAgent ${VERSION} installed to ${INSTALL_DIR}/mantomanagent"
echo "Migrations installed to ${MIGRATIONS_DIR}"
echo ""
echo "The binary includes an embedded web dashboard — no separate nginx needed."
echo ""
echo "Next steps:"
echo "  1. Set up PostgreSQL (pgvector):"
echo "     docker run -d --name mantomanagent-pg -p 5432:5432 -e POSTGRES_PASSWORD=mantomanagent pgvector/pgvector:pg18"
echo ""
echo "  2. Set environment variables:"
echo "     export MANTOMANAGENT_POSTGRES_DSN='postgres://postgres:mantomanagent@localhost:5432/postgres?sslmode=disable'"
echo "     export MANTOMANAGENT_MIGRATIONS_DIR='${MIGRATIONS_DIR}'"
echo ""
echo "  3. Start the onboard wizard (runs migrations automatically):"
echo "     mantomanagent onboard"
echo ""
echo "  4. Start the gateway:"
echo "     source .env.local && mantomanagent"
echo ""
echo "  Web dashboard: http://localhost:18790"
echo "  Health check:  curl http://localhost:18790/health"
echo ""
echo "  To update later: mantomanagent update --apply"
