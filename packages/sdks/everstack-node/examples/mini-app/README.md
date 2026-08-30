# Everstack SDK Mini App

Standalone Node.js app that exercises the published `@everstack/node` SDK end-to-end. Use it to verify the SDK works against any Everstack gateway (local or remote).

## Setup

```bash
cd packages/sdks/everstack-node/examples/mini-app
cp .env.example .env
# edit .env to set EVERSTACK_API_KEY and EVERSTACK_GATEWAY_URL
npm install
```

> The app pulls `@everstack/node` from npm. If the published package isn't available yet, run `npm install ../../  --no-save` to install the local build, or use `pnpm link --global` from the SDK package and `pnpm link --global @everstack/node` here.

## Commands

| Command | What it does |
|---|---|
| `npm start` | Prints help + config |
| `npm run models` | Lists every model the gateway exposes, grouped by provider |
| `npm run chat -- "your prompt"` | One-shot non-streaming chat completion |
| `npm run stream -- "your prompt"` | Streaming chat completion with first-token + total latency |
| `npm run agents` | Lists agents on the gateway |
| `npm run repl` | Interactive multi-turn chat REPL (`reset` clears history, `exit` quits) |

Set `MODEL` to override the default `gpt-4o-mini`:

```bash
MODEL=claude-sonnet-4-6 npm run chat -- "explain async iterators"
MODEL=gpt-4o npm run repl
```

## Quick sanity check

```bash
npm run models                              # confirms auth + gateway connectivity
npm run chat -- "ping"                      # confirms basic chat works
npm run stream -- "count to five slowly"    # confirms streaming works
npm run repl                                # confirms multi-turn + history works
```

If `models` works but `chat` fails, the gateway is reachable but the SDK's chat resource is broken. If `models` fails too, check `EVERSTACK_GATEWAY_URL` and your API key.
