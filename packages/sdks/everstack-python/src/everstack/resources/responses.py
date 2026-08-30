"""Responses API resource — agentic orchestration."""

from __future__ import annotations

from typing import Any, Dict, Iterator, List, Optional

from .._transport import Transport, AsyncTransport
from .._types import DeleteResponseResult, ListResponsesResult, ResponseObject


class Responses:
    def __init__(self, transport: Transport) -> None:
        self._transport = transport

    def create(
        self,
        *,
        model: str,
        input: List[Dict[str, Any]],
        instructions: Optional[str] = None,
        tools: Optional[List[Dict[str, Any]]] = None,
        builtin_tools: Optional[List[Dict[str, Any]]] = None,
        tool_choice: Optional[str] = None,
        parallel_tool_calls: Optional[bool] = None,
        reasoning: Optional[Dict[str, Any]] = None,
        max_output_tokens: Optional[int] = None,
        temperature: Optional[float] = None,
        top_p: Optional[float] = None,
        truncation: Optional[Dict[str, Any]] = None,
        store: Optional[bool] = None,
        previous_response_id: Optional[str] = None,
        stream: Optional[bool] = None,
        metadata: Optional[Dict[str, str]] = None,
        **kwargs: Any,
    ) -> Any:
        """Create a response (agentic orchestration)."""
        body: Dict[str, Any] = {"model": model, "input": input}
        if instructions is not None:
            body["instructions"] = instructions
        if tools is not None:
            body["tools"] = tools
        if builtin_tools is not None:
            body["builtin_tools"] = builtin_tools
        if tool_choice is not None:
            body["tool_choice"] = tool_choice
        if parallel_tool_calls is not None:
            body["parallel_tool_calls"] = parallel_tool_calls
        if reasoning is not None:
            body["reasoning"] = reasoning
        if max_output_tokens is not None:
            body["max_output_tokens"] = max_output_tokens
        if temperature is not None:
            body["temperature"] = temperature
        if top_p is not None:
            body["top_p"] = top_p
        if truncation is not None:
            body["truncation"] = truncation
        if store is not None:
            body["store"] = store
        if previous_response_id is not None:
            body["previous_response_id"] = previous_response_id
        if metadata is not None:
            body["metadata"] = metadata
        body["stream"] = bool(stream)
        body.update(kwargs)

        if stream:
            return self._stream(body)

        data = self._transport.request("POST", "/v1/responses", json_body=body)
        return ResponseObject.model_validate(data)

    def _stream(self, body: Dict[str, Any]) -> Iterator[Dict[str, Any]]:
        for event in self._transport.stream("POST", "/v1/responses", json_body=body):
            yield event

    def get(self, response_id: str) -> ResponseObject:
        """Get a response by ID."""
        data = self._transport.request("GET", f"/v1/responses/{response_id}")
        return ResponseObject.model_validate(data)

    def cancel(self, response_id: str) -> ResponseObject:
        """Cancel an in-progress response."""
        data = self._transport.request("POST", f"/v1/responses/{response_id}/cancel")
        return ResponseObject.model_validate(data)

    def delete(self, response_id: str) -> DeleteResponseResult:
        """Delete a response."""
        data = self._transport.request("DELETE", f"/v1/responses/{response_id}")
        return DeleteResponseResult.model_validate(data)

    def list(
        self,
        *,
        status: Optional[str] = None,
        limit: Optional[int] = None,
        after: Optional[str] = None,
        before: Optional[str] = None,
        order: Optional[str] = None,
    ) -> ListResponsesResult:
        """List responses."""
        params: Dict[str, Any] = {}
        if status:
            params["status"] = status
        if limit is not None:
            params["limit"] = limit
        if after:
            params["after"] = after
        if before:
            params["before"] = before
        if order:
            params["order"] = order
        data = self._transport.request("GET", "/v1/responses", params=params)
        return ListResponsesResult.model_validate(data)


class AsyncResponses:
    def __init__(self, transport: AsyncTransport) -> None:
        self._transport = transport

    async def create(
        self,
        *,
        model: str,
        input: List[Dict[str, Any]],
        stream: Optional[bool] = None,
        **kwargs: Any,
    ) -> Any:
        body: Dict[str, Any] = {"model": model, "input": input, "stream": bool(stream), **kwargs}
        if stream:
            return self._stream(body)
        data = await self._transport.request("POST", "/v1/responses", json_body=body)
        return ResponseObject.model_validate(data)

    async def _stream(self, body: Dict[str, Any]) -> Any:
        async for event in self._transport.stream("POST", "/v1/responses", json_body=body):
            yield event

    async def get(self, response_id: str) -> ResponseObject:
        data = await self._transport.request("GET", f"/v1/responses/{response_id}")
        return ResponseObject.model_validate(data)

    async def cancel(self, response_id: str) -> ResponseObject:
        data = await self._transport.request("POST", f"/v1/responses/{response_id}/cancel")
        return ResponseObject.model_validate(data)

    async def delete(self, response_id: str) -> DeleteResponseResult:
        data = await self._transport.request("DELETE", f"/v1/responses/{response_id}")
        return DeleteResponseResult.model_validate(data)

    async def list(self, **kwargs: Any) -> ListResponsesResult:
        data = await self._transport.request("GET", "/v1/responses", params=kwargs)
        return ListResponsesResult.model_validate(data)
