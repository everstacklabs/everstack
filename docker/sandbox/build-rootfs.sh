#!/usr/bin/env bash
# build-rootfs.sh — Build ext4 rootfs images for Firecracker sandbox VMs.
#
# Usage:
#   ./build-rootfs.sh [image_tag] [output_dir]
#
# Defaults:
#   image_tag  = ghcr.io/everstacklabs/sandbox:base
#   output_dir = ./rootfs
#
# This script:
# 1. Builds the sandbox Docker image (if not already present)
# 2. Creates a container from the image
# 3. Exports the filesystem to a tar archive
# 4. Converts the tar to an ext4 image suitable for Firecracker
# 5. Injects the sandbox-agent binary (if built)

set -euo pipefail

IMAGE_TAG="${1:-ghcr.io/everstacklabs/sandbox:base}"
OUTPUT_DIR="${2:-./rootfs}"
ROOTFS_SIZE_MB=2048
AGENT_BINARY="../../cmd/sandbox-agent/sandbox-agent"

mkdir -p "$OUTPUT_DIR"

echo "==> Building sandbox Docker image: ${IMAGE_TAG}"
docker build -t "${IMAGE_TAG}" -f ../../internal/sandbox/docker/Dockerfile.base .

echo "==> Creating temporary container"
CONTAINER_ID=$(docker create "${IMAGE_TAG}")

echo "==> Exporting filesystem to tar"
docker export "${CONTAINER_ID}" -o "${OUTPUT_DIR}/rootfs.tar"
docker rm "${CONTAINER_ID}" > /dev/null

echo "==> Creating ext4 image (${ROOTFS_SIZE_MB}MB)"
dd if=/dev/zero of="${OUTPUT_DIR}/rootfs.ext4" bs=1M count="${ROOTFS_SIZE_MB}"
mkfs.ext4 "${OUTPUT_DIR}/rootfs.ext4"

echo "==> Mounting and extracting filesystem"
MOUNT_DIR=$(mktemp -d)
sudo mount "${OUTPUT_DIR}/rootfs.ext4" "${MOUNT_DIR}"
sudo tar xf "${OUTPUT_DIR}/rootfs.tar" -C "${MOUNT_DIR}"

# Inject sandbox-agent if it exists
if [ -f "${AGENT_BINARY}" ]; then
    echo "==> Injecting sandbox-agent binary"
    sudo cp "${AGENT_BINARY}" "${MOUNT_DIR}/usr/bin/sandbox-agent"
    sudo chmod +x "${MOUNT_DIR}/usr/bin/sandbox-agent"

    # Add systemd service or init script to start agent on boot
    sudo mkdir -p "${MOUNT_DIR}/etc/init.d"
    cat <<'INITEOF' | sudo tee "${MOUNT_DIR}/etc/init.d/sandbox-agent" > /dev/null
#!/bin/sh
### BEGIN INIT INFO
# Provides:          sandbox-agent
# Required-Start:    $local_fs
# Default-Start:     2 3 4 5
# Short-Description: Everstack sandbox agent
### END INIT INFO
/usr/bin/sandbox-agent &
INITEOF
    sudo chmod +x "${MOUNT_DIR}/etc/init.d/sandbox-agent"
    sudo ln -sf /etc/init.d/sandbox-agent "${MOUNT_DIR}/etc/rc2.d/S99sandbox-agent" 2>/dev/null || true
else
    echo "==> Warning: sandbox-agent binary not found at ${AGENT_BINARY}, skipping injection"
fi

sudo umount "${MOUNT_DIR}"
rmdir "${MOUNT_DIR}"

# Clean up tar
rm -f "${OUTPUT_DIR}/rootfs.tar"

echo "==> Done! Rootfs image: ${OUTPUT_DIR}/rootfs.ext4"
echo "    Size: $(du -h "${OUTPUT_DIR}/rootfs.ext4" | cut -f1)"
