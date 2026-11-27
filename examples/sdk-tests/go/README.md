# Everstack Go SDK smoke tests

Tests the Everstack Go SDK against a live gateway. The SDK is `net/http`-based
(OpenAI-compatible REST, no gRPC), so this is general validation of the gateway
through the Go client.

The `everstack-go` module repository is not publicly resolvable, so `go.mod` uses
a `replace` directive to build against the in-repo SDK source
(`packages/sdks/everstack-go`). Swap the `replace` for a real version once the
module is published.

## Run

```bash
cd examples/sdk-tests/go

# Config is shared with the other SDKs at examples/sdk-tests/.env
cp ../.env.example ../.env   # then edit it

go run .
```

Covers `Models.List`, non-streaming chat, and streaming chat.
