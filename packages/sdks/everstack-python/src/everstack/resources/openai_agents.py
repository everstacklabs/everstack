"""Typed resources for OpenAI-managed agent sessions."""

from __future__ import annotations

from typing import Any, Dict, List, Optional, TypedDict, cast
from urllib.parse import quote

from .._transport import AsyncTransport, Transport

ROOT = "/v1/openai-agents"


class OpenAIAgentConnection(TypedDict, total=False):
    id: str
    name: str
    provider: str
    project_id: str
    organization_id: str
    principal_id: str
    allowed_models: List[str]
    status: str
    last_error: str


class OpenAIAgentSession(TypedDict, total=False):
    id: str
    provider: str
    connection_id: str
    upstream_session_id: str
    upstream_environment_id: str
    sandbox_id: str
    model: str
    environment: Dict[str, Any]
    status: str
    readiness: str
    cleanup_status: str
    last_error: str
    created_at: str
    updated_at: str


class OpenAIAgentEvent(TypedDict):
    cursor: int
    event_id: str
    type: str
    version: str
    payload: Dict[str, Any]
    created_at: str


class OpenAIAgentEventPage(TypedDict):
    data: List[OpenAIAgentEvent]
    next_cursor: int
    has_more: bool


class OpenAIAgentArtifact(TypedDict, total=False):
    object_id: str
    filename: str
    content_type: str
    size_bytes: int
    sha256: str
    kind: str
    created_at: str
    url: str
    expires_at: str


class OpenAIAgentApproval(TypedDict):
    id: str
    session_id: str
    turn_id: str
    call_id: str
    tool_name: str
    status: str
    requested_by: str
    arguments: Dict[str, Any]
    requested_at: str
    expires_at: str


class OpenAIAgentUsage(TypedDict, total=False):
    status: str
    input_tokens: int
    output_tokens: int
    total_tokens: int
    updated_at: str
    source: str


class OpenAIAgentCapabilities(TypedDict, total=False):
    enabled: bool
    provider: str
    environment_modes: List[str]
    tool_types: List[str]
    file_operations: List[str]
    executor_version: str
    readiness: str
    can_manage_connections: bool
    can_run_sessions: bool
    can_resolve_approvals: bool


def _id(value: str) -> str:
    if not value or value in (".", ".."):
        raise ValueError("A resource ID is required")
    return quote(value, safe="")


def _body(values: Dict[str, Any]) -> Dict[str, Any]:
    return {key: value for key, value in values.items() if key != "self" and value is not None}


class OpenAIAgentConnections:
    def __init__(self, transport: Transport) -> None:
        self._t = transport

    def list(self) -> List[OpenAIAgentConnection]:
        page = cast(
            Dict[str, List[OpenAIAgentConnection]],
            self._t.request("GET", f"{ROOT}/connections"),
        )
        return page["data"]

    def get(self, connection_id: str) -> OpenAIAgentConnection:
        return cast(
            OpenAIAgentConnection,
            self._t.request("GET", f"{ROOT}/connections/{_id(connection_id)}"),
        )

    def create(
        self,
        *,
        name: str,
        api_key: str,
        executor_key: Optional[str] = None,
        project_id: Optional[str] = None,
        organization_id: Optional[str] = None,
        principal_id: Optional[str] = None,
        allowed_models: Optional[List[str]] = None,
    ) -> OpenAIAgentConnection:
        return cast(
            OpenAIAgentConnection,
            self._t.request("POST", f"{ROOT}/connections", json_body=_body(locals())),
        )

    def update(self, connection_id: str, **changes: Any) -> OpenAIAgentConnection:
        path = f"{ROOT}/connections/{_id(connection_id)}"
        return cast(OpenAIAgentConnection, self._t.request("PATCH", path, json_body=changes))

    def delete(self, connection_id: str) -> None:
        self._t.request("DELETE", f"{ROOT}/connections/{_id(connection_id)}")


