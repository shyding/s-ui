#!/usr/bin/env bash
# Build a deployable s-ui Linux amd64 binary on Ubuntu.
# Usage: ./scripts/build_linux.sh [output-directory]
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${1:-${ROOT_DIR}/dist/linux-amd64}"
TAGS="with_quic,with_grpc,with_utls,with_acme,with_gvisor"

fail() { echo "ERROR: $*" >&2; exit 1; }
command -v go >/dev/null 2>&1 || fail "Go 1.26.x is required"
GO_VERSION="$(go version)"
[[ "${GO_VERSION}" == *"go1.26"* ]] || fail "Go 1.26.x required, found: ${GO_VERSION}"

# Keep compiler temporary files and the Go build cache off small /tmp tmpfs.
export TMPDIR="${TMPDIR:-${HOME}/tmp}"
[[ "${TMPDIR}" == "/tmp" ]] && export TMPDIR="${HOME}/tmp"
export GOCACHE="${GOCACHE:-${HOME}/.cache/go-build}"
mkdir -p "${TMPDIR}" "${GOCACHE}" "${OUT_DIR}"

cd "${ROOT_DIR}"
if [[ "${SKIP_FRONTEND:-0}" != "1" ]]; then
  command -v npm >/dev/null 2>&1 || fail "npm is required (or set SKIP_FRONTEND=1)"
  npm --prefix frontend ci
  npm --prefix frontend run build
  mkdir -p web/html
  find web/html -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +
  cp -a frontend/dist/. web/html/
fi

export GOOS=linux
export GOARCH=amd64
export CGO_ENABLED=1
if ! command -v gcc >/dev/null 2>&1; then
  if command -v apt-get >/dev/null 2>&1 && command -v sudo >/dev/null 2>&1; then
    echo "gcc not found; installing build-essential"
    sudo apt-get update
    sudo apt-get install -y --no-install-recommends build-essential file gzip
  else
    fail "gcc is required for the SQLite build (install build-essential)"
  fi
fi
command -v file >/dev/null 2>&1 || fail "file is required to validate the ELF output"

BIN="${OUT_DIR}/sui"
echo "Building ${BIN} (${GO_VERSION})"
go build -trimpath -ldflags="-w -s" -tags "${TAGS}" -o "${BIN}" ./main.go
chmod 755 "${BIN}"
file "${BIN}" | grep -Eq 'ELF 64-bit.*x86-64' || fail "output is not a Linux amd64 ELF binary"
gzip -c -9 "${BIN}" > "${BIN}.gz"
sha256sum "${BIN}" "${BIN}.gz"
echo "Build complete: ${BIN}"
