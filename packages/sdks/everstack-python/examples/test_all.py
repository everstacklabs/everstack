#!/usr/bin/env python3
"""
Comprehensive SDK test — exercises all resource types.

Usage:
    EVERSTACK_API_KEY=pk_... python examples/test_all.py
    EVERSTACK_API_KEY=pk_... EVERSTACK_GATEWAY_URL=http://localhost:8089 python examples/test_all.py
"""

import os
import sys
import traceback
from typing import Optional

# Allow running from repo root or from the SDK dir
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "src"))

from everstack import Everstack

passed: list[str] = []
failed: list[str] = []
skipped: list[str] = []


def run(name: str, fn):
    print(f"  {name} ... ", end="", flush=True)
    try:
        fn()
        print("✅")
        passed.append(name)
    except Exception as e:
        print(f"❌ {e}")
        failed.append(name)


def skip(name: str, reason: str):
    print(f"  {name} ... ⏭️  {reason}")
    skipped.append(name)


def main():
    api_key = os.environ.get("EVERSTACK_API_KEY")
    base_url = os.environ.get("EVERSTACK_GATEWAY_URL", "http://localhost:8089")

    if not api_key:
        print("EVERSTACK_API_KEY is required", file=sys.stderr)
        sys.exit(1)

    print(f"\n🧪 Everstack Python SDK — Full Test Suite")
    print(f"   Gateway: {base_url}\n")

    client = Everstack(api_key=api_key, base_url=base_url)

    # ── Models ──────────────────────────────────────────────
    models = []

    def test_models_list():
        nonlocal models
        res = client.models.list()
        models = res.get("data", [])
        print(f"({len(models)} models) ", end="", flush=True)

    run("models.list", test_models_list)

    chat_model = next(
        (m for m in models if m.get("owned_by") == "openai" and "embedding" not in m["id"]),
        next((m for m in models if m.get("owned_by") == "anthropic"), None),
    )
    if chat_model is None and models:
        chat_model = models[0]

    embedding_model = next(
        (m for m in models if "embedding" in m["id"]),
        None,
    )

    if not chat_model:
        print("\n❌ No models available. Is the gateway running?")
        sys.exit(1)

    print(f"\n   Using chat model: {chat_model['id']}")
    if embedding_model:
        print(f"   Using embedding model: {embedding_model['id']}")
    print()

    # ── Chat Completions ────────────────────────────────────
    def test_chat():
        res = client.chat.completions.create(
            model=chat_model["id"],
            messages=[{"role": "user", "content": "What is 2+2? One word."}],
            max_tokens=10,
        )
        content = res.choices[0].message.content
        if not content:
            raise ValueError("No content in response")
        print(f'→ "{content.strip()}" ', end="", flush=True)

    run("chat.completions.create (non-streaming)", test_chat)

    def test_chat_stream():
        text = ""
        for chunk in client.chat.completions.create(
            model=chat_model["id"],
            messages=[{"role": "user", "content": "Say hello in 3 words"}],
            max_tokens=20,
            stream=True,
        ):
            delta = chunk.choices[0].delta.content if chunk.choices else None
            if delta:
                text += delta
        if not text:
            raise ValueError("No content from stream")
        print(f'→ "{text.strip()}" ', end="", flush=True)

    run("chat.completions.create (streaming)", test_chat_stream)

    # ── Embeddings ──────────────────────────────────────────
    if embedding_model:

        def test_embeddings():
            res = client.embeddings.create(
                model=embedding_model["id"],
                input="Hello, world!",
            )
            dims = len(res.get("data", [{}])[0].get("embedding", []))
            print(f"→ {dims} dims ", end="", flush=True)

        run("embeddings.create", test_embeddings)
    else:
        skip("embeddings.create", "no embedding model available")

    # ── Responses API ───────────────────────────────────────
    def test_responses_create():
        res = client.responses.create(
            model=chat_model["id"],
            input=[{"role": "user", "content": "What is 1+1?"}],
        )
        print(f"→ id={res.id} status={res.status} ", end="", flush=True)

    run("responses.create", test_responses_create)

    def test_responses_list():
        res = client.responses.list(limit=5)
        print(f"→ {len(res.data)} responses ", end="", flush=True)

    run("responses.list", test_responses_list)

    # ── Agents ──────────────────────────────────────────────
    def test_agents_list():
        res = client.agents.list()
        count = len(res.get("agents", []))
        print(f"→ {count} agents ", end="", flush=True)

    run("agents.list", test_agents_list)

    test_agent_id: Optional[str] = None
    test_session_id: Optional[str] = None

    def test_agents_create():
        nonlocal test_agent_id
        res = client.agents.create(
            name="sdk-test-agent",
            description="Temporary agent created by SDK test suite",
            model=chat_model["id"],
            system_prompt="You are a helpful test assistant. Keep answers very short.",
            max_turns=5,
        )
        test_agent_id = res.get("agent", {}).get("id")
        if not test_agent_id:
            raise ValueError("No agent ID returned")
        print(f"→ id={test_agent_id} ", end="", flush=True)

    run("agents.create", test_agents_create)

    if test_agent_id:

        def test_sessions_create():
            nonlocal test_session_id
            res = client.agents.sessions.create(agent_id=test_agent_id)
            test_session_id = res.get("session", {}).get("id")
            if not test_session_id:
                raise ValueError("No session ID returned")
            print(f"→ id={test_session_id} ", end="", flush=True)

        run("agents.sessions.create", test_sessions_create)
    else:
        skip("agents.sessions.create", "no agent created")

    if test_session_id:

        def test_run_turn():
            res = client.agents.sessions.run_turn(
                session_id=test_session_id,
                user_input="What is the capital of France? One word.",
            )
            text = res.get("turn", {}).get("assistant_message", "")
            print(f'→ "{text.strip()[:60]}" ', end="", flush=True)

        run("agents.sessions.run_turn", test_run_turn)

        def test_run_turn_stream():
            text = ""
            for event in client.agents.sessions.run_turn_stream(
                session_id=test_session_id,
                user_input="What is 10 * 10? One word.",
                enable_streaming=True,
            ):
                delta = event.get("text_delta", "")
                if delta:
                    text += delta
            print(f'→ "{text.strip()[:60]}" ', end="", flush=True)

        run("agents.sessions.run_turn_stream", test_run_turn_stream)
    else:
        skip("agents.sessions.run_turn", "no session created")
        skip("agents.sessions.run_turn_stream", "no session created")

    # Cleanup: delete the test agent
    if test_agent_id:

        def test_agents_delete():
            client.agents.delete(test_agent_id)
            print(f"→ deleted {test_agent_id} ", end="", flush=True)

        run("agents.delete (cleanup)", test_agents_delete)

    # ── Datasets ────────────────────────────────────────────
    def test_datasets_list():
        client.datasets.list()
        print("→ listed ", end="", flush=True)

    run("datasets.list", test_datasets_list)

    # ── Evaluations ─────────────────────────────────────────
    def test_evals_list():
        client.evaluations.runs.list()
        print("→ listed ", end="", flush=True)

    run("evaluations.runs.list", test_evals_list)

    # ── Observability ───────────────────────────────────────
    def test_obs_dashboard():
        client.observability.metrics.get_dashboard()
        print("→ ok ", end="", flush=True)

    run("observability.metrics.get_dashboard", test_obs_dashboard)

    # ── Summary ─────────────────────────────────────────────
    print(f"\n{'─' * 50}")
    print(f"  ✅ Passed:  {len(passed)}")
    if skipped:
        print(f"  ⏭️  Skipped: {len(skipped)}")
    if failed:
        print(f"  ❌ Failed:  {len(failed)}")
        for f in failed:
            print(f"     - {f}")
        sys.exit(1)
    print()

    client.close()


if __name__ == "__main__":
    main()
