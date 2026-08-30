"""Traces resource — list/inspect traces, scores, performance breakdowns.

Uses the REST endpoints exposed by ``TracesService`` (annotated with
``google.api.http``). Each method maps to a single ``POST /v1/traces/...``
call; the request body is a JSON dump of the proto message and the response
is the JSON-encoded proto response.

The streaming ``ListTraces`` endpoint is intentionally not yet exposed here
— it requires server-streaming JSON handling that doesn't compose cleanly
with the sync ``requests`` transport. Use ``list_rich`` for paginated
listing instead.

Example::

    from everstack import Everstack

    client = Everstack(api_key="pk_...")

    # Paginated rich traces
    page = client.traces.list_rich(limit=50, environment="production")
    for t in page["traces"]:
        print(t["id"], t.get("totalCost"))

    # Inspect a specific trace
    tree = client.traces.get_tree(trace_id="abc123")

    # Score a trace from human review
    client.traces.scores.create(
        trace_id="abc123",
        name="quality",
        source="ANNOTATION",
        data_type="NUMERIC",
        numeric_value=0.9,
        comment="Followed the rubric.",
    )
"""

from __future__ import annotations

import json
import time
import uuid
from contextvars import ContextVar
from datetime import datetime, timezone
from functools import wraps
from typing import Any, Callable, Dict, List, Optional, TypeVar

from .._transport import Transport, AsyncTransport

_T = TypeVar("_T")
_current_observation_id: ContextVar[Optional[str]] = ContextVar(
    "everstack_current_trace_observation_id",
    default=None,
)


class TraceSpan:
    """Mutable handle passed to SDK custom span callbacks."""

    def __init__(self, *, observation_id: str, options: Dict[str, Any]) -> None:
        self.id = observation_id
        self.options = options
        self.started_at = time.time_ns()
        self.started_at_iso = _utc_now_iso()

    def set_input(self, value: Any, mime_type: Optional[str] = None) -> None:
        data, detected_mime_type = _serialize_payload(value, mime_type)
        self.options["input_data"] = data
        self.options["input_mime_type"] = detected_mime_type

    def set_output(self, value: Any, mime_type: Optional[str] = None) -> None:
        data, detected_mime_type = _serialize_payload(value, mime_type)
        self.options["output_data"] = data
        self.options["output_mime_type"] = detected_mime_type

    def set_metadata(self, metadata: Dict[str, str]) -> None:
        self.options["metadata"] = {**self.options.get("metadata", {}), **metadata}

    def set_tags(self, tags: List[str]) -> None:
        self.options["tags"] = tags


# Canonical fix-attempt verdict values. Server-side validation rejects any
# value outside this set when the score name is FIX_ATTEMPT_VERDICT_NAME.
FIX_ATTEMPT_VERDICT_NAME = "fix_attempt_verdict"
FixAttemptVerdict = str  # "win" | "fail" | "draw" | "no_change"


