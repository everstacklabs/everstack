# Everstack SDK smoke tests

Cross-language smoke tests that exercise the Everstack SDKs against a **live
gateway**. Each suite installs the SDK as a real consumer would and runs the
same core checks: `models.list`, a non-streaming chat completion, and a
streaming chat completion.

Originally created to verify the Node `0.2.2` transport fix (Node defaults to
Connect JSON over HTTP/1.1 instead of gRPC), then extended to cover all three
SDKs.

## Layout

| Dir | SDK | Installed from | Transport |
| --- | --- | --- | --- |
| `node/` | `@everstack/node` | local `0.2.2` tarball (until published to npm) | Connect-RPC (the one the fix touches) |
| `python/` | `everstack` | PyPI `0.1.0` | HTTP (`httpx`) |
| `go/` | `github.com/everstacklabs/everstack-go` | in-repo source via `replace` (module repo not public) | HTTP (`net/http`) |

Only the Node SDK uses Connect/gRPC; Python and Go are plain HTTP, so for them
this is general gateway validation rather than a transport regression test.

## Configure once (shared)

```bash
cd examples/sdk-tests
cp .env.example .env
# edit .env: EVERSTACK_GATEWAY_URL + EVERSTACK_API_KEY
```

Each language loads this shared `.env` (a per-language `./.env` overrides it; a
shell-exported value wins over both).

## Run

```bash
# Node
cd node && npm install && npm test

# Python
cd python && python -m venv .venv && source .venv/bin/activate \
  && pip install -r requirements.txt && python test_all.py

# Go
cd go && go run .
```

See each subdirectory's README for details.

## Notes

- **Node** points at a local tarball because `@everstack/node@0.2.2` is not yet on
  npm (CI publish is blocked on the `NPM_TOKEN` secret). Once published, change the
  dependency in `node/package.json` from the `file:` tarball to `^0.2.2`.
- **Go** uses a `replace` to the in-repo SDK source because the `everstack-go`
  module repository is not publicly resolvable. Swap for a tagged version once it is.
