#!/bin/sh
set -eu

max_attempts="${GO_INSTALL_MAX_ATTEMPTS:-3}"
base_delay_seconds="${GO_INSTALL_RETRY_DELAY_SECONDS:-5}"

for module in "$@"; do
  attempt=1
  until go install "$module"; do
    if [ "$attempt" -ge "$max_attempts" ]; then
      echo "go install failed after ${attempt} attempts: ${module}" >&2
      exit 1
    fi

    delay_seconds=$((base_delay_seconds * attempt))
    echo "go install failed, retrying ${module} in ${delay_seconds}s (attempt $((attempt + 1))/${max_attempts})" >&2
    sleep "$delay_seconds"
    attempt=$((attempt + 1))
  done
done
