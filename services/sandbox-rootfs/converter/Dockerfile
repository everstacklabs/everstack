# rootfs-converter — converts a directory tree to an ext4 file
#
# Used as the second init container in the firecracker-agent DaemonSet.
# The first init container uses the sandbox-rootfs image directly and copies
# its filesystem to a shared emptyDir. This container reads that directory
# and writes an ext4 image using mkfs.ext4 -d (no loopback, no network).

FROM alpine:3.20

RUN apk add --no-cache \
    e2fsprogs \
    e2fsprogs-extra \
    coreutils

COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
