#!/usr/bin/env python3
"""Smoke tests for the published `everstack` Python SDK against a live gateway.

Usage:
    cp ../.env.example ../.env   # fill in URL + key (shared with the other SDKs)
    pip install -r requirements.txt
    python test_all.py

Reads EVERSTACK_GATEWAY_URL, EVERSTACK_API_KEY, and optional EVERSTACK_TEST_MODEL
from the environment (or a shared ../.env / local ./.env).
"""

import os
import sys

try:
    from dotenv import load_dotenv

    # Shared root config first, then a local override, then real env wins.
    load_dotenv(os.path.join(os.path.dirname(__file__), "..", ".env"))
    load_dotenv()
except ModuleNotFoundError:
    pass

from everstack import Everstack

passed: list[str] = []
failed: list[str] = []


def run(name, fn):
    print(f"  {name} ... ", end="", flush=True)
    try:
        fn()
        print("✅")
        passed.append(name)
    except Exception as e:  # noqa: BLE001 - report any failure
        print(f"❌ {e}")
        failed.append(name)


def require_env(name: str) -> str:
    value = os.environ.get(name)
    if not value:
        print(
            f"Missing required env var: {name}\n"
            "Copy ../.env.example to ../.env and fill it in (gateway URL + API key).",
            file=sys.stderr,
        )
        sys.exit(1)
    return value


def main() -> None:
    api_key = require_env("EVERSTACK_API_KEY")
    base_url = require_env("EVERSTACK_GATEWAY_URL")
    model_override = os.environ.get("EVERSTACK_TEST_MODEL")

    print("\n🧪 Everstack Python SDK smoke tests")
    print(f"   everstack v{getattr(__import__('everstack'), '__version__', '?')}  →  {base_url}\n")

    client = Everstack(api_key=api_key, base_url=base_url)

    models: list = []

    def test_models_list():
        nonlocal models
        res = client.models.list()
        models = res.get("data", []) if isinstance(res, dict) else getattr(res, "data", [])
        print(f"({len(models)} models) ", end="", flush=True)

    run("models.list", test_models_list)

    if model_override:
        chat_model_id = model_override
    else:
        chat_model = next(
            (m for m in models if m.get("owned_by") == "openai" and "embedding" not in m["id"]),
            next((m for m in models if m.get("owned_by") == "anthropic"), models[0] if models else None),
        )
        if not chat_model:
            print("\n❌ No models available. Set EVERSTACK_TEST_MODEL to test a specific model.")
            sys.exit(1)
        chat_model_id = chat_model["id"]

    print(f"   Using chat model: {chat_model_id}\n")

    def test_chat():
        res = client.chat.completions.create(
            model=chat_model_id,
            messages=[{"role": "user", "content": "What is 2+2? One word."}],
            max_tokens=10,
        )
        content = res.choices[0].message.content
        if not content:
            raise ValueError("no content in response")
        print(f'→ "{content.strip()}" ', end="", flush=True)

    run("chat.completions.create (non-streaming)", test_chat)

    def test_chat_stream():
        text = ""
        for chunk in client.chat.completions.create(
            model=chat_model_id,
            messages=[{"role": "user", "content": "Say hello in 3 words"}],
            max_tokens=20,
            stream=True,
        ):
            delta = chunk.choices[0].delta.content if chunk.choices else None
            if delta:
                text += delta
        if not text:
            raise ValueError("no content from stream")
        print(f'→ "{text.strip()}" ', end="", flush=True)

    run("chat.completions.create (streaming)", test_chat_stream)

    print("\n========== RESULTS ==========")
    for name in passed:
        print(f"  ✅ PASS  {name}")
    for name in failed:
        print(f"  ❌ FAIL  {name}")
    print("=============================")
    print(f"{len(passed)}/{len(passed) + len(failed)} passed")
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
