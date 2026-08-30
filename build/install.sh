#!/usr/bin/env bash
# Install the Everstack CLI (evs).
#
# Usage:
#   curl -fsSL https://get.everstack.ai/install.sh | bash
#   curl -fsSL https://get.everstack.ai/install.sh | bash -s -- --version v0.2.1
#   curl -fsSL https://get.everstack.ai/install.sh | bash -s -- --bin-dir ~/.local/bin
#
# Flags:
#   --version <vX.Y.Z|latest>    Version to install (default: latest)
#   --bin-dir <path>             Install directory (default: /usr/local/bin)
#   --no-verify                  Skip SHA256 checksum verification
#   --check-version <vX.Y.Z>     Validate release eligibility and exit
#   -h, --help                   Show this message

set -euo pipefail

VERSION=""
BIN_DIR="/usr/local/bin"
VERIFY=1
CHECK_VERSION_ONLY=0
RELEASES_REPO="everstacklabs/everstack"
MIN_SAFE_VERSION="v0.1.25"
MIN_SAFE_MAJOR=0
MIN_SAFE_MINOR=1
MIN_SAFE_PATCH=25

validate_release_version() {
  local candidate="$1"

  # Only stable semantic versions are eligible for the installer. This also
  # prevents unrelated release tags from becoming download URL components.
  if [[ ! "$candidate" =~ ^v?([0-9]+)\.([0-9]+)\.([0-9]+)(\+[0-9A-Za-z.-]+)?$ ]]; then
    echo "error: release version must be a stable semantic version (vX.Y.Z)" >&2
    return 1
  fi

  local major=$((10#${BASH_REMATCH[1]}))
  local minor=$((10#${BASH_REMATCH[2]}))
  local patch=$((10#${BASH_REMATCH[3]}))

  if (( major < MIN_SAFE_MAJOR ||
        (major == MIN_SAFE_MAJOR && minor < MIN_SAFE_MINOR) ||
        (major == MIN_SAFE_MAJOR && minor == MIN_SAFE_MINOR && patch < MIN_SAFE_PATCH) )); then
    echo "error: $candidate predates security-hardened Community Edition binary releases (minimum $MIN_SAFE_VERSION)" >&2
    echo "build Community Edition from source or choose $MIN_SAFE_VERSION or newer" >&2
    return 1
  fi
}

# Parse flags
while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      [[ $# -ge 2 ]] || { echo "error: --version requires a value" >&2; exit 2; }
      VERSION="$2"; shift 2 ;;
    --bin-dir)
      [[ $# -ge 2 ]] || { echo "error: --bin-dir requires a value" >&2; exit 2; }
      BIN_DIR="$2"; shift 2 ;;
    --no-verify) VERIFY=0; shift ;;
    --check-version)
      [[ $# -ge 2 ]] || { echo "error: --check-version requires a value" >&2; exit 2; }
      VERSION="$2"; CHECK_VERSION_ONLY=1; shift 2 ;;
    -h|--help)
      sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'
      exit 0 ;;
    *) echo "Unknown flag: $1" >&2; exit 1 ;;
  esac
done

# Resolve latest version if not pinned
if [[ -z "$VERSION" || "$VERSION" == "latest" ]]; then
  echo "Fetching latest version..."
  VERSION=$(
    curl -fsSL "https://api.github.com/repos/${RELEASES_REPO}/releases/latest" \
      | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'
  )
  if [[ -z "$VERSION" || "$VERSION" == "null" ]]; then
    echo "error: could not fetch latest version from GitHub" >&2
    exit 1
  fi
fi

validate_release_version "$VERSION"
if [[ "$CHECK_VERSION_ONLY" -eq 1 ]]; then
  echo "$VERSION is eligible for installation (minimum $MIN_SAFE_VERSION)."
  exit 0
fi

# Detect OS and architecture
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$os" in
  linux)   ;;
  darwin)  ;;
  *) echo "error: unsupported OS: $os" >&2; exit 1 ;;
esac
case "$arch" in
  x86_64|amd64)  arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) echo "error: unsupported architecture: $arch" >&2; exit 1 ;;
esac

ARCHIVE="everstack-${os}-${arch}.gz"
BASE_URL="https://github.com/${RELEASES_REPO}/releases/download/${VERSION}"

echo "Installing evs ${VERSION} (${os}/${arch}) to ${BIN_DIR}"

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

# Download binary archive
echo "Downloading ${ARCHIVE}..."
if command -v curl >/dev/null 2>&1; then
  curl -fsSL --retry 3 -o "${tmpdir}/${ARCHIVE}" "${BASE_URL}/${ARCHIVE}"
else
  wget -qO "${tmpdir}/${ARCHIVE}" "${BASE_URL}/${ARCHIVE}"
fi

# Verify checksum. Verification is fail-closed unless the caller explicitly
# opts out with --no-verify.
if [[ "$VERIFY" -eq 1 ]]; then
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --retry 3 -o "${tmpdir}/SHA256SUMS.txt" "${BASE_URL}/SHA256SUMS.txt"
  else
    wget -qO "${tmpdir}/SHA256SUMS.txt" "${BASE_URL}/SHA256SUMS.txt"
  fi

  checksum_line=$(grep -E "[ *]${ARCHIVE}$" "${tmpdir}/SHA256SUMS.txt" || true)
  if [[ -z "$checksum_line" ]]; then
    echo "error: ${ARCHIVE} is missing from SHA256SUMS.txt" >&2
    exit 1
  fi

  expected=$(printf '%s\n' "$checksum_line" | awk '{print $1}')
  if command -v shasum >/dev/null 2>&1; then
    actual=$(shasum -a 256 "${tmpdir}/${ARCHIVE}" | awk '{print $1}')
  elif command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "${tmpdir}/${ARCHIVE}" | awk '{print $1}')
  else
    echo "error: checksum verification requires shasum or sha256sum" >&2
    exit 1
  fi

  if [[ "$actual" != "$expected" ]]; then
    echo "error: checksum mismatch for ${ARCHIVE}" >&2
    exit 1
  fi
  echo "Checksum verified."
fi

# Extract and install
gunzip -c "${tmpdir}/${ARCHIVE}" > "${tmpdir}/evs"
chmod +x "${tmpdir}/evs"

# Create the target dir as the current user first. A not-yet-existing but
# creatable dir (e.g. ~/.local/bin) would otherwise fail the -w test below
# and needlessly escalate to sudo.
mkdir -p "$BIN_DIR" 2>/dev/null || true

# Fall back to sudo only if the dir still isn't writable (e.g. the default
# /usr/local/bin, which is root-owned on macOS).
SUDO=""
if [[ ! -w "$BIN_DIR" ]]; then
  if command -v sudo >/dev/null 2>&1; then
    SUDO="sudo"
    echo "Requesting sudo to install to ${BIN_DIR}..."
  else
    echo "error: ${BIN_DIR} is not writable and sudo is not available" >&2
    echo "Re-run with --bin-dir pointing to a writable directory, e.g.:" >&2
    echo "  curl -fsSL https://get.everstack.ai/install.sh | bash -s -- --bin-dir ~/.local/bin" >&2
    exit 1
  fi
fi

$SUDO mkdir -p "$BIN_DIR"
$SUDO install -m 0755 "${tmpdir}/evs" "${BIN_DIR}/evs"

# Keep 'everstack' as a backward-compatible alias
if [[ ! -e "${BIN_DIR}/everstack" ]]; then
  $SUDO ln -sf "${BIN_DIR}/evs" "${BIN_DIR}/everstack" 2>/dev/null || true
fi

# Confirm installed version
echo ""
if command -v "${BIN_DIR}/evs" >/dev/null 2>&1; then
  "${BIN_DIR}/evs" --version 2>/dev/null || echo "Installed ${VERSION}"
else
  echo "Installed ${VERSION} to ${BIN_DIR}/evs"
  echo "Make sure ${BIN_DIR} is in your PATH."
fi

echo ""
echo "Get started:"
echo "  evs login"
echo "  evs --help"
