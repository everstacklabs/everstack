# syntax=docker/dockerfile:1.7

FROM node:26-alpine AS ui-builder
WORKDIR /src

COPY package.json pnpm-lock.yaml pnpm-workspace.yaml turbo.json ./
COPY buf.yaml buf.lock ./
COPY proto/ ./proto/
COPY model-catalog/ ./model-catalog/
COPY packages/ ./packages/
COPY apps/admin/ ./apps/admin/
COPY pkg/plans/plans.json ./pkg/plans/plans.json

RUN corepack enable && corepack prepare pnpm@10.5.2 --activate
RUN --mount=type=cache,id=pnpm,target=/pnpm/store \
    pnpm config set store-dir /pnpm/store && \
    pnpm install --frozen-lockfile
ENV NODE_OPTIONS=--max-old-space-size=4096
RUN pnpm --filter @everstack/admin... build

FROM golang:1.25-alpine AS go-builder
WORKDIR /src
RUN apk add --no-cache ca-certificates git tzdata

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=ui-builder /src/apps/admin/dist ./ui/dist/

ARG VERSION=development
ARG COMMIT_SHA=unknown
ARG BUILD_DATE=unknown
ARG TARGETOS=linux
ARG TARGETARCH
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    go build -tags="ui_embed,ce" -trimpath \
      -ldflags="-s -w \
        -X github.com/everstacklabs/everstack/cmd/build.version=$VERSION \
        -X github.com/everstacklabs/everstack/cmd/build.commit=$COMMIT_SHA \
        -X github.com/everstacklabs/everstack/cmd/build.date=$BUILD_DATE" \
      -o /out/everstack .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata wget && \
    addgroup -g 1000 everstack && \
    adduser -D -u 1000 -G everstack everstack
WORKDIR /app

COPY --from=go-builder /out/everstack /app/everstack
COPY --from=go-builder --chown=everstack:everstack /src/cmd /app/cmd
COPY --from=go-builder --chown=everstack:everstack /src/internal/database/migrations /app/internal/database/migrations
COPY --from=go-builder --chown=everstack:everstack /src/model-catalog /app/model-catalog
COPY --from=go-builder --chown=everstack:everstack /src/pkg/plans/plans.json /app/pkg/plans/plans.json
COPY --from=go-builder --chown=everstack:everstack /src/cmd/config/gateway/defaults/gateway-default.yaml /app/config/default.yaml
RUN mkdir -p /app/data /app/logs && chown -R everstack:everstack /app

USER everstack
EXPOSE 8089
HEALTHCHECK --interval=15s --timeout=5s --start-period=20s --retries=5 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8089/debug/healthz || exit 1
ENTRYPOINT ["/app/everstack"]
CMD ["serve"]
