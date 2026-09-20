# Firecracker guest rootfs — Ubuntu 22.04 + sandbox-agent
#
# This image's filesystem IS the rootfs that boots inside every Firecracker
# microVM. An init container in the firecracker-agent DaemonSet pulls this
# image, exports its filesystem, and converts it to ext4 at pod startup.
#
# Build:
#   docker build -f services/sandbox-rootfs/Dockerfile -t ghcr.io/everstacklabs/sandbox-rootfs:latest .

# --- Stage 1: Build the sandbox-agent binary ---
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
COPY build/run-with-retry.sh ./build/run-with-retry.sh
RUN ./build/run-with-retry.sh go mod download

COPY cmd/sandbox-agent/ ./cmd/sandbox-agent/
COPY internal/sandbox/firecracker/shellframe/ ./internal/sandbox/firecracker/shellframe/
COPY internal/sandbox/toolbox/ ./internal/sandbox/toolbox/

ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -o /sandbox-agent ./cmd/sandbox-agent/

# --- Stage 2: Guest rootfs ---
FROM ubuntu:22.04

# Base VM tooling. Users land as a normal sandbox user, with passwordless sudo
# for package management and development workflows.
#
# The network-debug bundle (wget, iputils-ping, iputils-tracepath, dnsutils,
# netcat-openbsd, jq) is here because every debug session that *doesn't*
# have these turns into a five-minute apt detour — and the line-based shell
# makes pasting multi-step install sequences flaky. Cost: ~10MB on a rootfs
# that's already hundreds of MB. The win is that "curl/ping/dig/nc just work"
# is the documented contract.
RUN apt-get update && apt-get install -y --no-install-recommends \
    systemd \
    systemd-sysv \
    ca-certificates \
    iproute2 \
    iptables \
    curl \
    wget \
    iputils-ping \
    iputils-tracepath \
    dnsutils \
    netcat-openbsd \
    jq \
    unzip \
    git \
    haveged \
    sudo \
    openssh-server \
    tmux \
    docker.io \
    python3 \
    python3-pip \
    python-is-python3 \
  && rm -rf /var/lib/apt/lists/* /var/cache/apt/*

# Node.js: modern LTS from NodeSource, NOT Ubuntu 22.04's apt `nodejs`
# (which is 12.22.9, EOL since 2022). Node 12 breaks anything built with
# a current toolchain: e.g. `npx vite` dies with `SyntaxError: Unexpected
# token '.'` because Vite's bundle uses optional chaining /
# `Module.enableCompileCache?.()` that the v12 parser can't read. We pin
# Node 22 (current LTS) via NodeSource so the sandbox matches what users
# develop against locally.
#
# corepack (bundled with Node) provides pnpm + yarn shims system-wide with
# no global npm write, so `pnpm install` / `npx vite` work out of the box.
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
  && apt-get install -y --no-install-recommends nodejs \
  && rm -rf /var/lib/apt/lists/* /var/cache/apt/* \
  && corepack enable \
  && corepack prepare pnpm@latest yarn@stable --activate \
  && node --version && npm --version && pnpm --version

# Deno: the third supported isolated-function runtime. Deno ships a single
# self-contained static binary (no runtime deps), so we just drop the
# pinned release on PATH system-wide — `deno run` then works for every
# user and for the function wrapper. TARGETARCH is re-declared here because
# ARGs don't cross stage boundaries; it maps to Deno's release arch names.
ARG TARGETARCH
ARG DENO_VERSION=v2.1.4
RUN set -eux; \
    case "${TARGETARCH:-amd64}" in \
      amd64) DENO_ARCH=x86_64-unknown-linux-gnu ;; \
      arm64) DENO_ARCH=aarch64-unknown-linux-gnu ;; \
      *) echo "unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl -fsSL "https://github.com/denoland/deno/releases/download/${DENO_VERSION}/deno-${DENO_ARCH}.zip" -o /tmp/deno.zip; \
    unzip -o /tmp/deno.zip -d /usr/local/bin; \
    rm -f /tmp/deno.zip; \
    chmod +x /usr/local/bin/deno; \
    deno --version

# Guest agent binary
COPY --from=builder /sandbox-agent /usr/local/bin/sandbox-agent

# Systemd unit — starts the agent as soon as the kernel loads vsock.
#
# KillMode=process is load-bearing. Default control-group mode means
# systemd sends SIGTERM to EVERY process in the unit's cgroup when
# sandbox-agent stops — including the tmux server that the agent
# spawned via `tmux new-session -A -d`. Result: every sandbox-agent
# restart (an OOM kill, a panic, even a `systemctl restart`) wipes
# every active user shell in the VM. tmux's whole job is to survive
# disconnects; with the default kill mode it can't even survive its
# parent restarting.
#
# With KillMode=process, only the main agent process gets the signal.
# The daemonized tmux server, which lives at a known socket path
# (/var/run/tmux-sandbox by sandbox-agent's convention), survives
# untouched. After sandbox-agent respawns, it re-attaches to the
# same socket and the user's sessions are right where they left them.
COPY <<'EOF' /etc/systemd/system/sandbox-agent.service
[Unit]
Description=Everstack Sandbox Agent (vsock RPC)
After=systemd-modules-load.service
Wants=systemd-modules-load.service

[Service]
Type=simple
ExecStart=/usr/local/bin/sandbox-agent
Restart=always
RestartSec=1
KillMode=process
StandardOutput=journal
StandardError=journal
User=root

[Install]
WantedBy=multi-user.target
EOF

RUN systemctl enable sandbox-agent.service

# Ensure vsock modules load at boot
COPY <<'EOF' /etc/modules-load.d/vsock.conf
vsock
vmw_vsock_virtio_transport
EOF

# Workspace directory and non-root login user.
RUN useradd -m -s /bin/bash sandbox \
  && usermod -aG sudo,docker sandbox \
  && echo "sandbox ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/90-sandbox \
  && chmod 0440 /etc/sudoers.d/90-sandbox \
  && mkdir -p /workspace \
  && chown sandbox:sandbox /workspace \
  && chmod 775 /workspace

# Per-user npm global prefix so `npm install -g <pkg>` works WITHOUT sudo.
# NodeSource installs node into /usr, whose global module dir
# (/usr/lib/node_modules) is root-only, so `npm i -g pnpm` as the sandbox
# user fails with `EACCES ... mkdir '/usr/local/lib/node_modules'`, which is
# exactly what users hit. Pointing npm's prefix at ~/.npm-global and putting
# its bin on PATH makes global installs land in a user-writable place. The
# env vars go in /etc/profile.d (login shells) and /home/sandbox/.bashrc
# (the interactive non-login shells tmux panes use), so both paths agree.
RUN install -d -o sandbox -g sandbox -m 0755 /home/sandbox/.npm-global \
  && printf 'export NPM_CONFIG_PREFIX="$HOME/.npm-global"\nexport PATH="$HOME/.npm-global/bin:$PATH"\n' \
       > /etc/profile.d/90-npm-global.sh \
  && chmod 0644 /etc/profile.d/90-npm-global.sh \
  && printf '\n# Everstack: user-writable npm global prefix (no sudo for `npm i -g`)\nexport NPM_CONFIG_PREFIX="$HOME/.npm-global"\nexport PATH="$HOME/.npm-global/bin:$PATH"\n' \
       >> /home/sandbox/.bashrc

# Stable guest-side hostname. kubelet overwrites /etc/hostname at
# container-start time with the pod's name (e.g.
# "firecracker-agent-xxxxx"), which would otherwise leak through to
# every VM's bash prompt as `sandbox@firecracker-agent-xxxxx:`. The
# helm init container that prepares the rootfs also writes this
# file as a defense-in-depth; baking it here keeps direct-run / dev
# workflows aligned.
RUN echo "sandbox" > /etc/hostname

# Quiet, brand-aligned shell prompt. Default bash on Ubuntu embeds
# the hostname via \h in /etc/bash.bashrc AND /etc/skel/.bashrc,
# both of which unconditionally re-set PS1 — so a /etc/profile.d
# script that sets PS1 first gets clobbered. Patch the source
# files directly: replace `\u@\h:\w` with `\u:\w` everywhere. Users
# can still override in their own ~/.bashrc if they want.
RUN sed -i 's/\\u@\\h:\\w/\\u:\\w/g' /etc/bash.bashrc /etc/skel/.bashrc /home/sandbox/.bashrc \
  && sed -i 's/\\u@\\h: \\w/\\u:\\w/g' /etc/bash.bashrc /etc/skel/.bashrc /home/sandbox/.bashrc

# Shell command audit (Phase A of the shell-logs pipeline).
#
# Every command typed in any interactive bash inside the VM —
# regardless of which tmux session or which terminal client — is
# captured to /var/log/everstack/shell-audit.log. The in-VM
# sandbox-agent tails this file in Phase B and forwards new entries
# to the host log buffer; the Logs tab consumes that buffer.
#
# Why a file on disk and not a direct IPC: a single shared file
# trivially gives us cross-tmux-session aggregation (all bashes
# write the same place), is robust to sandbox-agent restarts (just
# resumes from the last position), and is easy to inspect by hand
# during development.
#
# Format is TSV: ISO timestamp, user, bash pid, command. Tabs and
# newlines inside the command are escaped to keep the format
# unambiguous for the host-side parser.
RUN install -d -o sandbox -g sandbox -m 0755 /var/log/everstack \
  && install -o sandbox -g sandbox -m 0664 /dev/null /var/log/everstack/shell-audit.log

# Audit hook appended to /etc/bash.bashrc so it runs for both
# interactive non-login bash (the default for tmux panes after the
# first) AND login bash (via /etc/bash.bashrc being sourced from
# /etc/profile on most Ubuntu setups). The guard prevents double-
# install if a user's ~/.bashrc sources /etc/bash.bashrc explicitly.
#
# Implementation notes:
#   - PROMPT_COMMAND fires after each command, before the next prompt.
#   - `history 1` returns the last entry; we cache the entry number
#     so a bare Enter (no new history entry) doesn't duplicate the
#     previous command.
#   - $$ is the bash pid; useful for correlating an audit entry to a
#     specific terminal client when multiple users share a sandbox.
#   - Failures (e.g., disk full, dir missing) are silently dropped —
#     audit must NEVER break the user's shell.
RUN cat >> /etc/bash.bashrc <<'BASHRC'

# Everstack shell-command audit — do not edit, managed by rootfs build.
if [ -n "$BASH_VERSION" ] && [ -z "$__EVERSTACK_AUDIT_INSTALLED" ]; then
    export __EVERSTACK_AUDIT_INSTALLED=1
    __EVERSTACK_AUDIT_LAST_NUM=
    __everstack_audit() {
        local rc=$? line num cmd
        line=$(HISTTIMEFORMAT= history 1 2>/dev/null) || return $rc
        # Parse "  42  ls -la" → num=42, cmd="ls -la"
        read -r num cmd <<< "$line"
        if [ -n "$cmd" ] && [ "$num" != "$__EVERSTACK_AUDIT_LAST_NUM" ]; then
            __EVERSTACK_AUDIT_LAST_NUM=$num
            # Escape tabs/newlines so TSV stays unambiguous.
            cmd=${cmd//$'\t'/    }
            cmd=${cmd//$'\n'/\\n}
            printf '%s\t%s\t%s\t%s\n' \
                "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
                "${USER:-?}" \
                "$$" \
                "$cmd" \
                >> /var/log/everstack/shell-audit.log 2>/dev/null
        fi
        return $rc
    }
    PROMPT_COMMAND="__everstack_audit; ${PROMPT_COMMAND:-:}"
fi
BASHRC

COPY <<'EOF' /etc/ssh/sshd_config.d/everstack-sandbox.conf
PermitRootLogin no
PasswordAuthentication no
PubkeyAuthentication yes
AllowUsers sandbox
EOF

# Serial console for Firecracker: MASK the interactive getty.
#
# The kernel cmdline carries `console=ttyS0`, so systemd's getty generator
# auto-instantiates serial-getty@ttyS0.service. But the host VMM does not wire
# an interactive stdin to ttyS0 (launcher.go leaves cmd.Stdin unset → /dev/null),
# so the getty blocks on dev-ttyS0.device for the full 90s DefaultTimeoutStartSec
# and prints the alarming "Timed out waiting for device /dev/ttyS0 / Dependency
# failed for Serial Getty on ttyS0" lines on every boot. We never use an
# interactive serial LOGIN (control is via the vsock/HTTP agent + SSH), and
# console OUTPUT still flows to console.log regardless of the getty. Mask (not
# disable) because the generator would re-create it; masking silences the stall.
RUN systemctl mask serial-getty@ttyS0.service

# Docker is installed but NOT auto-started. dockerd in a Firecracker
# microVM is the single biggest cause of the "VM dies ~5 minutes after
# boot" failure: its boot unit has no start-timeout ("A start job is
# running for Container Engine … / no limit"), so when dockerd +
# containerd hang or exhaust the guest's RAM (the default sandbox is
# 1024MB Ubuntu), they wedge the whole systemd boot and the guest
# OOM-freezes mid-boot — observed directly in the captured guest
# console. Mask the units so the VM boots clean and stays alive; a user
# who needs Docker can `sudo systemctl unmask --now docker.service`
# inside the sandbox (and should size the sandbox above 1GB first).
RUN systemctl disable docker.service docker.socket containerd.service 2>/dev/null || true \
    && systemctl mask docker.service docker.socket

# Disable services we don't need inside a microVM. snapd, multipathd,
# and the random-seed/entropy waits all add boot latency or memory in a
# VM with no hardware to manage; masking them keeps boot fast and lean.
RUN systemctl mask \
    apt-daily.service \
    apt-daily-upgrade.service \
    systemd-timesyncd.service

# Entropy. The Firecracker CI guest kernel (vmlinux-5.10.x) does NOT honour
# random.trust_cpu, and a microVM exposes no hardware RNG, so the kernel CRNG
# never seeds. Every getrandom() caller then blocks until init: boot wedges
# for minutes on "A start job is running for Save Random Seed" (systemd-
# random-seed) and the agent's shell server / sshd keygen stall too, until
# the guest is reaped ~5 min in. The fcagent already passes random.trust_cpu
# and attaches a virtio-rng device, but the CI kernel ignores both, so the
# fix has to live in the guest: haveged feeds the pool from CPU timing
# jitter, kernel-independent. A drop-in forces it ahead of sysinit/
# random-seed so entropy exists before anything blocks on it.
RUN mkdir -p /etc/systemd/system/haveged.service.d \
 && printf '[Unit]\nDefaultDependencies=no\nBefore=sysinit.target systemd-random-seed.service\nAfter=systemd-tmpfiles-setup-dev.service\nConflicts=shutdown.target\n' \
      > /etc/systemd/system/haveged.service.d/10-early.conf \
 && systemctl enable haveged.service

# Marker used both for human-readable provenance ("what version is this?")
# and — more importantly — as the idempotency key for the rootfs-converter
# init container that turns this tree into a Firecracker ext4. The
# converter skips its rebuild when the marker matches the previous run.
#
# REVISION is required here because VERSION on its own resolves to the
# contents of .github/version, which is bumped manually per release and
# stays static across all the master commits in between. Two master
# pushes without a version bump would both produce images stamped with
# the same VERSION, the converter would see identical markers, skip the
# ext4 rebuild, and every VM would keep booting whichever sandbox-agent
# binary was first baked into the ext4. Symptom: code changes ship, the
# image ships, the daemonset pulls it, but VMs still run the old binary.
# Suffixing with the build SHA makes every commit produce a unique marker.
ARG VERSION=dev
ARG REVISION=unknown
RUN echo "$VERSION-$REVISION" > /etc/everstack-rootfs-version
