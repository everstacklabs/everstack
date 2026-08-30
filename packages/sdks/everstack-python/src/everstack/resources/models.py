"""Models resource."""

from __future__ import annotations

from .._transport import Transport, AsyncTransport
from .._types import ModelsListResponse


class Models:
    """Sync models resource."""

    def __init__(self, transport: Transport) -> None:
        self._transport = transport

    def list(self) -> ModelsListResponse:
        """List available models."""
        data = self._transport.request("GET", "/v1/gateway/models")
        return ModelsListResponse.model_validate(data)


class AsyncModels:
    """Async models resource."""

    def __init__(self, transport: AsyncTransport) -> None:
        self._transport = transport

    async def list(self) -> ModelsListResponse:
        """List available models (async)."""
        data = await self._transport.request("GET", "/v1/gateway/models")
        return ModelsListResponse.model_validate(data)
