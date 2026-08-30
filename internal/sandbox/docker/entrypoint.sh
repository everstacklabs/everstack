#!/bin/sh
# Browser sidecar entrypoint.
# Launches Chromium with CDP and the browser-streamer for live streaming.
# In headed mode, also starts Xvfb for Chromium to render into.
# Never exits — restarts Chromium on crash so the pod stays Running.
#
# CDP exposure (Chromium M113+ workaround):
#   Since Chromium M113, --remote-debugging-address=0.0.0.0 is IGNORED and CDP is
#   forced to bind 127.0.0.1 only (Chromium issue 1425667, WontFix). So Chromium
#   binds CDP on an INTERNAL loopback port, and socat republishes it on the port
#   consumers connect to. We also pass --remote-allow-origins=* or the CDP
#   WebSocket upgrade returns HTTP 403 from any non-null origin (M111+).
#
# Environment variables:
#   BROWSER_HEADLESS      - "true" (default) or "false"
#   BROWSER_CDP_PORT      - external CDP port consumers connect to (default: 9222)
#   BROWSER_CDP_INNER_PORT- loopback port Chromium binds CDP on (default: 9223)
#   BROWSER_STREAM_PORT   - WebSocket streamer port (default: 6080)

CDP_PORT="${BROWSER_CDP_PORT:-9222}"
CDP_INNER_PORT="${BROWSER_CDP_INNER_PORT:-9223}"
STREAM_PORT="${BROWSER_STREAM_PORT:-6080}"
HEADLESS="${BROWSER_HEADLESS:-true}"

# Ensure writable dirs exist (tmpfs mounts may be empty)
mkdir -p /tmp/browser-home /tmp/chromium-data /tmp/.X11-unix 2>/dev/null || true

CHROMIUM_FLAGS="--no-first-run --disable-gpu --disable-dev-shm-usage --no-sandbox \
  --disable-background-networking --disable-sync \
  --user-data-dir=/tmp/chromium-data \
  --remote-debugging-port=${CDP_INNER_PORT} --remote-allow-origins=*"

if [ "$HEADLESS" = "false" ]; then
    # Headed mode: start virtual display for Chromium to render into
    Xvfb :99 -screen 0 1280x720x24 &
    sleep 1
    export DISPLAY=:99
    CHROMIUM_FLAGS="${CHROMIUM_FLAGS} --display=:99"
else
    CHROMIUM_FLAGS="${CHROMIUM_FLAGS} --headless=new"
fi

echo "Browser sidecar starting (headless=${HEADLESS}, cdp_port=${CDP_PORT}, inner=${CDP_INNER_PORT}, stream_port=${STREAM_PORT})"

# Republish the loopback-bound CDP port on the externally-consumed port.
# fork+reuseaddr lets it accept repeated connections and survive Chromium
# restarts (each new connection re-dials 127.0.0.1:${CDP_INNER_PORT}).
socat TCP-LISTEN:${CDP_PORT},fork,reuseaddr TCP:127.0.0.1:${CDP_INNER_PORT} &
SOCAT_PID=$!
echo "socat bridging :${CDP_PORT} -> 127.0.0.1:${CDP_INNER_PORT} (pid ${SOCAT_PID})"

# Start the browser-streamer in the background — it captures CDP screencast
# frames and serves them over WebSocket for the frontend viewer. It connects to
# the internal CDP port directly (does not depend on socat).
STREAMER_PORT="${STREAM_PORT}" \
STREAMER_CDP_URL="http://127.0.0.1:${CDP_INNER_PORT}" \
browser-streamer &
STREAMER_PID=$!

# Retry loop — if Chromium crashes, restart it. Never exit so the pod
# stays in Running phase and the main sandbox container keeps working.
while true; do
    echo "Launching Chromium..."
    chromium ${CHROMIUM_FLAGS} about:blank 2>&1 || true
    echo "Chromium exited, restarting in 2s..."
    sleep 2
done
