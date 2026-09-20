#!/bin/sh
# Convert a prepared rootfs directory into an ext4 filesystem file.
#
# Env:
#   INPUT_DIR      — directory containing the rootfs tree (populated by the
#                    earlier init container that ran the sandbox-rootfs image)
#   OUTPUT_PATH    — where to write the ext4 (e.g. /output/base.ext4)
#   ROOTFS_SIZE_MB — optional, default 2048
#   VERSION_MARKER — optional path inside INPUT_DIR whose contents are used
#                    as the idempotency key (defaults to /etc/everstack-rootfs-version)
#
# Idempotent: if OUTPUT_PATH exists and matches the version marker, skip.

set -eu

: "${INPUT_DIR:?must set INPUT_DIR}"
: "${OUTPUT_PATH:?must set OUTPUT_PATH}"
SIZE_MB="${ROOTFS_SIZE_MB:-2048}"
VERSION_MARKER="${VERSION_MARKER:-/etc/everstack-rootfs-version}"

VERSION_FILE="${INPUT_DIR}${VERSION_MARKER}"
DIGEST_FILE="${OUTPUT_PATH}.version"

if [ -f "$VERSION_FILE" ]; then
  CURRENT_VERSION="$(cat "$VERSION_FILE")"
else
  # Fallback: hash the rootfs contents if no marker file.
  CURRENT_VERSION="$(find "$INPUT_DIR" -type f -printf '%T@ %s %p\n' 2>/dev/null | sha256sum | cut -d' ' -f1)"
fi

echo "[rootfs-converter] Version: $CURRENT_VERSION"

if [ -f "$DIGEST_FILE" ] && [ -f "$OUTPUT_PATH" ]; then
  SAVED="$(cat "$DIGEST_FILE")"
  if [ "$SAVED" = "$CURRENT_VERSION" ]; then
    echo "[rootfs-converter] Rootfs is up-to-date. Skipping rebuild."
    exit 0
  fi
  echo "[rootfs-converter] Version changed: $SAVED -> $CURRENT_VERSION. Rebuilding."
fi

echo "[rootfs-converter] Creating ${SIZE_MB}MB ext4 from ${INPUT_DIR}..."
mkfs.ext4 -q -F -d "$INPUT_DIR" -L everstack-rootfs "${OUTPUT_PATH}.tmp" "${SIZE_MB}M"

mv "${OUTPUT_PATH}.tmp" "$OUTPUT_PATH"
echo "$CURRENT_VERSION" > "$DIGEST_FILE"

echo "[rootfs-converter] Done. Wrote $(du -h "$OUTPUT_PATH" | cut -f1) to $OUTPUT_PATH"