class _Scores:
    """Score CRUD on a trace."""

    def __init__(self, transport: Transport) -> None:
        self._t = transport

    def list(self, *, trace_id: str) -> Dict[str, Any]:
        return self._t.request("POST", "/v1/traces/scores/list", json_body={"traceId": trace_id})

    def create(
        self,
        *,
        trace_id: str,
        name: str,
        source: str,
        data_type: str,
        numeric_value: Optional[float] = None,
        string_value: Optional[str] = None,
        boolean_value: Optional[bool] = None,
        comment: Optional[str] = None,
        observation_id: Optional[str] = None,
    ) -> Dict[str, Any]:
        body: Dict[str, Any] = {
            "traceId": trace_id,
            "name": name,
            "source": source,
            "dataType": data_type,
        }
        if numeric_value is not None:
            body["numericValue"] = numeric_value
        if string_value is not None:
            body["stringValue"] = string_value
        if boolean_value is not None:
            body["booleanValue"] = boolean_value
        if comment is not None:
            body["comment"] = comment
        if observation_id is not None:
            body["observationId"] = observation_id
        return self._t.request("POST", "/v1/traces/scores", json_body=body)

    def verdict(
        self,
        *,
        trace_id: str,
        verdict: FixAttemptVerdict,
        source: str = "API",
        observation_id: Optional[str] = None,
        comment: Optional[str] = None,
    ) -> Dict[str, Any]:
        """One-line helper to label a trace with the canonical fix-attempt
        verdict (``win`` | ``fail`` | ``draw`` | ``no_change``). Wraps
        :meth:`create` with the reserved score name and ``CATEGORICAL``
        data type so the server validator accepts it.

        Example::

            # After running your test suite against the agent's output:
            client.traces.scores.verdict(
                trace_id=trace_id,
                verdict="win" if tests_pass else "fail",
                comment="ci-run #4821",
            )
        """
        return self.create(
            trace_id=trace_id,
            name=FIX_ATTEMPT_VERDICT_NAME,
            source=source,
            data_type="CATEGORICAL",
            string_value=verdict,
            observation_id=observation_id,
            comment=comment,
        )

    def delete(self, *, score_id: str, trace_id: str) -> Dict[str, Any]:
        return self._t.request(
            "POST",
            "/v1/traces/scores/delete",
            json_body={"scoreId": score_id, "traceId": trace_id},
        )


class _Performance:
    """Per-trace performance + resource analysis."""

    def __init__(self, transport: Transport) -> None:
        self._t = transport

    def breakdown(
        self,
        *,
        trace_id: str,
        observation_id: Optional[str] = None,
        group_by_node: bool = False,
        group_by_type: bool = False,
    ) -> Dict[str, Any]:
        body: Dict[str, Any] = {"traceId": trace_id}
        if observation_id:
            body["observationId"] = observation_id
        if group_by_node:
            body["groupByNode"] = True
        if group_by_type:
            body["groupByType"] = True
        return self._t.request("POST", "/v1/traces/performance", json_body=body)

    def utilization(
        self,
        *,
        trace_id: str,
        from_time: Optional[str] = None,
        to_time: Optional[str] = None,
        granularity_ms: Optional[int] = None,
    ) -> Dict[str, Any]:
        body: Dict[str, Any] = {"traceId": trace_id}
        if from_time:
            body["fromTime"] = from_time
        if to_time:
            body["toTime"] = to_time
        if granularity_ms:
            body["granularityMs"] = granularity_ms
        return self._t.request("POST", "/v1/traces/resources", json_body=body)


