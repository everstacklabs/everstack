"""Embeddings resource."""

from __future__ import annotations

from typing import Any, Dict, List, Optional, Union

from .._transport import Transport, AsyncTransport
from .._types import EmbeddingsResponse


def _parse_model(model: str) -> str:
    if model.startswith("@"):
        parts = model.split("/", 1)
        if len(parts) == 2:
            return parts[1]
    return model


class Embeddings:
    """Sync embeddings resource."""

    def __init__(self, transport: Transport) -> None:
        self._transport = transport

    def create(
        self,
        *,
        model: str,
        input: Union[str, List[str]],
        dimensions: Optional[int] = None,
        **kwargs: Any,
    ) -> EmbeddingsResponse:
        """Create embeddings for the given input."""
        body: Dict[str, Any] = {
            "model": _parse_model(model),
            "input": input if isinstance(input, str) else " ".join(input),
        }
        if dimensions is not None:
            body["dimensions"] = dimensions
        body.update(kwargs)

        data = self._transport.request("POST", "/v1/embeddings", json_body=body)
        return EmbeddingsResponse.model_validate(data)


class AsyncEmbeddings:
    """Async embeddings resource."""

    def __init__(self, transport: AsyncTransport) -> None:
        self._transport = transport

    async def create(
        self,
        *,
        model: str,
        input: Union[str, List[str]],
        dimensions: Optional[int] = None,
        **kwargs: Any,
    ) -> EmbeddingsResponse:
        """Create embeddings for the given input (async)."""
        body: Dict[str, Any] = {
            "model": _parse_model(model),
            "input": input if isinstance(input, str) else " ".join(input),
        }
        if dimensions is not None:
            body["dimensions"] = dimensions
        body.update(kwargs)

        data = await self._transport.request("POST", "/v1/embeddings", json_body=body)
        return EmbeddingsResponse.model_validate(data)
