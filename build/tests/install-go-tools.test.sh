#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
INSTALLER="${REPO_ROOT}/build/install-go-tools.sh"
DOCKERFILE="${REPO_ROOT}/build/Dockerfile.full"
DOCKERIGNORE="${REPO_ROOT}/.dockerignore"
TEST_DIR="$(mktemp -d)"
trap 'rm -rf "${TEST_DIR}"' EXIT

mkdir -p "${TEST_DIR}/bin"

cat >"${TEST_DIR}/bin/go" <<'EOF'
#!/bin/sh
set -eu
test "$1" = "install"
module="$2"
count_file="${TEST_STATE}/$(printf '%s' "$module" | tr '/@.' '____')"
count=0
if [ -f "$count_file" ]; then
  count=$(cat "$count_file")
fi
count=$((count + 1))
printf '%s' "$count" >"$count_file"
if [ "$module" = 'example.com/always-fails@v1.0.0' ]; then
  exit 1
fi
if [ "$count" -lt 3 ]; then
  exit 1
fi
printf '%s\n' "$module" >>"${TEST_STATE}/installed"
EOF
chmod +x "${TEST_DIR}/bin/go"

cat >"${TEST_DIR}/bin/sleep" <<'EOF'
#!/bin/sh
set -eu
printf '%s\n' "$1" >>"${TEST_STATE}/sleeps"
EOF
chmod +x "${TEST_DIR}/bin/sleep"

module='example.com/tool@v1.2.3'
TEST_STATE="${TEST_DIR}" \
PATH="${TEST_DIR}/bin:${PATH}" \
GO_INSTALL_RETRY_DELAY_SECONDS=1 \
"${INSTALLER}" "$module"

test "$(cat "${TEST_DIR}/example_com_tool_v1_2_3")" = "3"
test "$(cat "${TEST_DIR}/installed")" = "$module"
test "$(paste -sd, "${TEST_DIR}/sleeps")" = "1,2"

failing_module='example.com/always-fails@v1.0.0'
if TEST_STATE="${TEST_DIR}" \
  PATH="${TEST_DIR}/bin:${PATH}" \
  GO_INSTALL_MAX_ATTEMPTS=3 \
  GO_INSTALL_RETRY_DELAY_SECONDS=0 \
  "${INSTALLER}" "$failing_module"; then
  echo "installer unexpectedly succeeded after exhausting retries" >&2
  exit 1
fi
test "$(cat "${TEST_DIR}/example_com_always-fails_v1_0_0")" = "3"

grep -Fq 'build/install-go-tools.sh' "$DOCKERFILE"
grep -Fq 'google.golang.org/protobuf/cmd/protoc-gen-go@v1.35.1' "$DOCKERFILE"
grep -Fxq 'build/*' "$DOCKERIGNORE"
grep -Fxq '!build/install-go-tools.sh' "$DOCKERIGNORE"

echo "Go tool installer retry assertions passed"
