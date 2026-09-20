#!/usr/bin/env bash

set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "usage: $0 <compressed-input> <expanded-output>" >&2
  exit 2
fi

compressed_input="$1"
expanded_output="$2"

# GNU dd can preserve long zero ranges as filesystem holes. This keeps the
# 4 GiB Firecracker image's logical bytes intact without requiring 4 GiB of
# additional runner storage. BSD dd lacks conv=sparse, so macOS keeps the
# established dense expansion path.
if dd --version 2>/dev/null | grep -q 'GNU coreutils'; then
  gzip -dc -- "$compressed_input" |
    dd of="$expanded_output" bs=4M conv=sparse status=none
else
  gzip -dc -- "$compressed_input" > "$expanded_output"
fi
