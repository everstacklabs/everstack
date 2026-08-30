# Contributing to Everstack

Thank you for helping improve Everstack Community Edition.

## Before you start

- Use [GitHub Discussions](https://github.com/everstacklabs/everstack/discussions)
  for questions and design exploration.
- Search [existing issues](https://github.com/everstacklabs/everstack/issues)
  before opening a new one.
- Open a design issue before changing a public API, persistence format,
  authorization boundary, or major module interface.
- Report vulnerabilities privately according to [SECURITY.md](./SECURITY.md).

By participating, you agree to follow the
[Code of Conduct](./CODE_OF_CONDUCT.md).

## Development requirements

- Go 1.25+
- Node.js 20+
- pnpm 10.5.2 (`corepack enable` is sufficient)
- PostgreSQL 16 for integration and local-server work
- Docker with Compose v2 for the packaged quickstart

The protobuf toolchain is installed by the Make target below.

## Set up the repository

```bash
git clone https://github.com/everstacklabs/everstack.git
cd everstack
pnpm install --frozen-lockfile
make install_grpc_dependencies
make core_api_dev
```

Run the local Community Edition stack with Docker:

```bash
docker compose -f examples/quickstart/compose.yaml up -d --build
```

Or build the backend directly:

```bash
go build -tags=ce -o ./everstack .
```

Community Edition is the safe default for untagged public builds. CI also uses
the explicit `ce` tag to verify the exact semantics shipped in release
artifacts. The unrestricted private development wiring is not present in this
repository.

## Find a first contribution

Browse issues labeled
[`good first issue`](https://github.com/everstacklabs/everstack/issues?q=is%3Aissue%20state%3Aopen%20label%3A%22good%20first%20issue%22)
or `help wanted`. Documentation corrections, provider fixtures, model-catalog
validation, and focused regression tests are good entry points. Security,
authentication, tenancy, and persistence changes require a design issue and
maintainer review first.

If no labeled issue fits, start a Discussion rather than opening a broad pull
request without alignment.

## Make a change

1. Fork the repository and create a focused branch.
2. Add or update tests with the implementation.
3. Keep generated files and their source definitions in sync. Change `.proto`
   files rather than editing generated clients manually.
4. Update user-facing documentation when behavior changes.
5. Avoid drive-by formatting or unrelated refactors in the same pull request.

### Common checks

```bash
# Backend
go test -tags=ce ./...
go build ./...
go build -tags=ce ./...

# Frontend packages and applications
pnpm lint
pnpm check-types
pnpm --filter @everstack/admin... build
pnpm --filter @everstack/docs build

# Public repository boundary, links, and secret hygiene
make validate
```

For a smaller Go test, pass the package path directly:

```bash
go test ./internal/providers/openai
```

## Pull requests

A good pull request includes:

- the user or operator problem being solved;
- a focused description of the approach;
- tests and the commands used to run them;
- migration, compatibility, security, and operational impact;
- screenshots or recordings for visible UI changes; and
- documentation updates where appropriate.

Maintainers may ask to split a broad pull request. Review prioritizes security,
backward compatibility, operational safety, and a clear public interface over
raw feature count.

## Generated code

Proto definitions under `proto/everstack/` are the source of truth.

```bash
make core_api       # Go and OpenAPI output
make core_api_dev   # Go, OpenAPI, and TypeScript output
```

Do not hand-edit generated protobuf, Connect, SDK, or route-tree files unless a
maintainer documents an exception.

## Licensing

Unless stated otherwise, contributions are accepted under the repository's
[Apache License 2.0](./LICENSE). You must have the right to submit the code and
other material in your contribution.