class Traces:
    """Sync traces resource."""

    scores: _Scores
    performance: _Performance

    def __init__(self, transport: Transport) -> None:
        self._t = transport
        self.scores = _Scores(transport)
        self.performance = _Performance(transport)

    def get(self, *, trace_id: str) -> Dict[str, Any]:
        return self._t.request("POST", "/v1/traces/get", json_body={"traceId": trace_id})

    def get_spans(self, *, trace_id: str) -> Dict[str, Any]:
        return self._t.request("POST", "/v1/traces/spans", json_body={"traceId": trace_id})

    def get_tree(self, *, trace_id: str) -> Dict[str, Any]:
        return self._t.request("POST", "/v1/traces/tree", json_body={"traceId": trace_id})

    def get_rich(self, *, trace_id: str) -> Dict[str, Any]:
        return self._t.request("POST", "/v1/traces/rich/get", json_body={"traceId": trace_id})

    def list_rich(
        self,
        *,
        trace_ids: Optional[List[str]] = None,
        session_id: Optional[str] = None,
        user_id: Optional[str] = None,
        thread_id: Optional[str] = None,
        environment: Optional[str] = None,
        tags: Optional[List[str]] = None,
        model: Optional[str] = None,
        provider: Optional[str] = None,
        from_time: Optional[str] = None,
        to_time: Optional[str] = None,
        limit: int = 50,
        offset: int = 0,
    ) -> Dict[str, Any]:
        body: Dict[str, Any] = {"limit": limit, "offset": offset}
        if trace_ids:
            body["traceIds"] = trace_ids
        if session_id:
            body["sessionId"] = session_id
        if user_id:
            body["userId"] = user_id
        if thread_id:
            body["threadId"] = thread_id
        if environment:
            body["environment"] = environment
        if tags:
            body["tags"] = tags
        if model:
            body["model"] = model
        if provider:
            body["provider"] = provider
        if from_time:
            body["fromTime"] = from_time
        if to_time:
            body["toTime"] = to_time
        return self._t.request("POST", "/v1/traces/rich/list", json_body=body)

    def get_analytics(
        self,
        *,
        trace_ids: Optional[List[str]] = None,
        tenant_id: Optional[str] = None,
        from_time: Optional[str] = None,
        to_time: Optional[str] = None,
        limit: int = 50,
        offset: int = 0,
    ) -> Dict[str, Any]:
        body: Dict[str, Any] = {"limit": limit, "offset": offset}
        if trace_ids:
            body["traceIds"] = trace_ids
        if tenant_id:
            body["tenantId"] = tenant_id
        if from_time:
            body["fromTime"] = from_time
        if to_time:
            body["toTime"] = to_time
        return self._t.request("POST", "/v1/traces/analytics", json_body=body)

    def get_overlay(self, *, trace_id: str) -> Dict[str, Any]:
        return self._t.request("POST", "/v1/traces/overlay/get", json_body={"traceId": trace_id})

    def update_overlay(
        self,
        *,
        trace_id: str,
        display_name: Optional[str] = None,
        input_override: Optional[str] = None,
        output_override: Optional[str] = None,
        metadata: Optional[Dict[str, str]] = None,
        tags: Optional[List[str]] = None,
        hidden_span_ids: Optional[List[str]] = None,
    ) -> Dict[str, Any]:
        body: Dict[str, Any] = {"traceId": trace_id}
        if display_name is not None:
            body["displayName"] = display_name
        if input_override is not None:
            body["inputOverride"] = input_override
        if output_override is not None:
            body["outputOverride"] = output_override
        if metadata is not None:
            body["metadata"] = metadata
        if tags is not None:
            body["tags"] = tags
        if hidden_span_ids is not None:
            body["hiddenSpanIds"] = hidden_span_ids
        return self._t.request("POST", "/v1/traces/overlay", json_body=body)

    def create_custom_observation(
        self,
        *,
        trace_id: str,
        name: str,
        type: str = "SPAN",
        parent_observation_id: Optional[str] = None,
        source: str = "API",
        start_time: Optional[str] = None,
        end_time: Optional[str] = None,
        duration: Optional[int] = None,
        level: Optional[str] = None,
        status_message: Optional[str] = None,
        model: Optional[str] = None,
        input_data: Optional[str] = None,
        output_data: Optional[str] = None,
        input_mime_type: Optional[str] = None,
        output_mime_type: Optional[str] = None,
        input_tokens: Optional[int] = None,
        output_tokens: Optional[int] = None,
        total_tokens: Optional[int] = None,
        input_cost: Optional[float] = None,
        output_cost: Optional[float] = None,
        total_cost: Optional[float] = None,
        metadata: Optional[Dict[str, str]] = None,
        tags: Optional[List[str]] = None,
        observation_id: Optional[str] = None,
    ) -> Dict[str, Any]:
        body: Dict[str, Any] = {"traceId": trace_id, "name": name, "type": type, "source": source}
        optional_values = {
            "id": observation_id,
            "parentObservationId": parent_observation_id,
            "startTime": start_time,
            "endTime": end_time,
            "duration": duration,
            "level": level,
            "statusMessage": status_message,
            "model": model,
            "inputData": input_data,
            "outputData": output_data,
            "inputMimeType": input_mime_type,
            "outputMimeType": output_mime_type,
            "inputTokens": input_tokens,
            "outputTokens": output_tokens,
            "totalTokens": total_tokens,
            "inputCost": input_cost,
            "outputCost": output_cost,
            "totalCost": total_cost,
            "metadata": metadata,
            "tags": tags,
        }
        body.update({k: v for k, v in optional_values.items() if v is not None})
        return self._t.request("POST", "/v1/traces/observations/custom", json_body=body)

    def span(self, *, trace_id: str, name: str, **kwargs: Any) -> "_TraceSpanContext":
        return _TraceSpanContext(self, trace_id=trace_id, name=name, **kwargs)

    def capture_exception(
        self,
        error: BaseException,
        *,
        provider: Optional[str] = None,
        model: Optional[str] = None,
        status_code: Optional[int] = None,
        trace_id: Optional[str] = None,
        name: Optional[str] = None,
        context: Optional[Dict[str, str]] = None,
    ) -> Dict[str, Any]:
        """Report a caught exception so it surfaces as an Issue.

        Records an ERROR-level observation; the backend groups recurring
        failures by a normalized signature of ``str(error)`` into a single
        Issue (first/last seen, count, trend, lifecycle). ``provider`` /
        ``model`` / ``status_code`` populate the issue's provider, model and
        category facets; ``context`` adds arbitrary tags.

        Example::

            try:
                openai.chat.completions.create(...)
            except Exception as e:
                client.capture_exception(e, provider="openai", model="gpt-4o")
                raise
        """
        return self.create_custom_observation(
            trace_id=trace_id or uuid.uuid4().hex,
            name=name or type(error).__name__,
            type="EVENT",
            level="ERROR",
            status_message=str(error) or type(error).__name__,
            model=model,
            metadata=_issue_metadata(error, provider, status_code, context),
        )

    def capture_message(
        self,
        message: str,
        *,
        level: str = "ERROR",
        provider: Optional[str] = None,
        model: Optional[str] = None,
        status_code: Optional[int] = None,
        trace_id: Optional[str] = None,
        name: Optional[str] = None,
        context: Optional[Dict[str, str]] = None,
    ) -> Dict[str, Any]:
        """Report a free-form failure message as an Issue.

        Only ``ERROR``-level captures become Issues; lower levels are recorded
        as plain observations on the trace.
        """
        return self.create_custom_observation(
            trace_id=trace_id or uuid.uuid4().hex,
            name=name or "message",
            type="EVENT",
            level=level,
            status_message=message,
            model=model,
            metadata=_issue_metadata(None, provider, status_code, context),
        )

    def list_custom_observations(self, *, trace_id: str) -> Dict[str, Any]:
        return self._t.request("POST", "/v1/traces/observations/custom/list", json_body={"traceId": trace_id})

    def create_annotation(
        self,
        *,
        trace_id: str,
        body: str,
        observation_id: Optional[str] = None,
        metadata: Optional[Dict[str, str]] = None,
    ) -> Dict[str, Any]:
        payload: Dict[str, Any] = {"traceId": trace_id, "body": body}
        if observation_id is not None:
            payload["observationId"] = observation_id
        if metadata is not None:
            payload["metadata"] = metadata
        return self._t.request("POST", "/v1/traces/annotations", json_body=payload)

    def list_annotations(self, *, trace_id: str) -> Dict[str, Any]:
        return self._t.request("POST", "/v1/traces/annotations/list", json_body={"traceId": trace_id})


