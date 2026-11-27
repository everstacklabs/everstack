# @everstack/node SDK smoke tests

Standalone tests for the **published** `@everstack/node` SDK against a live gateway.

This project lives under `examples/`, which is outside the pnpm workspace globs,
so `@everstack/node` installs from npm (the real published artifact) rather than
linking the workspace source. That is the point: it verifies what consumers
actually get.

Created to verify the 0.2.2 transport fix: the Node transport now defaults to
Connect JSON over HTTP/1.1 instead of gRPC, so calls succeed against gateways
that expose Connect JSON through their edge proxy (0.2.1 failed with
`protocol error: missing status`).

## Setup

```bash
cd examples/sdk-tests

# Install the published SDK (isolated from the monorepo workspace):
npm install
# or with pnpm:
pnpm install --ignore-workspace

cp .env.example .env
# then edit .env with the gateway URL + API key
```

`.env`:

```
EVERSTACK_GATEWAY_URL=https://your-gateway-instance
EVERSTACK_API_KEY=pk_...
# EVERSTACK_TEST_MODEL=@openai/gpt-4o-mini   # optional; auto-discovered otherwise
```

## Run

```bash
npm test                 # run all tests, print a pass/fail summary

npm run test:connectivity  # models.list (unary)
npm run test:chat          # non-streaming chat completion
npm run test:stream        # streaming chat completion
npm run test:transport     # connect (default) vs grpc, side by side
npm run raw                # raw Connect-JSON fetch probe, no SDK (control)

npm run typecheck          # type-check the tests against the installed SDK types
```

## What each test proves

| Test | Path | What it checks |
| --- | --- | --- |
| `01-connectivity` | unary | auth works, Connect-JSON unary requests succeed |
| `02-chat` | unary | the exact path that failed on 0.2.1 now works |
| `03-streaming` | server-stream | Connect streaming over HTTP/1.1 works |
| `04-transport-compare` | unary | default `connect` passes; `grpc` reproduces the old failure |
| `raw-fetch` | raw HTTP | the gateway speaks Connect JSON, independent of the SDK |

To pin the SDK version exactly, set it in `package.json` (e.g. `"@everstack/node": "0.2.2"`)
and reinstall. The header at the top of each run prints the resolved SDK version.
