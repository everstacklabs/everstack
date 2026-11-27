#!/usr/bin/env bash

set -euo pipefail

helper="infra/images/expand-gzip-sparse.sh"
bash -n "$helper"

test_dir="$(mktemp -d "${TMPDIR:-/tmp}/everstack-sparse-test.XXXXXX")"
trap 'rm -rf "$test_dir"' EXIT INT TERM

source_file="$test_dir/source"
compressed_file="$test_dir/source.gz"
expanded_file="$test_dir/expanded"

# Include non-zero data at both ends and across a block boundary so a truncated
# or incorrectly positioned sparse expansion cannot pass the byte comparison.
truncate -s 64M "$source_file"
printf 'everstack-sparse-start' | dd of="$source_file" conv=notrunc status=none 2>/dev/null
printf 'everstack-sparse-middle' | dd of="$source_file" bs=1 seek=33554425 conv=notrunc status=none 2>/dev/null
printf 'everstack-sparse-end' | dd of="$source_file" bs=1 seek=67108844 conv=notrunc status=none 2>/dev/null
gzip -1 -c < "$source_file" > "$compressed_file"

"$helper" "$compressed_file" "$expanded_file"
cmp "$source_file" "$expanded_file"
test "$(wc -c < "$expanded_file" | tr -d ' ')" = 67108864

if dd --version 2>/dev/null | grep -q 'GNU coreutils'; then
  allocated_kib=$(du -k "$expanded_file" | cut -f1)
  test "$allocated_kib" -lt 4096 || {
    echo "GNU sparse expansion allocated ${allocated_kib} KiB for a 64 MiB image" >&2
    exit 1
  }
fi

echo "gzip sparse expansion round-trip passed"
