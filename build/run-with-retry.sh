#!/bin/sh
set -eu

max_attempts="${COMMAND_RETRY_MAX_ATTEMPTS:-3}"
base_delay_seconds="${COMMAND_RETRY_DELAY_SECONDS:-5}"

case "$max_attempts" in
  ''|*[!0-9]*|0)
    echo "COMMAND_RETRY_MAX_ATTEMPTS must be a positive integer" >&2
    exit 2
    ;;
esac

case "$base_delay_seconds" in
  ''|*[!0-9]*)
    echo "COMMAND_RETRY_DELAY_SECONDS must be a non-negative integer" >&2
    exit 2
    ;;
esac

if [ "$#" -eq 0 ]; then
  echo "usage: run-with-retry command [args...]" >&2
  exit 2
fi

attempt=1
while ! "$@"; do
  if [ "$attempt" -ge "$max_attempts" ]; then
    echo "command failed after ${attempt} attempts" >&2
    exit 1
  fi

  delay_seconds=$((base_delay_seconds * attempt))
  attempt=$((attempt + 1))
  echo "command failed, retrying in ${delay_seconds}s (attempt ${attempt}/${max_attempts})" >&2
  sleep "$delay_seconds"
done
