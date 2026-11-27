# Everstack Python SDK

The official Python SDK for the [Everstack](https://everstack.ai) AI platform.

## Installation

```bash
pip install everstack
```

## Quick Start

```python
from everstack import Everstack

client = Everstack(api_key="your-api-key")
```

## Streaming responses

`client.responses.create(..., stream=True)` yields the gateway's stream
frames as dicts, each wrapped as `{"result": {...}}`:

- a snapshot of the whole response (`status: "in_progress"` and the full
  `output`) whenever its structure changes: a new output item or content part;
- a delta frame, with no `status` and no `output`, carrying only what one item
  grew by. `result.delta.item_id` names the item, and
  `result.delta.content_delta` (message text) or
  `result.delta.arguments_delta` (function call arguments) is the fragment to
  append. `output_index` and `content_index` locate the item and its content
  part; like every zero-valued field they are omitted when 0;
- the final, full response (`status: "completed"`, `"failed"` or
  `"incomplete"`).

To rebuild the output as it arrives, replace each item with a snapshot's
version and append each delta's fragment:

```python
items = {}
for frame in client.responses.create(model="gpt-5.5", input=[...], stream=True):
    res = frame.get("result", {})
    delta = res.get("delta")
    if delta:
        item = items.setdefault(delta["item_id"], {"text": "", "arguments": ""})
        item["text"] += delta.get("content_delta", "")
        item["arguments"] += delta.get("arguments_delta", "")
        continue
    for out in res.get("output", []):
        text = "".join(p.get("text", "") for p in out.get("content", []))
        items[out["id"]] = {"text": text, "arguments": out.get("arguments", "")}
```

## Documentation

See the full documentation at [docs.everstack.ai/sdks/python](https://docs.everstack.ai/sdks/python).
