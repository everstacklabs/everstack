#!/usr/bin/env bash
# Build the pinned OpenAI Codex Firecracker guest and convert it to the exact
# filename consumed by the Firecracker backend. Extraction and conversion stay
# inside Linux Docker volumes so numeric guest ownership is preserved on macOS.
#
# Usage:
#   prepare-openai-codex-firecracker-rootfs.sh [OUTPUT_DIR]
#       Build into OUTPUT_DIR (default dist/firecracker-rootfs). Run from the
#       repository root.
#   prepare-openai-codex-firecracker-rootfs.sh install ARTIFACT_DIR ROOTFS_DIR
#       On the Firecracker node, install ARTIFACT_DIR/<alias>.ext4 (checked
#       against ARTIFACT_DIR/<alias>.ext4.sha256) into ROOTFS_DIR, the
#       FC_ROOTFS_DIR of ONE environment, for example
#       /var/lib/everstack/vmstore/dev/rootfs. Needs no Docker or checkout.
#
# Install keeps at most one previous build, as <alias>.ext4.previous, and only
# while the new build is being health checked. On success it deletes that copy
# and any legacy timestamped <alias>.ext4*.backup-* files; on failure it moves
# the previous build back into place. Nothing timestamped is ever written.
#
# Install environment:
#   EVS_ROOTFS_ENV_CAP_MB          per-env soft cap on ROOTFS_DIR (default
#                                  10240, half of the shared 20 GiB store).
#                                  Refuses (exit 3) when the dir plus the new
#                                  image would exceed it.
#   EVS_ROOTFS_MIN_FREE_AFTER_MB   free space the store must keep after the
#                                  install (default 4096, 2x the fcagent create
#                                  guard). Refuses (exit 3) below it.
#   ROOTFS_HEALTH_CMD              extra health check run after the swap, for
#                                  example a sandbox create smoke test. Non-zero
#                                  exit rolls the install back.
#   ROOTFS_SKIP_DEBUGFS=1          skip the in-image version marker check (tests).

set -euo pipefail

ROOTFS_ALIAS="openai-codex-0.154.0-r3"

file_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d ' ' -f 1
  else
    shasum -a 256 "$1" | cut -d ' ' -f 1
  fi
}