# ─────────────────────────────────────────────────────────────────────────
# Async mirror — same surface, awaitable.
# ─────────────────────────────────────────────────────────────────────────


class _AsyncScores:
    def __init__(self, transport: AsyncTransport) -> None:
        self._t = transport

    async def list(self, *, trace_id: str) -> Dict[str, Any]:
        return await self._t.request("POST", "/v1/traces/scores/list", json_body={"traceId": trace_id})

    async def create(
        self,
        *,
        trace_id: str,
        name: str,
        source: str,
        data_type: str,
        numeric_value: Optional[float] = None,
        string_value: Optional[str] = None,
        boolean_value: Optional[bool] = None,
        comment: Optional[str] = None,
        observation_id: Optional[str] = None,
    ) -> Dict[str, Any]:
        body: Dict[str, Any] = {
            "traceId": trace_id,
            "name": name,
            "source": source,
            "dataType": data_type,
        }
        if numeric_value is not None:
            body["numericValue"] = numeric_value
        if string_value is not None:
            body["stringValue"] = string_value
        if boolean_value is not None:
            body["booleanValue"] = boolean_value
        if comment is not None:
            body["comment"] = comment
        if observation_id is not None:
            body["observationId"] = observation_id
        return await self._t.request("POST", "/v1/traces/scores", json_body=body)

    async def verdict(
        self,
        *,
        trace_id: str,
        verdict: FixAttemptVerdict,
        source: str = "API",
        observation_id: Optional[str] = None,
        comment: Optional[str] = None,
    ) -> Dict[str, Any]:
        """Async counterpart of :meth:`_Scores.verdict`."""
        return await self.create(
            trace_id=trace_id,
            name=FIX_ATTEMPT_VERDICT_NAME,
            source=source,
            data_type="CATEGORICAL",
            string_value=verdict,
            observation_id=observation_id,
            comment=comment,
        )

    async def delete(self, *, score_id: str, trace_id: str) -> Dict[str, Any]:
        return await self._t.request(
            "POST",
            "/v1/traces/scores/delete",
            json_body={"scoreId": score_id, "traceId": trace_id},
        )


