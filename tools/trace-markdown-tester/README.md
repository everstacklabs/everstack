# trace-markdown-tester

Sends hand-crafted traces to a local Everstack instance so you can eyeball how the
trace detail sheet renders markdown. Dependency-free: it POSTs raw OTLP/HTTP JSON
(`protojson` `ExportTraceServiceRequest`) straight at `/v1/traces`, so it has full
control over span attributes and can drive every markdown render path deliberately.

## Run

```bash
# local instance needs the gateway up on :8089 (make dev / air serve) and its
# ClickHouse reachable. No API key needed locally — WithTenantAuth falls back to
# the default local tenant (internal/api/http/otlp/auth.go:45).
node tools/trace-markdown-tester/send.mjs           # all scenarios
node tools/trace-markdown-tester/send.mjs flat      # just one
```

Then open `http://localhost:3000/observability/traces` and look for
`markdown-test.<scenario>`.

Env:
- `EVERSTACK_OTLP_URL` (default `http://localhost:8089/v1/traces`)
- `EVERSTACK_API_KEY` (optional; only needed against a shared/managed gateway, sent
  as `Authorization: Bearer` + `x-evs-api-key` — the headers the OTLP ingest
  path authenticates on)

## Scenarios

Each is one trace: a `SERVER` root + a `provider.chat` GENERATION child that carries
the payload. The same kitchen-sink markdown is pushed through each path so you can
compare.

| scenario       | attributes set                          | render path (file)                                  | engine |
| -------------- | --------------------------------------- | --------------------------------------------------- | ------ |
| `flat`         | `input`, `output` (plain strings)       | flat panel, `trace-overview.tsx:890`                | bare `react-markdown` (CommonMark, no GFM) |
| `conversation` | `llm.request.messages`, `llm.response.choices` | `ConversationView`, `conversation-view.tsx:36` | bare `react-markdown` |
| `reasoning`    | + `reasoning_content` on the choice     | `ReasoningBlock`, `conversation-view.tsx:105`       | bare `react-markdown` |
| `tools`        | assistant `tool_calls` + `tool` result  | tool-call card, `tool-call-card.tsx`                | `AgentMarkdown` (marked + GFM + shiki) |

## What to look for

The trace sheet uses **two different markdown engines**. The kitchen-sink content
includes GFM-only features (tables, `~~strikethrough~~`, task lists, autolinks,
footnotes) so the difference is obvious:

- `flat` / `conversation` / `reasoning` render through **bare `react-markdown` with
  no `remark-gfm`** — expect tables to show as raw pipes, strikethrough as literal
  `~~`, task lists as `[ ]`, autolinks as plain text.
- `tools` renders the tool result through **`AgentMarkdown`** (GFM + shiki) — tables,
  strikethrough, task lists, and syntax-highlighted code all render.

So the same markdown looks different depending on where it lands. If the goal is
consistent rendering, the fix is adding `remark-gfm` to the `react-markdown`
instances in `trace-overview.tsx` and `conversation-view.tsx`.
