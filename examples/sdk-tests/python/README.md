# Everstack Python SDK smoke tests

Tests the **published** `everstack` package from PyPI (currently `0.1.0`) against a
live gateway. The Python SDK is `httpx`-based (plain HTTP), so it has none of the
Connect/gRPC transport concerns that affected the Node SDK — this is general
validation that the gateway works through the Python client.

## Setup

```bash
cd examples/sdk-tests/python
python -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt

# Config is shared with the other SDKs at examples/sdk-tests/.env
cp ../.env.example ../.env   # then edit it
```

## Run

```bash
python test_all.py
```

Covers `models.list`, non-streaming chat, and streaming chat. To test a different
release, edit the pin in `requirements.txt` (e.g. `everstack` for latest) and reinstall.
