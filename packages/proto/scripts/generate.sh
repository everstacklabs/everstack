#!/usr/bin/env bash
set -euo pipefail

# Usage:
#   ./scripts/generate.sh <GITHUB_TOKEN>
# or set GITHUB_TOKEN env var and run without arg.

REPO_URL="https://github.com/everstacklabs/everstack.git"
PROTO_PATH="./proto/everstack"

TOKEN_INPUT="${1-}"
GITHUB_TOKEN="${TOKEN_INPUT:-${GITHUB_TOKEN:-}}"

if [[ -z "${GITHUB_TOKEN}" ]]; then
  echo "Error: GitHub token not provided. Pass as arg or set GITHUB_TOKEN env var." >&2
  exit 1
fi

# Ensure buf is available
if ! command -v buf >/dev/null 2>&1; then
  echo "Error: buf is not installed. Install @bufbuild/buf or run via pnpm: pnpm -w dlx @bufbuild/buf generate ..." >&2
  exit 1
fi

# Create a temporary askpass script for git to use the token
TMP_ASKPASS="$(mktemp)"
chmod 700 "$TMP_ASKPASS"
cat > "$TMP_ASKPASS" <<EOF
#!/usr/bin/env bash
echo "${GITHUB_TOKEN}"
EOF

# Use HTTPS with token via GIT_ASKPASS
export GIT_ASKPASS="$TMP_ASKPASS"
export GIT_TERMINAL_PROMPT=0

# Run buf generate directly against repo URL with path scoping
# Note: buf will clone internally; credentials are provided via git env.
BUF_CMD=(buf generate "${REPO_URL}" --path "${PROTO_PATH}")

echo "Running: ${BUF_CMD[*]}"
"${BUF_CMD[@]}"

# Cleanup
rm -f "$TMP_ASKPASS"

echo "Protobufs generated successfully."
