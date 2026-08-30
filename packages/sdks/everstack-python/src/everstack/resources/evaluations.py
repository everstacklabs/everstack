"""Evaluations resource."""

from __future__ import annotations

from typing import Any, Dict

from .._transport import Transport, AsyncTransport


class _Runs:
    def __init__(self, transport: Transport) -> None:
        self._t = transport

    def create(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("POST", "/v1/eval-runs", json_body=kwargs)

    def get(self, run_id: str) -> Dict[str, Any]:
        return self._t.request("GET", f"/v1/eval-runs/{run_id}")

    def list(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("GET", "/v1/eval-runs", params=kwargs)

    def cancel(self, run_id: str) -> Dict[str, Any]:
        return self._t.request("POST", f"/v1/eval-runs/{run_id}/cancel", json_body={})

    def delete(self, run_id: str) -> Dict[str, Any]:
        return self._t.request("DELETE", f"/v1/eval-runs/{run_id}")

    def retry(self, run_id: str) -> Dict[str, Any]:
        return self._t.request("POST", f"/v1/eval-runs/{run_id}/retry", json_body={})

    def get_items(self, eval_run_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("GET", f"/v1/eval-runs/{eval_run_id}/items", params=kwargs)

    def get_summary(self, run_id: str) -> Dict[str, Any]:
        return self._t.request("GET", f"/v1/eval-runs/{run_id}/summary")

    def compare(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("POST", "/v1/eval-runs/compare", json_body=kwargs)

    def set_baseline(self, eval_run_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request(
            "POST", f"/v1/eval-runs/{eval_run_id}/baseline", json_body=kwargs
        )


class _Schedules:
    def __init__(self, transport: Transport) -> None:
        self._t = transport

    def create(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("POST", "/v1/eval-schedules", json_body=kwargs)

    def get(self, schedule_id: str) -> Dict[str, Any]:
        return self._t.request("GET", f"/v1/eval-schedules/{schedule_id}")

    def list(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("GET", "/v1/eval-schedules", params=kwargs)

    def update(self, schedule_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request(
            "PATCH", f"/v1/eval-schedules/{schedule_id}", json_body=kwargs
        )

    def delete(self, schedule_id: str) -> Dict[str, Any]:
        return self._t.request("DELETE", f"/v1/eval-schedules/{schedule_id}")


class Evaluations:
    """Sync evaluations resource."""

    runs: _Runs
    schedules: _Schedules

    def __init__(self, transport: Transport) -> None:
        self._t = transport
        self.runs = _Runs(transport)
        self.schedules = _Schedules(transport)

    def score(
        self,
        *,
        input: Any = None,
        output: Any = None,
        expected_output: Any = None,
        metadata: Any = None,
        scorer_config_ids: Any = None,
    ) -> Dict[str, Any]:
        """Synchronously score an output against one or more score configs
        (POST /v1/score-output). Powers the pytest-native ``everstack.evals``
        harness."""
        body = {
            "input": input,
            "output": output,
            "expectedOutput": expected_output,
            "metadata": metadata,
            "scorerConfigIds": scorer_config_ids or [],
        }
        body = {k: v for k, v in body.items() if v is not None}
        return self._t.request("POST", "/v1/score-output", json_body=body)


class AsyncEvaluations:
    """Async evaluations resource."""

    def __init__(self, transport: AsyncTransport) -> None:
        self._t = transport

    async def create_run(self, **kwargs: Any) -> Dict[str, Any]:
        return await self._t.request("POST", "/v1/eval-runs", json_body=kwargs)

    async def get_run(self, run_id: str) -> Dict[str, Any]:
        return await self._t.request("GET", f"/v1/eval-runs/{run_id}")

    async def list_runs(self, **kwargs: Any) -> Dict[str, Any]:
        return await self._t.request("GET", "/v1/eval-runs", params=kwargs)

    async def cancel_run(self, run_id: str) -> Dict[str, Any]:
        return await self._t.request("POST", f"/v1/eval-runs/{run_id}/cancel", json_body={})

    async def get_run_summary(self, run_id: str) -> Dict[str, Any]:
        return await self._t.request("GET", f"/v1/eval-runs/{run_id}/summary")
