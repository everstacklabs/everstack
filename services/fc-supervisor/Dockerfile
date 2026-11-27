# fc-supervisor build image. This binary is NOT a Kubernetes
# workload — it runs as a host systemd unit. The Dockerfile exists
# only to participate in our existing GHCR-publish CI pipeline; the
# release artifact extracted from the image is the binary itself,
# which operators copy to /usr/local/bin/fc-supervisor on each node.
#
# See docs/design/fc-supervisor.md for the architectural rationale
# and services/fc-supervisor/fc-supervisor.service for the install
# instructions.

FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -ldflags="-s -w" -o /fc-supervisor ./services/fc-supervisor/

# Minimal output image — single static binary + the systemd unit
# alongside so operators can `docker run --rm IMAGE cat
# /etc/systemd/system/fc-supervisor.service > /etc/...` if they want.
FROM scratch
COPY --from=builder /fc-supervisor /fc-supervisor
COPY services/fc-supervisor/fc-supervisor.service /fc-supervisor.service
ENTRYPOINT ["/fc-supervisor"]
