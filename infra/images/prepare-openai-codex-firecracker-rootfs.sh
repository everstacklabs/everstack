#!/usr/bin/env bash
# Build the pinned OpenAI Codex Firecracker guest and convert it to the exact
# filename consumed by the Firecracker backend. Extraction and conversion stay
# inside Linux Docker volumes so numeric guest ownership is preserved on macOS.

set -euo pipefail

if [ ! -f infra/images/Dockerfile.openai-codex-firecracker-rootfs ] || \
   [ ! -f services/sandbox-rootfs/converter/Dockerfile ] || \
   [ ! -x infra/images/expand-gzip-sparse.sh ]; then
  echo "run this script from the Everstack repository root" >&2
  exit 2
fi

ROOTFS_ALIAS="openai-codex-0.154.0-r3"
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
