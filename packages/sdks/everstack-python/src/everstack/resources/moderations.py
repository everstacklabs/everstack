"""Moderations resource."""

from __future__ import annotations

from typing import Any, Dict, List, Optional, Union

from .._transport import Transport, AsyncTransport
from .._types import ModerationResponse


class Moderations:
    def __init__(self, transport: Transport) -> None:
        self._transport = transport

    def create(
        self,
        *,
        input: Optional[str] = None,
        inputs: Optional[List[Dict[str, Any]]] = None,
        model: Optional[str] = None,
    ) -> ModerationResponse:
        """Classify content for policy violations."""
        body: Dict[str, Any] = {}
        if input is not None:
            body["input"] = input
        if inputs is not None:
            body["inputs"] = inputs
        if model:
            body["model"] = model
        data = self._transport.request("POST", "/v1/moderations", json_body=body)
        return ModerationResponse.model_validate(data)


class AsyncModerations:
    def __init__(self, transport: AsyncTransport) -> None:
        self._transport = transport

    async def create(
        self,
        *,
        input: Optional[str] = None,
        inputs: Optional[List[Dict[str, Any]]] = None,
        model: Optional[str] = None,
    ) -> ModerationResponse:
        body: Dict[str, Any] = {}
        if input is not None:
            body["input"] = input
        if inputs is not None:
            body["inputs"] = inputs
        if model:
            body["model"] = model
        data = await self._transport.request("POST", "/v1/moderations", json_body=body)
        return ModerationResponse.model_validate(data)
