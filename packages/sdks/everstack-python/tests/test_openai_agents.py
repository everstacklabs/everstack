import json

import httpx

from everstack import (
    AsyncOpenAIAgents,
    AsyncEverstack,
    Everstack,
    OpenAIAgentApproval,
    OpenAIAgentArtifact,
    OpenAIAgentCapabilities,
    OpenAIAgentUsage,
    OpenAIAgents,
)


def test_openai_agents_session_protocol_and_durable_cursor() -> None:
    requests = []

    def handler(request: httpx.Request) -> httpx.Response:
        requests.append(request)
        assert request.headers["x-evs-api-key"] == "pk_test"
        assert request.headers["x-evs-org-id"] == "org"
        if request.method == "POST":
            body = json.loads(request.content)
            assert body["idempotency_key"] == "intent-1"
            return httpx.Response(201, json={"id": "s1", "provider": "openai_agents"})
        assert dict(request.url.params) == {"after": "3", "limit": "10"}
        return httpx.Response(200, json={"data": [], "next_cursor": 3, "has_more": False})

    client = Everstack(api_key="pk_test", base_url="https://example.test", org_id="org")
    client._transport._client = httpx.Client(
        base_url="https://example.test",
        headers=client._transport._client.headers,
        transport=httpx.MockTransport(handler),
    )
    session = client.openai_agents.sessions.create(
        connection_id="c1",
        idempotency_key="intent-1",
        model="gpt",
        environment={"type": "none"},
        input="hello",
    )
    assert session["id"] == "s1"
    page = client.openai_agents.sessions.events("s1", after=3, limit=10)
    assert page["next_cursor"] == 3
    assert len(requests) == 2


def test_openai_agents_mutation_is_single_attempt() -> None:
    calls = 0

    def handler(request: httpx.Request) -> httpx.Response:
        nonlocal calls
        calls += 1
        raise httpx.ConnectError("closed", request=request)

    client = Everstack(api_key="pk", base_url="https://example.test")
    client._transport._client = httpx.Client(
        base_url="https://example.test",
        headers=client._transport._client.headers,
        transport=httpx.MockTransport(handler),
    )
    try:
        client.openai_agents.connections.create(name="test", api_key="upstream")
    except Exception:
        pass
    else:
        raise AssertionError("expected connection failure")
    assert calls == 1


def test_openai_agents_supports_bearer_without_placeholder_api_key() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        assert request.headers["authorization"] == "Bearer user-token"
        return httpx.Response(200, json={"data": []})

    client = Everstack(base_url="https://example.test", access_token="user-token")
    client._transport._client = httpx.Client(
        base_url="https://example.test",
        headers=client._transport._client.headers,
        transport=httpx.MockTransport(handler),
    )
    assert client.openai_agents.connections.list() == []


def test_openai_agents_capabilities_and_public_exports() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        assert request.method == "GET"
        assert request.url.path == "/v1/openai-agents/capabilities"
        return httpx.Response(
            200,
            json={
                "enabled": True,
                "provider": "openai_agents",
                "environment_modes": ["none"],
                "tool_types": [],
                "file_operations": [],
                "readiness": "ready",
                "can_manage_connections": False,
                "can_run_sessions": True,
                "can_resolve_approvals": False,
            },
        )

    client = Everstack(api_key="pk", base_url="https://example.test")
    client._transport._client = httpx.Client(
        base_url="https://example.test", transport=httpx.MockTransport(handler)
    )
    capabilities = client.openai_agents.capabilities()
    assert capabilities["environment_modes"] == ["none"]
    assert "executor_version" not in capabilities
    assert OpenAIAgents.__name__ == "OpenAIAgents"
    assert AsyncOpenAIAgents.__name__ == "AsyncOpenAIAgents"
    assert OpenAIAgentCapabilities.__name__ == "OpenAIAgentCapabilities"
    assert OpenAIAgentArtifact.__name__ == "OpenAIAgentArtifact"
    assert OpenAIAgentApproval.__name__ == "OpenAIAgentApproval"
    assert OpenAIAgentUsage.__name__ == "OpenAIAgentUsage"


async def test_async_openai_agents_capabilities() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        assert request.method == "GET"
        assert request.url.path == "/v1/openai-agents/capabilities"
        return httpx.Response(
            200,
            json={
                "enabled": True,
                "provider": "openai_agents",
                "environment_modes": ["none", "self_hosted"],
                "tool_types": ["function", "mcp"],
                "file_operations": ["upload", "download"],
                "readiness": "ready",
                "can_manage_connections": True,
                "can_run_sessions": True,
                "can_resolve_approvals": True,
            },
        )

    client = AsyncEverstack(api_key="pk", base_url="https://example.test")
    await client._transport._client.aclose()
    client._transport._client = httpx.AsyncClient(
        base_url="https://example.test", transport=httpx.MockTransport(handler)
    )
    try:
        capabilities = await client.openai_agents.capabilities()
        assert capabilities["file_operations"] == ["upload", "download"]
        assert "executor_version" not in capabilities
    finally:
        await client._transport._client.aclose()


def test_openai_agents_final_tool_artifact_approval_and_usage_routes() -> None:
    seen = []

    def handler(request: httpx.Request) -> httpx.Response:
        seen.append((request.method, request.url.path))
        if request.url.path.endswith("/approvals"):
            return httpx.Response(200, json={"data": []})
        if request.url.path.endswith("/download"):
            return httpx.Response(200, json={"object_id": "o1", "url": "https://download.test/a"})
        if request.url.path.endswith("/usage"):
            return httpx.Response(200, json={"status": "unavailable", "source": "openai"})
        if request.method == "DELETE":
            return httpx.Response(200, json={"deleted": True})
        return httpx.Response(200, json={"object_id": "o1", "resolved": True})

    client = Everstack(api_key="pk", base_url="https://example.test")
    client._transport._client = httpx.Client(
        base_url="https://example.test", transport=httpx.MockTransport(handler)
    )
    client.openai_agents.approvals.list()
    client.openai_agents.approvals.resolve("a1", decision="approved")
    client.openai_agents.sessions.upload_artifact("s1", filename="a", content_base64="YQ==")
    client.openai_agents.sessions.export_artifact("s1", path="/tmp/a")
    assert client.openai_agents.sessions.download_artifact("s1", "o1")["object_id"] == "o1"
    client.openai_agents.sessions.delete_artifact("s1", "o1")
    assert client.openai_agents.sessions.usage("s1")["status"] == "unavailable"
    assert ("POST", "/v1/openai-agents/sessions/s1/inputs") in seen
    assert ("GET", "/v1/openai-agents/sessions/s1/artifacts/o1/download") in seen