install_rootfs() {
  if [ "$#" -ne 2 ]; then
    echo "usage: $0 install ARTIFACT_DIR ROOTFS_DIR" >&2
    exit 2
  fi
  local artifact="$1/$ROOTFS_ALIAS.ext4"
  local checksum="$1/$ROOTFS_ALIAS.ext4.sha256"
  local rootfs_dir="$2"
  local current="$rootfs_dir/$ROOTFS_ALIAS.ext4"
  local previous="$current.previous"
  # Global, not local: the EXIT trap runs after this function returns.
  INSTALL_STAGED="$rootfs_dir/.$ROOTFS_ALIAS.ext4.install.$$"
  local staged="$INSTALL_STAGED"
  local cap_mb="${EVS_ROOTFS_ENV_CAP_MB:-10240}"
  local min_free_mb="${EVS_ROOTFS_MIN_FREE_AFTER_MB:-4096}"

  [ -s "$artifact" ] || { echo "missing artifact $artifact" >&2; exit 2; }
  [ -s "$checksum" ] || { echo "missing checksum $checksum" >&2; exit 2; }
  [ -d "$rootfs_dir" ] || { echo "missing rootfs dir $rootfs_dir" >&2; exit 2; }

  local expected
  expected="$(cut -d ' ' -f 1 "$checksum")"
  [[ "$expected" =~ ^[0-9a-f]{64}$ ]] || { echo "malformed checksum file $checksum" >&2; exit 2; }
  if [ "$(file_sha256 "$artifact")" != "$expected" ]; then
    echo "refusing to install: $artifact does not match $checksum" >&2
    exit 1
  fi

  # Per-env soft cap. du counts allocated blocks, so a reflinked extent
  # shared with the other env counts in both: a conservative reading.
  local used_kb new_kb avail_kb
  used_kb="$(du -sk "$rootfs_dir" | cut -f 1)"
  new_kb="$(du -sk "$artifact" | cut -f 1)"
  if [ $((used_kb + new_kb)) -gt $((cap_mb * 1024)) ]; then
    echo "refusing to install: $rootfs_dir uses $((used_kb / 1024)) MiB, +$((new_kb / 1024)) MiB would exceed EVS_ROOTFS_ENV_CAP_MB=$cap_mb." >&2
    echo "Free space first: the fcagent rootfs GC lists eligible files (rootfs_gc log lines)." >&2
    exit 3
  fi
  avail_kb="$(df -Pk "$rootfs_dir" | awk 'NR == 2 { print $4 }')"
  if [ $((avail_kb - new_kb)) -lt $((min_free_mb * 1024)) ]; then
    echo "refusing to install: only $((avail_kb / 1024)) MiB free on the store; the install would leave less than EVS_ROOTFS_MIN_FREE_AFTER_MB=$min_free_mb." >&2
    exit 3
  fi

  # Stage on the same filesystem so the swap below is an atomic rename.
  trap 'rm -f "$INSTALL_STAGED"' EXIT INT TERM
  cp --sparse=always "$artifact" "$staged" 2>/dev/null || cp "$artifact" "$staged"
  if [ "$(file_sha256 "$staged")" != "$expected" ]; then
    echo "staged copy $staged does not match $checksum" >&2
    exit 1
  fi

  # Hard-link the current build as the single previous copy (replacing any
  # stale one), then swap. Creates never see a missing image: the rename
  # replaces the name atomically.
  local had_previous=0
  if [ -e "$current" ]; then
    ln -f "$current" "$previous"
    had_previous=1
  fi
  mv -f "$staged" "$current"

  local healthy=1
  if [ "$(file_sha256 "$current")" != "$expected" ]; then
    echo "health: installed image digest mismatch" >&2
    healthy=0
  fi
  if [ "$healthy" = 1 ] && [ "${ROOTFS_SKIP_DEBUGFS:-0}" != 1 ]; then
    local marker
    if ! marker="$(debugfs -R "cat /etc/everstack-rootfs-version" "$current" 2>/dev/null)"; then
      echo "health: debugfs could not read /etc/everstack-rootfs-version" >&2
      healthy=0
    elif [[ "$marker" != "$ROOTFS_ALIAS-source-"* ]]; then
      echo "health: version marker '$marker' does not start with $ROOTFS_ALIAS-source-" >&2
      healthy=0
    fi
  fi
  if [ "$healthy" = 1 ] && [ -n "${ROOTFS_HEALTH_CMD:-}" ]; then
    if ! sh -c "$ROOTFS_HEALTH_CMD"; then
      echo "health: ROOTFS_HEALTH_CMD failed" >&2
      healthy=0
    fi
  fi

  if [ "$healthy" != 1 ]; then
    if [ "$had_previous" = 1 ]; then
      mv -f "$previous" "$current"
      echo "install failed its health check; restored the previous $current" >&2
    else
      rm -f "$current"
      echo "install failed its health check; removed $current" >&2
    fi
    exit 1
  fi

  cp "$checksum" "$current.sha256"
  rm -f "$previous"
  # Markers from hand installs describe the build just replaced; the
  # .sha256 written above is the one that matches the installed image.
  rm -f "$current.version" "$current.source"
  local legacy
  for legacy in "$current".backup-* "$current".*.backup-*; do
    [ -e "$legacy" ] || continue
    rm -f "$legacy"
    echo "removed legacy backup $legacy"
  done
  printf 'Installed %s (%s)\n' "$current" "$expected"
}

if [ "${1:-}" = "install" ]; then
  shift
  install_rootfs "$@"
  exit 0
fi

if [ ! -f infra/images/Dockerfile.openai-codex-firecracker-rootfs ] || \
   [ ! -f services/sandbox-rootfs/converter/Dockerfile ] || \
   [ ! -x infra/images/expand-gzip-sparse.sh ]; then
  echo "run this script from the Everstack repository root" >&2
  exit 2
fi

SOURCE_REVISION="$(git rev-parse HEAD)"
UBUNTU_BASE="docker.io/library/ubuntu:22.04@sha256:281c5745f657873d78e5531fc5ba8575f46ab7769b94550ac99543f122679986"
ROOTFS_SIZE_MB="${ROOTFS_SIZE_MB:-4096}"
OUTPUT_DIR="${1:-dist/firecracker-rootfs}"
mkdir -p "$OUTPUT_DIR"
OUTPUT_DIR="$(cd "$OUTPUT_DIR" && pwd)"
OUTPUT_PATH="$OUTPUT_DIR/$ROOTFS_ALIAS.ext4"
CHECKSUM_PATH="$OUTPUT_PATH.sha256"
TEMP_OUTPUT="$OUTPUT_PATH.tmp.$$"
TEMP_CHECKSUM="$CHECKSUM_PATH.tmp.$$"
TEMP_COMPRESSED="$OUTPUT_PATH.gz.tmp.$$"
ROOTFS_CONTAINER=""
OUTPUT_CONTAINER=""
TEMP_DIR=""
ROOTFS_VOLUME="everstack-openai-rootfs-$$"
OUTPUT_VOLUME="everstack-openai-output-$$"