class _AsyncPerformance:
    def __init__(self, transport: AsyncTransport) -> None:
        self._t = transport

    async def breakdown(
        self,
        *,
        trace_id: str,
        observation_id: Optional[str] = None,
        group_by_node: bool = False,
        group_by_type: bool = False,
    ) -> Dict[str, Any]:
        body: Dict[str, Any] = {"traceId": trace_id}
        if observation_id:
            body["observationId"] = observation_id
        if group_by_node:
            body["groupByNode"] = True
        if group_by_type:
            body["groupByType"] = True
        return await self._t.request("POST", "/v1/traces/performance", json_body=body)

    async def utilization(
        self,
        *,
        trace_id: str,
        from_time: Optional[str] = None,
        to_time: Optional[str] = None,
        granularity_ms: Optional[int] = None,
    ) -> Dict[str, Any]:
        body: Dict[str, Any] = {"traceId": trace_id}
        if from_time:
            body["fromTime"] = from_time
        if to_time:
            body["toTime"] = to_time
        if granularity_ms:
            body["granularityMs"] = granularity_ms
        return await self._t.request("POST", "/v1/traces/resources", json_body=body)


class AsyncTraces:
    """Async traces resource."""

    scores: _AsyncScores
    performance: _AsyncPerformance

    def __init__(self, transport: AsyncTransport) -> None:
        self._t = transport
        self.scores = _AsyncScores(transport)
        self.performance = _AsyncPerformance(transport)

    async def get(self, *, trace_id: str) -> Dict[str, Any]:
        return await self._t.request("POST", "/v1/traces/get", json_body={"traceId": trace_id})

    async def get_spans(self, *, trace_id: str) -> Dict[str, Any]:
        return await self._t.request("POST", "/v1/traces/spans", json_body={"traceId": trace_id})

    async def get_tree(self, *, trace_id: str) -> Dict[str, Any]:
        return await self._t.request("POST", "/v1/traces/tree", json_body={"traceId": trace_id})

    async def get_rich(self, *, trace_id: str) -> Dict[str, Any]:
        return await self._t.request("POST", "/v1/traces/rich/get", json_body={"traceId": trace_id})

    async def list_rich(
        self,
        *,
        trace_ids: Optional[List[str]] = None,
        session_id: Optional[str] = None,
        user_id: Optional[str] = None,
        thread_id: Optional[str] = None,
        environment: Optional[str] = None,
        tags: Optional[List[str]] = None,
        model: Optional[str] = None,
        provider: Optional[str] = None,
        from_time: Optional[str] = None,
        to_time: Optional[str] = None,
        limit: int = 50,
        offset: int = 0,
    ) -> Dict[str, Any]:
        body: Dict[str, Any] = {"limit": limit, "offset": offset}
        if trace_ids:
            body["traceIds"] = trace_ids
        if session_id:
            body["sessionId"] = session_id
        if user_id:
            body["userId"] = user_id
        if thread_id:
            body["threadId"] = thread_id
        if environment:
            body["environment"] = environment
        if tags:
            body["tags"] = tags
        if model:
            body["model"] = model
        if provider:
            body["provider"] = provider
        if from_time:
            body["fromTime"] = from_time
        if to_time:
            body["toTime"] = to_time
        return await self._t.request("POST", "/v1/traces/rich/list", json_body=body)

    async def get_analytics(
        self,
        *,
        trace_ids: Optional[List[str]] = None,
        tenant_id: Optional[str] = None,
        from_time: Optional[str] = None,
        to_time: Optional[str] = None,
        limit: int = 50,
        offset: int = 0,
    ) -> Dict[str, Any]:
        body: Dict[str, Any] = {"limit": limit, "offset": offset}
        if trace_ids:
            body["traceIds"] = trace_ids
        if tenant_id:
            body["tenantId"] = tenant_id
        if from_time:
            body["fromTime"] = from_time
        if to_time:
            body["toTime"] = to_time
        return await self._t.request("POST", "/v1/traces/analytics", json_body=body)

    async def get_overlay(self, *, trace_id: str) -> Dict[str, Any]:
        return await self._t.request("POST", "/v1/traces/overlay/get", json_body={"traceId": trace_id})

    async def update_overlay(
        self,
        *,
        trace_id: str,
        display_name: Optional[str] = None,
        input_override: Optional[str] = None,
        output_override: Optional[str] = None,
        metadata: Optional[Dict[str, str]] = None,
        tags: Optional[List[str]] = None,
        hidden_span_ids: Optional[List[str]] = None,
    ) -> Dict[str, Any]:
        body: Dict[str, Any] = {"traceId": trace_id}
        optional_values = {
            "displayName": display_name,
            "inputOverride": input_override,
            "outputOverride": output_override,
            "metadata": metadata,
            "tags": tags,
            "hiddenSpanIds": hidden_span_ids,
        }
        body.update({k: v for k, v in optional_values.items() if v is not None})
        return await self._t.request("POST", "/v1/traces/overlay", json_body=body)

    async def create_custom_observation(
        self,
        *,
        trace_id: str,
        name: str,
        type: str = "SPAN",
        parent_observation_id: Optional[str] = None,
        source: str = "API",
        start_time: Optional[str] = None,
        end_time: Optional[str] = None,
        duration: Optional[int] = None,
        level: Optional[str] = None,
        status_message: Optional[str] = None,
        model: Optional[str] = None,
        input_data: Optional[str] = None,
        output_data: Optional[str] = None,
        input_mime_type: Optional[str] = None,
        output_mime_type: Optional[str] = None,
        input_tokens: Optional[int] = None,
        output_tokens: Optional[int] = None,
        total_tokens: Optional[int] = None,
        input_cost: Optional[float] = None,
        output_cost: Optional[float] = None,
        total_cost: Optional[float] = None,
        metadata: Optional[Dict[str, str]] = None,
        tags: Optional[List[str]] = None,
        observation_id: Optional[str] = None,
    ) -> Dict[str, Any]:
        body: Dict[str, Any] = {"traceId": trace_id, "name": name, "type": type, "source": source}
        optional_values = {
            "id": observation_id,
            "parentObservationId": parent_observation_id,
            "startTime": start_time,
            "endTime": end_time,
            "duration": duration,
            "level": level,
            "statusMessage": status_message,
            "model": model,
            "inputData": input_data,
            "outputData": output_data,
            "inputMimeType": input_mime_type,
            "outputMimeType": output_mime_type,
            "inputTokens": input_tokens,
            "outputTokens": output_tokens,
            "totalTokens": total_tokens,
            "inputCost": input_cost,
            "outputCost": output_cost,
            "totalCost": total_cost,
            "metadata": metadata,
            "tags": tags,
        }
        body.update({k: v for k, v in optional_values.items() if v is not None})
        return await self._t.request("POST", "/v1/traces/observations/custom", json_body=body)

    def span(self, *, trace_id: str, name: str, **kwargs: Any) -> "_AsyncTraceSpanContext":
        return _AsyncTraceSpanContext(self, trace_id=trace_id, name=name, **kwargs)

    async def capture_exception(
        self,
        error: BaseException,
        *,
        provider: Optional[str] = None,
        model: Optional[str] = None,
        status_code: Optional[int] = None,
        trace_id: Optional[str] = None,
        name: Optional[str] = None,
        context: Optional[Dict[str, str]] = None,
    ) -> Dict[str, Any]:
        """Async mirror of :meth:`Traces.capture_exception`."""
        return await self.create_custom_observation(
            trace_id=trace_id or uuid.uuid4().hex,
            name=name or type(error).__name__,
            type="EVENT",
            level="ERROR",
            status_message=str(error) or type(error).__name__,
            model=model,
            metadata=_issue_metadata(error, provider, status_code, context),
        )

    async def capture_message(
        self,
        message: str,
        *,
        level: str = "ERROR",
        provider: Optional[str] = None,
        model: Optional[str] = None,
        status_code: Optional[int] = None,
        trace_id: Optional[str] = None,
        name: Optional[str] = None,
        context: Optional[Dict[str, str]] = None,
    ) -> Dict[str, Any]:
        """Async mirror of :meth:`Traces.capture_message`."""
        return await self.create_custom_observation(
            trace_id=trace_id or uuid.uuid4().hex,
            name=name or "message",
            type="EVENT",
            level=level,
            status_message=message,
            model=model,
            metadata=_issue_metadata(None, provider, status_code, context),
        )

    async def list_custom_observations(self, *, trace_id: str) -> Dict[str, Any]:
        return await self._t.request("POST", "/v1/traces/observations/custom/list", json_body={"traceId": trace_id})

    async def create_annotation(
        self,
        *,
        trace_id: str,
        body: str,
        observation_id: Optional[str] = None,
        metadata: Optional[Dict[str, str]] = None,
    ) -> Dict[str, Any]:
        payload: Dict[str, Any] = {"traceId": trace_id, "body": body}
        if observation_id is not None:
            payload["observationId"] = observation_id
        if metadata is not None:
            payload["metadata"] = metadata
        return await self._t.request("POST", "/v1/traces/annotations", json_body=payload)

    async def list_annotations(self, *, trace_id: str) -> Dict[str, Any]:
        return await self._t.request("POST", "/v1/traces/annotations/list", json_body={"traceId": trace_id})


