# Everstack Python SDK — Implementation Plan

## Overview

`everstack-python` — A Pythonic SDK mirroring the Node SDK's API surface, using gRPC/Connect for transport and providing both sync and async interfaces.

**Package name:** `everstack` (PyPI)
**Min Python:** 3.9+

---

## Project Structure

```
everstack-python/
├── src/
│   └── everstack/
│       ├── __init__.py              # Public API exports, Everstack class
│       ├── _client.py               # Everstack client (sync + async)
│       ├── _transport.py            # gRPC/Connect transport setup
│       ├── _compat.py               # OpenAI SDK compatibility helpers
│       ├── _errors.py               # Error hierarchy
│       ├── _types.py                # Shared type aliases, TypedDicts
│       ├── _version.py              # Version string
│       ├── resources/
│       │   ├── __init__.py
│       │   ├── chat.py              # Chat + Completions
│       │   ├── embeddings.py        # Embeddings
│       │   ├── models.py            # Model listing
│       │   ├── agents.py            # Full agent lifecycle
│       │   ├── datasets.py          # Datasets + Evaluations
│       │   ├── memory.py            # Vector memory (REST-based)
│       │   ├── audio.py             # Speech, Transcriptions, Translations
│       │   ├── images.py            # Generation, Edit, Variation
│       │   ├── moderations.py       # Content moderation
│       │   ├── rerank.py            # Document reranking
│       │   ├── responses.py         # Responses API (agentic)
│       │   └── observability.py     # Metrics, sessions, users, outcomes
│       └── generated/
│           └── models.py            # Auto-generated model catalog types
├── tests/
│   ├── conftest.py
│   ├── test_chat.py
│   ├── test_agents.py
│   ├── test_datasets.py
│   └── ...
├── examples/
│   ├── chat_basic.py
│   ├── chat_streaming.py
│   ├── agents_basic.py
│   ├── datasets_eval.py
│   ├── audio_tts.py
│   ├── images_generate.py
│   └── responses_agentic.py
├── scripts/
│   └── generate_models.py          # Model catalog codegen
├── pyproject.toml                   # Build config (hatch/setuptools)
├── README.md
└── py.typed                         # PEP 561 marker
```

---

## Dependencies

| Package | Purpose |
|---------|---------|
| `grpcio` + `grpcio-tools` | gRPC transport |
| `protobuf` | Proto message runtime |
| `httpx` | Async HTTP (memory REST, fallback) |
| `pydantic` >= 2.0 | Response models, validation |
| `typing-extensions` | Backport typing features |

**Optional:**
- `grpcio-status` — rich gRPC error details
- `openai` — compatibility layer re-export

**Dev:**
- `pytest` + `pytest-asyncio` — testing
- `mypy` — type checking
- `ruff` — linting/formatting
- `hatch` — build backend

---

## Proto Codegen Strategy

Use `grpcio-tools` (or `buf generate` with `protoc-gen-python-grpc`) to generate Python stubs from the same `proto/` definitions.

```bash
# In monorepo root
buf generate --template buf.gen.python.yaml
```

Output into `src/everstack/_proto/` (private, not re-exported). The SDK resources wrap these with Pythonic interfaces.

---

## API Design

### Client Instantiation

```python
from everstack import Everstack

# Sync client (default)
client = Everstack(api_key="pk_...")

# Async client
client = Everstack(api_key="pk_...", async_mode=True)
# or
from everstack import AsyncEverstack
client = AsyncEverstack(api_key="pk_...")
```

### Resource Pattern

```python
# Chat completions
response = client.chat.completions.create(
    model="@openai/gpt-4o",
    messages=[{"role": "user", "content": "Hello!"}],
)
print(response.choices[0].message.content)

# Streaming
stream = client.chat.completions.create(
    model="@openai/gpt-4o",
    messages=[{"role": "user", "content": "Hello!"}],
    stream=True,
)
for chunk in stream:
    print(chunk.choices[0].delta.content, end="")

# Async streaming
async for chunk in await async_client.chat.completions.create(
    model="@openai/gpt-4o",
    messages=[...],
    stream=True,
):
    print(chunk.choices[0].delta.content, end="")
```

### Full Resource Surface

```python
client.chat.completions.create(...)

client.embeddings.create(...)

client.models.list()

client.audio.speech.create(...)
client.audio.transcriptions.create(...)
client.audio.translations.create(...)

client.images.generate(...)
client.images.edit(...)
client.images.create_variation(...)

client.moderations.create(...)

client.rerank.create(...)

client.responses.create(...)
client.responses.get(response_id)
client.responses.cancel(response_id)
client.responses.delete(response_id)
client.responses.list(...)

client.agents.create(...)
client.agents.get(...)
client.agents.list(...)
client.agents.update(...)
client.agents.delete(...)
client.agents.sessions.create(...)
client.agents.sessions.run_turn(...)
client.agents.sessions.run_turn_stream(...)
client.agents.lifecycle.provision(...)
client.agents.lifecycle.sleep(...)
client.agents.lifecycle.wake(...)
# ... etc (mirrors Node SDK)

client.datasets.create(...)
client.datasets.items.create_batch(...)
client.evaluations.runs.create(...)
client.evaluations.schedules.create(...)

client.observability.metrics.get_dashboard(...)
client.observability.sessions.list(...)
client.observability.outcomes.get_dashboard(...)

client.memory.collections.create(...)
client.memory.collections.query(...)
```

---

## Type Safety

- All request params are `TypedDict` or `@dataclass` with full type annotations
- All responses are Pydantic `BaseModel` subclasses (frozen, validated)
- Model IDs are `Literal` unions for autocomplete
- `overload` decorators for stream vs non-stream return types
- Full `py.typed` PEP 561 compliance

---

## Error Hierarchy

```python
EverstackError
├── APIError(status_code, code, message)
│   ├── AuthenticationError    # 401
│   ├── PermissionDeniedError  # 403
│   ├── NotFoundError          # 404
│   ├── RateLimitError         # 429 (retry_after property)
│   ├── InternalServerError    # 500
│   └── ServiceUnavailableError # 503
├── TimeoutError
├── ConnectionError
└── InvalidModelError
```

---

## Streaming

- Sync: return a generator/iterator wrapping gRPC stream
- Async: return an async generator/iterator
- Both support context manager (`with` / `async with`) for cleanup
- Response accumulator helper: `stream.get_final_response()`

---

## OpenAI Compatibility

```python
from everstack.compat import create_openai_config
import openai

config = create_openai_config(api_key="pk_...", provider="@openai")
openai_client = openai.OpenAI(**config)
```

---

## Build & Publish

- `pyproject.toml` with `hatch` backend
- CI: `ruff check`, `mypy --strict`, `pytest`
- Publish to PyPI as `everstack`

---

## Implementation Order

1. Transport layer + error handling
2. Chat completions (sync + async, streaming)
3. Embeddings + Models
4. Audio, Images, Moderations, Rerank
5. Responses API
6. Agents (largest resource)
7. Datasets + Evaluations
8. Observability
9. Memory (REST-based)
10. Model catalog codegen
11. OpenAI compat layer
12. Examples + tests