if [ -e "$OUTPUT_PATH" ] || [ -e "$CHECKSUM_PATH" ]; then
  echo "refusing to replace existing rootfs output in $OUTPUT_DIR" >&2
  exit 2
fi

cleanup() {
  if [ -n "$ROOTFS_CONTAINER" ]; then
    docker rm -f "$ROOTFS_CONTAINER" >/dev/null 2>&1 || true
  fi
  if [ -n "$OUTPUT_CONTAINER" ]; then
    docker rm -f "$OUTPUT_CONTAINER" >/dev/null 2>&1 || true
  fi
  docker volume rm -f "$ROOTFS_VOLUME" "$OUTPUT_VOLUME" >/dev/null 2>&1 || true
  rm -f "$TEMP_OUTPUT" "$TEMP_CHECKSUM" "$TEMP_COMPRESSED"
  if [ -n "$TEMP_DIR" ]; then
    rm -rf "$TEMP_DIR"
  fi
}
trap cleanup EXIT INT TERM

release_rootfs_container() {
  if [ -n "$ROOTFS_CONTAINER" ]; then
    docker rm -f "$ROOTFS_CONTAINER" >/dev/null
    ROOTFS_CONTAINER=""
  fi
}

release_output_container() {
  if [ -n "$OUTPUT_CONTAINER" ]; then
    docker rm -f "$OUTPUT_CONTAINER" >/dev/null
    OUTPUT_CONTAINER=""
  fi
}

release_volume() {
  volume="$1"
  docker volume rm -f "$volume" >/dev/null
}

# Compose one Dockerfile so a docker-container buildx driver never needs to
# resolve an intermediate local image through a registry.
TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/everstack-openai-recipe.XXXXXX")"
sed "s|^FROM ubuntu:22.04$|FROM $UBUNTU_BASE|" \
  services/sandbox-rootfs/Dockerfile > "$TEMP_DIR/Dockerfile.composed"
sed -n '/^USER root$/,$p' infra/images/Dockerfile.openai-codex-firecracker-rootfs \
  >> "$TEMP_DIR/Dockerfile.composed"
grep -Fxq "FROM $UBUNTU_BASE" "$TEMP_DIR/Dockerfile.composed"
grep -Fq 'ARG CODEX_VERSION=0.154.0' "$TEMP_DIR/Dockerfile.composed"

docker buildx build \
  --build-arg VERSION="openai-codex-base" \
  --build-arg REVISION="$SOURCE_REVISION" \
  --platform linux/amd64 \
  --build-arg SOURCE_REVISION="$SOURCE_REVISION" \
  --load \
  --file "$TEMP_DIR/Dockerfile.composed" \
  --tag "everstack/openai-codex-firecracker-rootfs:$ROOTFS_ALIAS" \
  .

docker run --rm --platform linux/amd64 \
  -e EXPECTED_SOURCE_REVISION="$SOURCE_REVISION" \
  "everstack/openai-codex-firecracker-rootfs:$ROOTFS_ALIAS" sh -ec '
    test "$(codex --version)" = "codex-cli 0.154.0"
    codex exec-server --help >/dev/null
    test -x /usr/local/bin/sandbox-agent
    test "$(cat /etc/everstack-rootfs-version)" = \
      "openai-codex-0.154.0-r3-source-$EXPECTED_SOURCE_REVISION"
  '

docker buildx build \
  --load \
  --file services/sandbox-rootfs/converter/Dockerfile \
  --tag everstack/rootfs-converter:local \
  services/sandbox-rootfs/converter
docker volume create "$ROOTFS_VOLUME" >/dev/null
docker volume create "$OUTPUT_VOLUME" >/dev/null

ROOTFS_CONTAINER="$(docker create --platform linux/amd64 \
  "everstack/openai-codex-firecracker-rootfs:$ROOTFS_ALIAS")"
docker export "$ROOTFS_CONTAINER" | docker run --rm -i \
  --entrypoint sh \
  -v "$ROOTFS_VOLUME:/input" \
  everstack/rootfs-converter:local -ec '
    tar -xpf - -C /input
    printf "sandbox\n" > /input/etc/hostname
    cat > /input/etc/hosts <<"EOF"