class _TraceSpanContext:
    def __init__(self, traces: Traces, *, trace_id: str, name: str, **kwargs: Any) -> None:
        self._constructor_options = {"trace_id": trace_id, "name": name, **kwargs}
        parent_id = kwargs.pop("parent_observation_id", None) or _current_observation_id.get()
        self._traces = traces
        self._span = TraceSpan(
            observation_id=str(uuid.uuid4()),
            options={
                "trace_id": trace_id,
                "name": name,
                "type": kwargs.pop("type", "SPAN"),
                "source": kwargs.pop("source", "SDK"),
                "parent_observation_id": parent_id,
                **kwargs,
            },
        )
        self._token = None

    def __enter__(self) -> TraceSpan:
        self._token = _current_observation_id.set(self._span.id)
        return self._span

    def __exit__(self, exc_type: Any, exc: Optional[BaseException], tb: Any) -> bool:
        if self._token is not None:
            _current_observation_id.reset(self._token)
        self._record(exc)
        return False

    def __call__(self, fn: Callable[..., _T]) -> Callable[..., _T]:
        @wraps(fn)
        def wrapper(*args: Any, **kwargs: Any) -> _T:
            with _TraceSpanContext(self._traces, **self._constructor_options) as span:
                span.set_input({"args": args, "kwargs": kwargs})
                result = fn(*args, **kwargs)
                span.set_output(result)
                return result

        return wrapper

    def _record(self, exc: Optional[BaseException]) -> None:
        opts = dict(self._span.options)
        for generated_key in ("observation_id", "start_time", "end_time", "duration"):
            opts.pop(generated_key, None)
        if exc is not None:
            opts["level"] = "ERROR"
            opts["status_message"] = str(exc)
        opts.setdefault("level", "DEFAULT")
        self._traces.create_custom_observation(
            observation_id=self._span.id,
            start_time=self._span.started_at_iso,
            end_time=_utc_now_iso(),
            duration=max(0, time.time_ns() - self._span.started_at),
            **opts,
        )


