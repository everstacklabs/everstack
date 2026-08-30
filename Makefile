SHELL := /bin/sh

GO_TAGS ?= ce
VERSION ?= development
COMMIT_SHA ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS := -s -w \
	-X github.com/everstacklabs/everstack/cmd/build.version=$(VERSION) \
	-X github.com/everstacklabs/everstack/cmd/build.commit=$(COMMIT_SHA) \
	-X github.com/everstacklabs/everstack/cmd/build.date=$(BUILD_DATE)

.PHONY: help
help:
	@echo "Everstack Community Edition"
	@echo ""
	@echo "  make build        Build the CE gateway"
	@echo "  make test         Run CE tests"
	@echo "  make run          Run the CE gateway"
	@echo "  make core_api     Regenerate Go and OpenAPI clients"
	@echo "  make core_api_dev Regenerate all API clients"
	@echo "  make build-ui     Build the admin UI"
	@echo "  make build-local  Build a binary with the admin UI embedded"
	@echo "  make validate     Validate the public repository boundary"

.PHONY: install_grpc_dependencies
install_grpc_dependencies:
	@go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.35.1
	@go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2
	@go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@v2.22.0
	@go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@v2.22.0
	@go install github.com/envoyproxy/protoc-gen-validate@v1.1.0
	@go install connectrpc.com/connect/cmd/protoc-gen-connect-go@v1.18.1
	@go install github.com/bufbuild/buf/cmd/buf@v1.45.0

.PHONY: core_api
core_api: install_grpc_dependencies
	rm -rf .artifacts/grpc pkg/grpc openapi/v1/everstack
	mkdir -p pkg/grpc openapi/v1/everstack
	PATH="$$PATH:$$HOME/go/bin" buf generate --template buf.gen.release.yaml
	cp -R .artifacts/grpc/github.com/everstacklabs/everstack/pkg/grpc/. pkg/grpc/
	cp -R .artifacts/grpc/everstack/. openapi/v1/everstack/
	rm -rf .artifacts

.PHONY: core_api_dev
core_api_dev: core_api
	pnpm install --frozen-lockfile --ignore-scripts --filter @everstack/proto...
	rm -rf packages/proto/cjs packages/proto/es packages/proto/esquery packages/proto/types
	pnpm --filter @everstack/proto build

.PHONY: build
build:
	go build -tags="$(GO_TAGS)" -trimpath -ldflags "$(LDFLAGS)" -o everstack .

.PHONY: test
test:
	go test -tags="$(GO_TAGS)" ./...

.PHONY: run
run:
	@if [ -z "$$EVS_API_KEY_HASH_SECRET" ]; then \
		echo "Set EVS_API_KEY_HASH_SECRET before running Everstack."; exit 1; \
	fi
	go run -tags="$(GO_TAGS)" . serve

.PHONY: build-ui
build-ui: install_grpc_dependencies
	pnpm install --frozen-lockfile
	pnpm --filter @everstack/proto build
	pnpm --filter @everstack/admin... build
	rm -rf ui/dist
	mkdir -p ui/dist
	cp -R apps/admin/dist/. ui/dist/

.PHONY: build-local
build-local: build-ui core_api
	mkdir -p dist
	CGO_ENABLED=0 go build -tags="ui_embed,$(GO_TAGS)" -trimpath \
		-ldflags "$(LDFLAGS)" -o dist/everstack .

.PHONY: validate
validate:
	python3 .github/scripts/test_validate_public_repo.py
	python3 .github/scripts/validate-public-repo.py .
	go build ./...
	go build -tags=ce ./...

.PHONY: quickstart-up
quickstart-up:
	docker compose -f examples/quickstart/compose.yaml up -d

.PHONY: quickstart-down
quickstart-down:
	docker compose -f examples/quickstart/compose.yaml down -v