class OpenAIAgentSessions:
    def __init__(self, transport: Transport) -> None:
        self._t = transport

    def list(self) -> List[OpenAIAgentSession]:
        page = cast(Dict[str, List[OpenAIAgentSession]], self._t.request("GET", f"{ROOT}/sessions"))
        return page["data"]

    def get(self, session_id: str) -> OpenAIAgentSession:
        path = f"{ROOT}/sessions/{_id(session_id)}"
        return cast(OpenAIAgentSession, self._t.request("GET", path))

    def create(
        self,
        *,
        connection_id: str,
        idempotency_key: str,
        model: str,
        environment: Dict[str, Any],
        instructions: Optional[str] = None,
        input: Optional[str] = None,
        tools: Optional[List[Dict[str, str]]] = None,
    ) -> OpenAIAgentSession:
        return cast(
            OpenAIAgentSession,
            self._t.request("POST", f"{ROOT}/sessions", json_body=_body(locals())),
        )

    def input(self, session_id: str, *, idempotency_key: str, input: str) -> Dict[str, bool]:
        body = {"idempotency_key": idempotency_key, "input": input}
        path = f"{ROOT}/sessions/{_id(session_id)}/input"
        return cast(Dict[str, bool], self._t.request("POST", path, json_body=body))

    def cancel(self, session_id: str) -> Dict[str, bool]:
        path = f"{ROOT}/sessions/{_id(session_id)}/cancel"
        return cast(Dict[str, bool], self._t.request("POST", path))

    def delete(self, session_id: str) -> Dict[str, bool]:
        path = f"{ROOT}/sessions/{_id(session_id)}"
        return cast(Dict[str, bool], self._t.request("DELETE", path))

    def events(
        self, session_id: str, *, after: Optional[int] = None, limit: Optional[int] = None
    ) -> OpenAIAgentEventPage:
        params = {"after": after, "limit": limit}
        path = f"{ROOT}/sessions/{_id(session_id)}/events"
        return cast(OpenAIAgentEventPage, self._t.request("GET", path, params=params))

    def artifacts(self, session_id: str) -> List[OpenAIAgentArtifact]:
        path = f"{ROOT}/sessions/{_id(session_id)}/artifacts"
        page = cast(Dict[str, List[OpenAIAgentArtifact]], self._t.request("GET", path))
        return page["data"]

    def upload_artifact(self, session_id: str, **request: Any) -> OpenAIAgentArtifact:
        path = f"{ROOT}/sessions/{_id(session_id)}/inputs"
        return cast(OpenAIAgentArtifact, self._t.request("POST", path, json_body=request))

    def export_artifact(self, session_id: str, *, path: str) -> OpenAIAgentArtifact:
        endpoint = f"{ROOT}/sessions/{_id(session_id)}/artifacts/export"
        return cast(
            OpenAIAgentArtifact,
            self._t.request("POST", endpoint, json_body={"path": path}),
        )

    def delete_artifact(self, session_id: str, artifact_id: str) -> Dict[str, bool]:
        path = f"{ROOT}/sessions/{_id(session_id)}/artifacts/{_id(artifact_id)}"
        return cast(Dict[str, bool], self._t.request("DELETE", path))

    def usage(self, session_id: str) -> OpenAIAgentUsage:
        path = f"{ROOT}/sessions/{_id(session_id)}/usage"
        return cast(OpenAIAgentUsage, self._t.request("GET", path))

    def download_artifact(self, session_id: str, artifact_id: str) -> OpenAIAgentArtifact:
        path = f"{ROOT}/sessions/{_id(session_id)}/artifacts/{_id(artifact_id)}/download"
        return cast(OpenAIAgentArtifact, self._t.request("GET", path))


class OpenAIAgents:
    """OpenAI Agents resources. Mutations are never retried automatically."""

    def __init__(self, transport: Transport) -> None:
        self._t = transport
        self.connections = OpenAIAgentConnections(transport)
        self.sessions = OpenAIAgentSessions(transport)
        self.approvals = OpenAIAgentApprovals(transport)

    def capabilities(self) -> OpenAIAgentCapabilities:
        return cast(OpenAIAgentCapabilities, self._t.request("GET", f"{ROOT}/capabilities"))


class OpenAIAgentApprovals:
    def __init__(self, transport: Transport) -> None:
        self._t = transport

    def list(self) -> List[OpenAIAgentApproval]:
        page = cast(
            Dict[str, List[OpenAIAgentApproval]],
            self._t.request("GET", f"{ROOT}/approvals"),
        )
        return page["data"]

    def resolve(self, approval_id: str, *, decision: str) -> Dict[str, bool]:
        path = f"{ROOT}/approvals/{_id(approval_id)}/resolve"
        return cast(
            Dict[str, bool],
            self._t.request("POST", path, json_body={"decision": decision}),
        )


class AsyncOpenAIAgentConnections:
    def __init__(self, transport: AsyncTransport) -> None:
        self._t = transport

    async def list(self) -> List[OpenAIAgentConnection]:
        page = cast(
            Dict[str, List[OpenAIAgentConnection]],
            await self._t.request("GET", f"{ROOT}/connections"),
        )
        return page["data"]

    async def get(self, connection_id: str) -> OpenAIAgentConnection:
        path = f"{ROOT}/connections/{_id(connection_id)}"
        return cast(OpenAIAgentConnection, await self._t.request("GET", path))

    async def create(self, **params: Any) -> OpenAIAgentConnection:
        return cast(
            OpenAIAgentConnection,
            await self._t.request("POST", f"{ROOT}/connections", json_body=params),
        )

    async def update(self, connection_id: str, **changes: Any) -> OpenAIAgentConnection:
        path = f"{ROOT}/connections/{_id(connection_id)}"
        return cast(
            OpenAIAgentConnection,
            await self._t.request("PATCH", path, json_body=changes),
        )

    async def delete(self, connection_id: str) -> None:
        await self._t.request("DELETE", f"{ROOT}/connections/{_id(connection_id)}")