127.0.0.1   localhost sandbox
::1         localhost ip6-localhost ip6-loopback
ff02::1     ip6-allnodes
ff02::2     ip6-allrouters
EOF
    mkdir -p /input/tmp /input/proc /input/sys /input/dev
    chown 0:0 /input/etc /input/etc/hostname /input/etc/hosts
    test "$(stat -c %u:%g /input/etc)" = 0:0
    test "$(stat -c %u:%g /input/workspace)" = 1000:1000
  '
release_rootfs_container

docker run --rm \
  -e INPUT_DIR=/input \
  -e OUTPUT_PATH="/output/$ROOTFS_ALIAS.ext4" \
  -e ROOTFS_SIZE_MB="$ROOTFS_SIZE_MB" \
  -v "$ROOTFS_VOLUME:/input:ro" \
  -v "$OUTPUT_VOLUME:/output" \
  everstack/rootfs-converter:local

docker run --rm --entrypoint sh \
  -v "$OUTPUT_VOLUME:/output:ro" \
  everstack/rootfs-converter:local -ec '
    image="/output/openai-codex-0.154.0-r3.ext4"
    test -s "$image"
    check_owner() {
      path="$1"
      uid="$2"
      gid="$3"
      # Keep command failures visible, including missing converter utilities.
      if ! stat_output="$(debugfs -R "stat $path" "$image" 2>&1)"; then
        printf "debugfs stat failed for %s\n%s\n" "$path" "$stat_output" >&2
        exit 1
      fi
      if ! printf "%s\n" "$stat_output" | grep -Eq \
        "User:[[:space:]]+$uid[[:space:]]+Group:[[:space:]]+$gid"; then
        printf "rootfs ownership check failed for %s (expected %s:%s)\n" \
          "$path" "$uid" "$gid" >&2
        printf "%s\n" "$stat_output" | grep -E "User:|Group:" >&2 || true
        exit 1
      fi
    }
    check_owner /etc 0 0
    check_owner /workspace 1000 1000
  '

# Conversion and ownership validation no longer need the expanded input tree.
# Releasing it here avoids retaining two complete filesystem copies while the
# artifact is compressed and transferred to the host.
release_volume "$ROOTFS_VOLUME"

volume_digest="$(docker run --rm --entrypoint sh \
  -v "$OUTPUT_VOLUME:/output" everstack/rootfs-converter:local -ec '
    image="/output/openai-codex-0.154.0-r3.ext4"
    compressed="${image}.transfer.gz"
    digest=$(sha256sum "$image" | cut -d " " -f 1)
    # Reading from stdin keeps BusyBox gzip from embedding a source filename.
    gzip -1 -c < "$image" > "$compressed"
    printf "%s\n" "$digest"
  ')"
[[ "$volume_digest" =~ ^[0-9a-f]{64}$ ]]

OUTPUT_CONTAINER="$(docker create --entrypoint sh \
  -v "$OUTPUT_VOLUME:/output:ro" everstack/rootfs-converter:local)"
docker cp "$OUTPUT_CONTAINER:/output/$ROOTFS_ALIAS.ext4.transfer.gz" "$TEMP_COMPRESSED"
gzip -t -- "$TEMP_COMPRESSED"

# The verified compressed host copy and the captured digest are sufficient to
# validate expansion. Drop the container and Docker volume before allocating
# the 4 GiB host output so both full ext4 copies never coexist.
release_output_container
release_volume "$OUTPUT_VOLUME"

infra/images/expand-gzip-sparse.sh "$TEMP_COMPRESSED" "$TEMP_OUTPUT"
test -s "$TEMP_OUTPUT"
if command -v sha256sum >/dev/null 2>&1; then
  digest="$(sha256sum "$TEMP_OUTPUT" | cut -d ' ' -f 1)"
else
  digest="$(shasum -a 256 "$TEMP_OUTPUT" | cut -d ' ' -f 1)"
fi
[[ "$digest" = "$volume_digest" ]] || {
  echo "expanded rootfs differs after compressed Docker transfer" >&2
  exit 1
}
printf '%s  %s\n' "$digest" "$ROOTFS_ALIAS.ext4" > "$TEMP_CHECKSUM"

# Both outputs were fully built and validated before either final path appears.
mv "$TEMP_OUTPUT" "$OUTPUT_PATH"
mv "$TEMP_CHECKSUM" "$CHECKSUM_PATH"
printf 'Prepared %s\n' "$OUTPUT_PATH"
cat "$CHECKSUM_PATH"
