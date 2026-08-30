"""Observability resource — metrics, sessions, users, outcomes."""

from __future__ import annotations

from typing import Any, Dict

from .._transport import Transport, AsyncTransport


class _Metrics:
    def __init__(self, transport: Transport) -> None:
        self._t = transport

    def get_dashboard(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("POST", "/v1/observability/metrics/dashboard", json_body=kwargs)

    def get_time_series(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("POST", "/v1/observability/metrics/timeseries", json_body=kwargs)


class _Sessions:
    def __init__(self, transport: Transport) -> None:
        self._t = transport

    def list(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("POST", "/v1/observability/sessions", json_body=kwargs)

    def get(self, session_id: str) -> Dict[str, Any]:
        return self._t.request("GET", f"/v1/observability/sessions/{session_id}")


class _Users:
    def __init__(self, transport: Transport) -> None:
        self._t = transport

    def list(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("POST", "/v1/observability/users", json_body=kwargs)

    def get(self, user_id: str) -> Dict[str, Any]:
        return self._t.request("GET", f"/v1/observability/users/{user_id}")


class _Outcomes:
    def __init__(self, transport: Transport) -> None:
        self._t = transport

    def get_dashboard(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("POST", "/v1/observability/outcomes/dashboard", json_body=kwargs)

    def get_time_series(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("POST", "/v1/observability/outcomes/timeseries", json_body=kwargs)


class Observability:
    """Sync observability resource."""

    metrics: _Metrics
    sessions: _Sessions
    users: _Users
    outcomes: _Outcomes

    def __init__(self, transport: Transport) -> None:
        self.metrics = _Metrics(transport)
        self.sessions = _Sessions(transport)
        self.users = _Users(transport)
        self.outcomes = _Outcomes(transport)


class AsyncObservability:
    """Async observability resource."""

    def __init__(self, transport: AsyncTransport) -> None:
        self._t = transport

    async def get_metrics_dashboard(self, **kwargs: Any) -> Dict[str, Any]:
        return await self._t.request(
            "POST", "/v1/observability/metrics/dashboard", json_body=kwargs
        )

    async def get_metrics_time_series(self, **kwargs: Any) -> Dict[str, Any]:
        return await self._t.request(
            "POST", "/v1/observability/metrics/timeseries", json_body=kwargs
        )

    async def list_sessions(self, **kwargs: Any) -> Dict[str, Any]:
        return await self._t.request("POST", "/v1/observability/sessions", json_body=kwargs)

    async def get_session(self, session_id: str) -> Dict[str, Any]:
        return await self._t.request("GET", f"/v1/observability/sessions/{session_id}")

    async def list_users(self, **kwargs: Any) -> Dict[str, Any]:
        return await self._t.request("POST", "/v1/observability/users", json_body=kwargs)

    async def get_user(self, user_id: str) -> Dict[str, Any]:
        return await self._t.request("GET", f"/v1/observability/users/{user_id}")

    async def get_outcome_dashboard(self, **kwargs: Any) -> Dict[str, Any]:
        return await self._t.request(
            "POST", "/v1/observability/outcomes/dashboard", json_body=kwargs
        )

    async def get_outcome_time_series(self, **kwargs: Any) -> Dict[str, Any]:
        return await self._t.request(
            "POST", "/v1/observability/outcomes/timeseries", json_body=kwargs
        )
