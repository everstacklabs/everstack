# Firecracker Agent — standalone gRPC service for managing microVMs
#
# Build:
#   docker build -t ghcr.io/everstacklabs/firecracker-agent:latest -f services/firecracker-agent/Dockerfile .
#
# Run (requires --privileged and /dev/kvm):
#   docker run --privileged --device=/dev/kvm -p 9090:9090 ghcr.io/everstacklabs/firecracker-agent:latest

FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app
COPY go.mod go.sum ./
COPY build/run-with-retry.sh ./build/run-with-retry.sh
RUN ./build/run-with-retry.sh go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -o /firecracker-agent ./services/firecracker-agent/

FROM alpine:3.20

# e2fsprogs-extra carries resize2fs, which the agent shells out to when a
# sandbox asks for more disk than base.ext4 provides. Without it the
# agent still boots VMs, it just provisions them at the base size and
# logs loudly (see applyDiskSize).
RUN apk add --no-cache ca-certificates iptables iproute2 e2fsprogs e2fsprogs-extra

COPY --from=builder /firecracker-agent /usr/local/bin/firecracker-agent

EXPOSE 9090

ENTRYPOINT ["/usr/local/bin/firecracker-agent"]