class AsyncOpenAIAgentSessions:
    def __init__(self, transport: AsyncTransport) -> None:
        self._t = transport

    async def list(self) -> List[OpenAIAgentSession]:
        page = cast(
            Dict[str, List[OpenAIAgentSession]],
            await self._t.request("GET", f"{ROOT}/sessions"),
        )
        return page["data"]

    async def get(self, session_id: str) -> OpenAIAgentSession:
        path = f"{ROOT}/sessions/{_id(session_id)}"
        return cast(OpenAIAgentSession, await self._t.request("GET", path))

    async def create(self, **params: Any) -> OpenAIAgentSession:
        return cast(
            OpenAIAgentSession,
            await self._t.request("POST", f"{ROOT}/sessions", json_body=params),
        )

    async def input(self, session_id: str, *, idempotency_key: str, input: str) -> Dict[str, bool]:
        body = {"idempotency_key": idempotency_key, "input": input}
        path = f"{ROOT}/sessions/{_id(session_id)}/input"
        return cast(Dict[str, bool], await self._t.request("POST", path, json_body=body))

    async def cancel(self, session_id: str) -> Dict[str, bool]:
        path = f"{ROOT}/sessions/{_id(session_id)}/cancel"
        return cast(Dict[str, bool], await self._t.request("POST", path))

    async def delete(self, session_id: str) -> Dict[str, bool]:
        path = f"{ROOT}/sessions/{_id(session_id)}"
        return cast(Dict[str, bool], await self._t.request("DELETE", path))

    async def events(
        self, session_id: str, *, after: Optional[int] = None, limit: Optional[int] = None
    ) -> OpenAIAgentEventPage:
        params = {"after": after, "limit": limit}
        path = f"{ROOT}/sessions/{_id(session_id)}/events"
        return cast(OpenAIAgentEventPage, await self._t.request("GET", path, params=params))

    async def artifacts(self, session_id: str) -> List[Dict[str, Any]]:
        path = f"{ROOT}/sessions/{_id(session_id)}/artifacts"
        page = cast(Dict[str, List[Dict[str, Any]]], await self._t.request("GET", path))
        return page["data"]

    async def upload_artifact(self, session_id: str, **request: Any) -> OpenAIAgentArtifact:
        path = f"{ROOT}/sessions/{_id(session_id)}/inputs"
        return cast(OpenAIAgentArtifact, await self._t.request("POST", path, json_body=request))

    async def export_artifact(self, session_id: str, *, path: str) -> OpenAIAgentArtifact:
        endpoint = f"{ROOT}/sessions/{_id(session_id)}/artifacts/export"
        return cast(
            OpenAIAgentArtifact,
            await self._t.request("POST", endpoint, json_body={"path": path}),
        )

    async def delete_artifact(self, session_id: str, artifact_id: str) -> Dict[str, bool]:
        path = f"{ROOT}/sessions/{_id(session_id)}/artifacts/{_id(artifact_id)}"
        return cast(Dict[str, bool], await self._t.request("DELETE", path))

    async def usage(self, session_id: str) -> OpenAIAgentUsage:
        path = f"{ROOT}/sessions/{_id(session_id)}/usage"
        return cast(OpenAIAgentUsage, await self._t.request("GET", path))

    async def download_artifact(self, session_id: str, artifact_id: str) -> OpenAIAgentArtifact:
        path = f"{ROOT}/sessions/{_id(session_id)}/artifacts/{_id(artifact_id)}/download"
        return cast(OpenAIAgentArtifact, await self._t.request("GET", path))


class AsyncOpenAIAgents:
    def __init__(self, transport: AsyncTransport) -> None:
        self._t = transport
        self.connections = AsyncOpenAIAgentConnections(transport)
        self.sessions = AsyncOpenAIAgentSessions(transport)
        self.approvals = AsyncOpenAIAgentApprovals(transport)

    async def capabilities(self) -> OpenAIAgentCapabilities:
        return cast(
            OpenAIAgentCapabilities,
            await self._t.request("GET", f"{ROOT}/capabilities"),
        )


class AsyncOpenAIAgentApprovals:
    def __init__(self, transport: AsyncTransport) -> None:
        self._t = transport

    async def list(self) -> List[OpenAIAgentApproval]:
        page = cast(
            Dict[str, List[OpenAIAgentApproval]],
            await self._t.request("GET", f"{ROOT}/approvals"),
        )
        return page["data"]

    async def resolve(self, approval_id: str, *, decision: str) -> Dict[str, bool]:
        path = f"{ROOT}/approvals/{_id(approval_id)}/resolve"
        return cast(
            Dict[str, bool],
            await self._t.request("POST", path, json_body={"decision": decision}),
        )
