"""Agents resource — full agent lifecycle, sessions, sandboxes, deployments, etc.

This resource wraps the REST API directly, forwarding request/response dicts
to keep it thin and maintenance-free as new fields are added to the proto.
"""

from __future__ import annotations

from typing import Any, Dict, Iterator, Optional


from .._transport import Transport, AsyncTransport


# ---------------------------------------------------------------------------
# Sub-resources
# ---------------------------------------------------------------------------

class _Sessions:
    def __init__(self, transport: Transport) -> None:
        self._t = transport

    def create(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("POST", "/v1/agents/sessions", json_body=kwargs)

    def get(self, session_id: str) -> Dict[str, Any]:
        return self._t.request("GET", f"/v1/agents/sessions/{session_id}")

    def list(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("GET", "/v1/agents/sessions", params=kwargs)

    def run_turn(self, session_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request(
            "POST", f"/v1/agents/sessions/{session_id}/turns", json_body=kwargs
        )

    def run_turn_stream(self, session_id: str, **kwargs: Any) -> Iterator[Dict[str, Any]]:
        return self._t.stream(
            "POST", f"/v1/agents/sessions/{session_id}/turns/stream", json_body=kwargs
        )

    def steer(self, session_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request(
            "POST", f"/v1/agents/sessions/{session_id}/steer", json_body=kwargs
        )

    def cancel(self, session_id: str) -> Dict[str, Any]:
        return self._t.request("POST", f"/v1/agents/sessions/{session_id}/cancel", json_body={})

    def complete(self, session_id: str) -> Dict[str, Any]:
        return self._t.request(
            "POST", f"/v1/agents/sessions/{session_id}/complete", json_body={}
        )


class _Reviews:
    def __init__(self, transport: Transport) -> None:
        self._t = transport

    def submit(self, review_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request(
            "POST", f"/v1/agents/reviews/{review_id}/submit", json_body=kwargs
        )

    def get(self, review_id: str) -> Dict[str, Any]:
        return self._t.request("GET", f"/v1/agents/reviews/{review_id}")

    def list(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("GET", "/v1/agents/reviews", params=kwargs)


class _Sandboxes:
    def __init__(self, transport: Transport) -> None:
        self._t = transport

    def create(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("POST", "/v1/sandbox", json_body=kwargs)

    def get_overview(self) -> Dict[str, Any]:
        return self._t.request("GET", "/v1/sandbox/overview")

    def list_instances(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("GET", "/v1/sandbox/instances", params=kwargs)

    def get_instance(self, sandbox_id: str) -> Dict[str, Any]:
        return self._t.request("GET", f"/v1/sandbox/instances/{sandbox_id}")

    def destroy(self, session_id: str) -> Dict[str, Any]:
        return self._t.request("DELETE", f"/v1/sandbox/{session_id}")

    def recreate(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("POST", "/v1/sandbox/recreate", json_body=kwargs)

    def get_stats(self, session_id: str) -> Dict[str, Any]:
        return self._t.request("GET", f"/v1/sandbox/{session_id}/stats")

    def list_executions(self, sandbox_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("GET", f"/v1/sandbox/{sandbox_id}/executions", params=kwargs)

    def list_events(self, sandbox_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("GET", f"/v1/sandbox/{sandbox_id}/events", params=kwargs)

    def stop(self, sandbox_id: str) -> Dict[str, Any]:
        return self._t.request("POST", f"/v1/sandbox/{sandbox_id}/stop", json_body={})

    def revive(self, sandbox_id: str) -> Dict[str, Any]:
        return self._t.request("POST", f"/v1/sandbox/{sandbox_id}/revive", json_body={})

    def terminate(self, sandbox_id: str) -> Dict[str, Any]:
        return self._t.request("POST", f"/v1/sandbox/{sandbox_id}/terminate", json_body={})

    def list_templates(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("GET", "/v1/sandbox/templates", params=kwargs)

    def get_template(self, template_id: str) -> Dict[str, Any]:
        return self._t.request("GET", f"/v1/sandbox/templates/{template_id}")

    def expose_port(self, session_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request(
            "POST", f"/v1/sandbox/{session_id}/ports", json_body=kwargs
        )

    def unexpose_port(self, session_id: str, port: int) -> Dict[str, Any]:
        return self._t.request("DELETE", f"/v1/sandbox/{session_id}/ports/{port}")

    def list_exposed_ports(self, session_id: str) -> Dict[str, Any]:
        return self._t.request("GET", f"/v1/sandbox/{session_id}/ports")

    def detect_listening_ports(self, session_id: str) -> Dict[str, Any]:
        return self._t.request("GET", f"/v1/sandbox/{session_id}/ports/detect")


class _Lifecycle:
    def __init__(self, transport: Transport) -> None:
        self._t = transport

    def provision(self, agent_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request(
            "POST", f"/v1/agents/{agent_id}/provision", json_body=kwargs
        )

    def sleep(self, agent_id: str) -> Dict[str, Any]:
        return self._t.request("POST", f"/v1/agents/{agent_id}/sleep", json_body={})

    def wake(self, agent_id: str) -> Dict[str, Any]:
        return self._t.request("POST", f"/v1/agents/{agent_id}/wake", json_body={})


class _Memories:
    def __init__(self, transport: Transport) -> None:
        self._t = transport

    def setup(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("POST", "/v1/agents/memory/setup", json_body=kwargs)

    def list(self, agent_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("GET", f"/v1/agents/{agent_id}/memories", params=kwargs)

    def create(self, agent_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request(
            "POST", f"/v1/agents/{agent_id}/memories", json_body=kwargs
        )

    def update(self, memory_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request(
            "PATCH", f"/v1/agents/memories/{memory_id}", json_body=kwargs
        )

    def deactivate(self, memory_id: str) -> Dict[str, Any]:
        return self._t.request(
            "POST", f"/v1/agents/memories/{memory_id}/deactivate", json_body={}
        )

    def delete(self, memory_id: str) -> Dict[str, Any]:
        return self._t.request("DELETE", f"/v1/agents/memories/{memory_id}")


class _Deployments:
    def __init__(self, transport: Transport) -> None:
        self._t = transport

    def create(self, agent_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request(
            "POST", f"/v1/agents/{agent_id}/deploy", json_body=kwargs
        )

    def list(self, agent_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("GET", f"/v1/agents/{agent_id}/deployments", params=kwargs)

    def get(self, deployment_id: str) -> Dict[str, Any]:
        return self._t.request("GET", f"/v1/deployments/{deployment_id}")

    def update(self, deployment_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request(
            "PATCH", f"/v1/deployments/{deployment_id}", json_body=kwargs
        )

    def create_key(self, deployment_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request(
            "POST", f"/v1/deployments/{deployment_id}/keys", json_body=kwargs
        )

    def list_keys(self, deployment_id: str) -> Dict[str, Any]:
        return self._t.request("GET", f"/v1/deployments/{deployment_id}/keys")

    def revoke_key(self, key_id: str) -> Dict[str, Any]:
        return self._t.request("DELETE", f"/v1/deployments/keys/{key_id}")

    def list_invocations(self, deployment_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request(
            "GET", f"/v1/deployments/{deployment_id}/invocations", params=kwargs
        )


class _Triggers:
    def __init__(self, transport: Transport) -> None:
        self._t = transport

    def create(self, agent_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request(
            "POST", f"/v1/agents/{agent_id}/triggers", json_body=kwargs
        )

    def list(self, agent_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("GET", f"/v1/agents/{agent_id}/triggers", params=kwargs)

    def get(self, trigger_id: str) -> Dict[str, Any]:
        return self._t.request("GET", f"/v1/agent-triggers/{trigger_id}")

    def update(self, trigger_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request(
            "PATCH", f"/v1/agent-triggers/{trigger_id}", json_body=kwargs
        )

    def delete(self, trigger_id: str) -> Dict[str, Any]:
        return self._t.request("DELETE", f"/v1/agent-triggers/{trigger_id}")

    def test(self, trigger_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request(
            "POST", f"/v1/agent-triggers/{trigger_id}/test", json_body=kwargs
        )

    def list_executions(self, trigger_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request(
            "GET", f"/v1/agent-triggers/{trigger_id}/executions", params=kwargs
        )


class _Links:
    def __init__(self, transport: Transport) -> None:
        self._t = transport

    def create(self, source_agent_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request(
            "POST", f"/v1/agents/{source_agent_id}/links", json_body=kwargs
        )

    def list(self, agent_id: str) -> Dict[str, Any]:
        return self._t.request("GET", f"/v1/agents/{agent_id}/links")

    def delete(self, link_id: str) -> Dict[str, Any]:
        return self._t.request("DELETE", f"/v1/agent-links/{link_id}")


class _Channels:
    def __init__(self, transport: Transport) -> None:
        self._t = transport

    def bind(self, agent_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request(
            "POST", f"/v1/agents/{agent_id}/channels", json_body=kwargs
        )

    def unbind(self, agent_id: str, channel_config_id: str) -> Dict[str, Any]:
        return self._t.request(
            "DELETE", f"/v1/agents/{agent_id}/channels/{channel_config_id}"
        )

    def list(self, agent_id: str) -> Dict[str, Any]:
        return self._t.request("GET", f"/v1/agents/{agent_id}/channels")


class _Crons:
    def __init__(self, transport: Transport) -> None:
        self._t = transport

    def create(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("POST", "/v1/sandbox/crons", json_body=kwargs)

    def update(self, cron_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("PATCH", f"/v1/sandbox/crons/{cron_id}", json_body=kwargs)

    def delete(self, cron_id: str) -> Dict[str, Any]:
        return self._t.request("DELETE", f"/v1/sandbox/crons/{cron_id}")

    def list(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("GET", "/v1/sandbox/crons", params=kwargs)


class _Webhooks:
    def __init__(self, transport: Transport) -> None:
        self._t = transport

    def create(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("POST", "/v1/sandbox/webhooks", json_body=kwargs)

    def delete(self, webhook_id: str) -> Dict[str, Any]:
        return self._t.request("DELETE", f"/v1/sandbox/webhooks/{webhook_id}")

    def list(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("GET", "/v1/sandbox/webhooks", params=kwargs)


class _GitHub:
    def __init__(self, transport: Transport) -> None:
        self._t = transport

    def list_installations(self) -> Dict[str, Any]:
        return self._t.request("GET", "/v1/integrations/github/installations")

    def remove_installation(self, installation_id: str) -> Dict[str, Any]:
        return self._t.request(
            "DELETE", f"/v1/integrations/github/installations/{installation_id}"
        )

    def link_installation(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request(
            "POST", "/v1/integrations/github/installations/link", json_body=kwargs
        )

    def list_repositories(self, installation_id: str) -> Dict[str, Any]:
        return self._t.request(
            "GET", f"/v1/integrations/github/installations/{installation_id}/repos"
        )

    def list_branches(
        self, installation_id: str, owner: str, repo: str
    ) -> Dict[str, Any]:
        return self._t.request(
            "GET",
            f"/v1/integrations/github/installations/{installation_id}/repos/{owner}/{repo}/branches",
        )


class _SSHKeys:
    def __init__(self, transport: Transport) -> None:
        self._t = transport

    def add(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("POST", "/v1/settings/ssh-keys", json_body=kwargs)

    def list(self) -> Dict[str, Any]:
        return self._t.request("GET", "/v1/settings/ssh-keys")

    def delete(self, key_id: str) -> Dict[str, Any]:
        return self._t.request("DELETE", f"/v1/settings/ssh-keys/{key_id}")

    def grant_sandbox_access(self, sandbox_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request(
            "POST", f"/v1/sandbox/{sandbox_id}/ssh/access", json_body=kwargs
        )

    def revoke_sandbox_access(self, sandbox_id: str, user_id: str) -> Dict[str, Any]:
        return self._t.request(
            "DELETE", f"/v1/sandbox/{sandbox_id}/ssh/access/{user_id}"
        )

    def get_sandbox_info(self, sandbox_id: str) -> Dict[str, Any]:
        return self._t.request("GET", f"/v1/sandbox/{sandbox_id}/ssh/info")


# ---------------------------------------------------------------------------
# Main agents resource
# ---------------------------------------------------------------------------

class Agents:
    """Sync agents resource."""

    sessions: _Sessions
    reviews: _Reviews
    sandboxes: _Sandboxes
    lifecycle: _Lifecycle
    memories: _Memories
    deployments: _Deployments
    triggers: _Triggers
    links: _Links
    channels: _Channels
    crons: _Crons
    webhooks: _Webhooks
    github: _GitHub
    ssh_keys: _SSHKeys

    def __init__(self, transport: Transport) -> None:
        self._t = transport
        self.sessions = _Sessions(transport)
        self.reviews = _Reviews(transport)
        self.sandboxes = _Sandboxes(transport)
        self.lifecycle = _Lifecycle(transport)
        self.memories = _Memories(transport)
        self.deployments = _Deployments(transport)
        self.triggers = _Triggers(transport)
        self.links = _Links(transport)
        self.channels = _Channels(transport)
        self.crons = _Crons(transport)
        self.webhooks = _Webhooks(transport)
        self.github = _GitHub(transport)
        self.ssh_keys = _SSHKeys(transport)

    def create(self, **kwargs: Any) -> Dict[str, Any]:
        """Create an agent."""
        return self._t.request("POST", "/v1/agents", json_body=kwargs)

    def get(self, agent_id: str) -> Dict[str, Any]:
        """Get an agent by ID."""
        return self._t.request("GET", f"/v1/agents/{agent_id}")

    def list(self, **kwargs: Any) -> Dict[str, Any]:
        """List agents."""
        return self._t.request("GET", "/v1/agents", params=kwargs)

    def update(self, agent_id: str, **kwargs: Any) -> Dict[str, Any]:
        """Update an agent."""
        return self._t.request("PATCH", f"/v1/agents/{agent_id}", json_body=kwargs)

    def delete(self, agent_id: str) -> Dict[str, Any]:
        """Delete an agent."""
        return self._t.request("DELETE", f"/v1/agents/{agent_id}")

    def import_from_opencode(self, **kwargs: Any) -> Dict[str, Any]:
        """Import an agent from Opencode format."""
        return self._t.request("POST", "/v1/agents/import/opencode", json_body=kwargs)

    def export_to_opencode(self, agent_id: str) -> Dict[str, Any]:
        """Export an agent to Opencode format."""
        return self._t.request("GET", f"/v1/agents/{agent_id}/export/opencode")


# ---------------------------------------------------------------------------
# Async variant (thin wrapper — same pattern, async transport)
# ---------------------------------------------------------------------------

class AsyncAgents:
    """Async agents resource.

    Mirrors the sync API. Sub-resources return raw dicts (same as sync).
    """

    def __init__(self, transport: AsyncTransport) -> None:
        self._t = transport
        # For the async variant we keep it simple: direct methods only.
        # Sub-resource namespaces can be added later as needed.

    async def create(self, **kwargs: Any) -> Dict[str, Any]:
        return await self._t.request("POST", "/v1/agents", json_body=kwargs)

    async def get(self, agent_id: str) -> Dict[str, Any]:
        return await self._t.request("GET", f"/v1/agents/{agent_id}")

    async def list(self, **kwargs: Any) -> Dict[str, Any]:
        return await self._t.request("GET", "/v1/agents", params=kwargs)

    async def update(self, agent_id: str, **kwargs: Any) -> Dict[str, Any]:
        return await self._t.request("PATCH", f"/v1/agents/{agent_id}", json_body=kwargs)

    async def delete(self, agent_id: str) -> Dict[str, Any]:
        return await self._t.request("DELETE", f"/v1/agents/{agent_id}")

    async def create_session(self, **kwargs: Any) -> Dict[str, Any]:
        return await self._t.request("POST", "/v1/agents/sessions", json_body=kwargs)

    async def run_turn(self, session_id: str, **kwargs: Any) -> Dict[str, Any]:
        return await self._t.request(
            "POST", f"/v1/agents/sessions/{session_id}/turns", json_body=kwargs
        )

    async def provision(self, agent_id: str, **kwargs: Any) -> Dict[str, Any]:
        return await self._t.request(
            "POST", f"/v1/agents/{agent_id}/provision", json_body=kwargs
        )

    async def sleep(self, agent_id: str) -> Dict[str, Any]:
        return await self._t.request(
            "POST", f"/v1/agents/{agent_id}/sleep", json_body={}
        )

    async def wake(self, agent_id: str) -> Dict[str, Any]:
        return await self._t.request(
            "POST", f"/v1/agents/{agent_id}/wake", json_body={}
        )