class _AsyncTraceSpanContext:
    def __init__(self, traces: AsyncTraces, *, trace_id: str, name: str, **kwargs: Any) -> None:
        self._constructor_options = {"trace_id": trace_id, "name": name, **kwargs}
        parent_id = kwargs.pop("parent_observation_id", None) or _current_observation_id.get()
        self._traces = traces
        self._span = TraceSpan(
            observation_id=str(uuid.uuid4()),
            options={
                "trace_id": trace_id,
                "name": name,
                "type": kwargs.pop("type", "SPAN"),
                "source": kwargs.pop("source", "SDK"),
                "parent_observation_id": parent_id,
                **kwargs,
            },
        )
        self._token = None

    async def __aenter__(self) -> TraceSpan:
        self._token = _current_observation_id.set(self._span.id)
        return self._span

    async def __aexit__(self, exc_type: Any, exc: Optional[BaseException], tb: Any) -> bool:
        if self._token is not None:
            _current_observation_id.reset(self._token)
        await self._record(exc)
        return False

    def __call__(self, fn: Callable[..., Any]) -> Callable[..., Any]:
        @wraps(fn)
        async def wrapper(*args: Any, **kwargs: Any) -> Any:
            async with _AsyncTraceSpanContext(self._traces, **self._constructor_options) as span:
                span.set_input({"args": args, "kwargs": kwargs})
                result = await fn(*args, **kwargs)
                span.set_output(result)
                return result

        return wrapper

    async def _record(self, exc: Optional[BaseException]) -> None:
        opts = dict(self._span.options)
        for generated_key in ("observation_id", "start_time", "end_time", "duration"):
            opts.pop(generated_key, None)
        if exc is not None:
            opts["level"] = "ERROR"
            opts["status_message"] = str(exc)
        opts.setdefault("level", "DEFAULT")
        await self._traces.create_custom_observation(
            observation_id=self._span.id,
            start_time=self._span.started_at_iso,
            end_time=_utc_now_iso(),
            duration=max(0, time.time_ns() - self._span.started_at),
            **opts,
        )


def _issue_metadata(
    error: Optional[BaseException],
    provider: Optional[str],
    status_code: Optional[int],
    context: Optional[Dict[str, str]],
) -> Optional[Dict[str, str]]:
    """Build the observation metadata that the Issues backend reads as span
    attributes: ``llm.provider``, ``error.type`` and ``http.status_code`` drive
    the issue's provider/model/category facets; ``context`` adds free-form tags.
    """
    md: Dict[str, str] = {}
    if provider:
        md["llm.provider"] = provider
    if error is not None:
        md["error.type"] = type(error).__name__
    if status_code is not None:
        md["http.status_code"] = str(status_code)
    if context:
        md.update({str(k): str(v) for k, v in context.items()})
    return md or None


def _utc_now_iso() -> str:
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def _serialize_payload(value: Any, mime_type: Optional[str] = None) -> tuple[Optional[str], Optional[str]]:
    if value is None:
        return None, None
    if isinstance(value, str):
        return value, mime_type or "text/plain"
    return json.dumps(value, default=str), mime_type or "application/json"
