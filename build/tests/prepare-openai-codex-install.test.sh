#!/usr/bin/env bash
# Install mode of infra/images/prepare-openai-codex-firecracker-rootfs.sh:
# it must leave at most one previous build behind, and none once the new
# build passes its health check.

set -euo pipefail

script="infra/images/prepare-openai-codex-firecracker-rootfs.sh"
bash -n "$script"
alias_name="openai-codex-0.154.0-r3"

test_dir="$(mktemp -d "${TMPDIR:-/tmp}/everstack-install-test.XXXXXX")"
trap 'rm -rf "$test_dir"' EXIT INT TERM

fail() { echo "FAIL: $*" >&2; exit 1; }

sha() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d ' ' -f 1
  else shasum -a 256 "$1" | cut -d ' ' -f 1; fi
}

# make_artifact DIR CONTENT writes a built artifact plus its checksum file,
# the layout the build mode and the release assets produce.
make_artifact() {
  mkdir -p "$1"
  printf '%s' "$2" > "$1/$alias_name.ext4"
  printf '%s  %s\n' "$(sha "$1/$alias_name.ext4")" "$alias_name.ext4" > "$1/$alias_name.ext4.sha256"
}

run_install() {
  # debugfs is not on macOS runners and the fake images are not ext4; the
  # marker check is covered on the node. ROOTFS_HEALTH_CMD stands in for it.
  ROOTFS_SKIP_DEBUGFS=1 bash "$script" install "$@"
}

rootfs="$test_dir/rootfs"
mkdir -p "$rootfs"

# extra_files lists anything in the rootfs dir besides the base image, the
# installed image and its checksum sidecar.
extra_files() {
  find "$rootfs" -mindepth 1 -maxdepth 1 \
    ! -name base.ext4 ! -name "$alias_name.ext4" ! -name "$alias_name.ext4.sha256" \
    -exec basename {} \;
}
printf 'base' > "$rootfs/base.ext4"

# 1. First install: image and checksum land, nothing else is left.
make_artifact "$test_dir/v1" "build-one"
run_install "$test_dir/v1" "$rootfs" >/dev/null
[[ "$(cat "$rootfs/$alias_name.ext4")" == "build-one" ]] || fail "first install did not land"
[[ -f "$rootfs/$alias_name.ext4.sha256" ]] || fail "checksum sidecar missing"
leftovers="$(extra_files)"
[[ -z "$leftovers" ]] || fail "first install left: $leftovers"

# 2. Reinstall over a healthy image with legacy timestamped backups lying
#    around: success removes the previous copy and the legacy backups.
printf 'old' > "$rootfs/$alias_name.ext4.backup-20260912T185145Z-ec9d4e1e6b66-8702"
printf 'old' > "$rootfs/$alias_name.ext4.source.backup-20260912T185145Z-ec9d4e1e6b66-8702"
printf 'stale' > "$rootfs/$alias_name.ext4.source"
printf 'stale' > "$rootfs/$alias_name.ext4.version"
make_artifact "$test_dir/v2" "build-two"
run_install "$test_dir/v2" "$rootfs" >/dev/null
[[ "$(cat "$rootfs/$alias_name.ext4")" == "build-two" ]] || fail "reinstall did not replace the image"
leftovers="$(extra_files)"
[[ -z "$leftovers" ]] || fail "successful reinstall left: $leftovers"

# 3. Failed health check: the previous build is restored in place and no
#    copy of either build is left beside it.
make_artifact "$test_dir/v3" "build-three"
if ROOTFS_HEALTH_CMD='exit 7' run_install "$test_dir/v3" "$rootfs" >/dev/null 2>&1; then
  fail "install succeeded although the health check failed"
fi
[[ "$(cat "$rootfs/$alias_name.ext4")" == "build-two" ]] || fail "failed install did not restore the previous build"
[[ "$(cut -d ' ' -f 1 "$rootfs/$alias_name.ext4.sha256")" == "$(sha "$rootfs/$alias_name.ext4")" ]] \
  || fail "checksum sidecar does not match the restored build"
leftovers="$(extra_files)"
[[ -z "$leftovers" ]] || fail "failed install left: $leftovers"

# 4. Corrupt artifact: refused before anything in the rootfs dir changes.
make_artifact "$test_dir/v4" "build-four"
printf 'tampered' > "$test_dir/v4/$alias_name.ext4"
before="$(cd "$rootfs" && ls -A && cat "$alias_name.ext4")"
if run_install "$test_dir/v4" "$rootfs" >/dev/null 2>&1; then
  fail "install accepted an artifact that fails its checksum"
fi
[[ "$(cd "$rootfs" && ls -A && cat "$alias_name.ext4")" == "$before" ]] || fail "checksum failure changed the rootfs dir"

# 5. Per-env soft cap: an install that would take the env over its share
#    is refused, and nothing changes.
make_artifact "$test_dir/v5" "build-five"
set +e
EVS_ROOTFS_ENV_CAP_MB=0 run_install "$test_dir/v5" "$rootfs" >/dev/null 2>&1
code=$?
set -e
[[ "$code" == 3 ]] || fail "cap exceeded: exit $code, want 3"
[[ "$(cd "$rootfs" && ls -A && cat "$alias_name.ext4")" == "$before" ]] || fail "cap refusal changed the rootfs dir"

echo "PASS: prepare-openai-codex-firecracker-rootfs.sh install"
