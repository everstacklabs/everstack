"""Rerank resource."""

from __future__ import annotations

from typing import Any, Dict, List, Optional

from .._transport import Transport, AsyncTransport
from .._types import RerankResponse


class Rerank:
    def __init__(self, transport: Transport) -> None:
        self._transport = transport

    def create(
        self,
        *,
        model: str,
        query: str,
        documents: Optional[List[str]] = None,
        document_objects: Optional[List[Dict[str, str]]] = None,
        top_n: Optional[int] = None,
        return_documents: Optional[bool] = None,
        max_tokens_per_doc: Optional[int] = None,
    ) -> RerankResponse:
        """Rerank documents by relevance to a query."""
        body: Dict[str, Any] = {"model": model, "query": query}
        if documents is not None:
            body["documents"] = documents
        if document_objects is not None:
            body["document_objects"] = document_objects
        if top_n is not None:
            body["top_n"] = top_n
        if return_documents is not None:
            body["return_documents"] = return_documents
        if max_tokens_per_doc is not None:
            body["max_tokens_per_doc"] = max_tokens_per_doc
        data = self._transport.request("POST", "/v1/rerank", json_body=body)
        return RerankResponse.model_validate(data)


class AsyncRerank:
    def __init__(self, transport: AsyncTransport) -> None:
        self._transport = transport

    async def create(
        self,
        *,
        model: str,
        query: str,
        documents: Optional[List[str]] = None,
        document_objects: Optional[List[Dict[str, str]]] = None,
        top_n: Optional[int] = None,
        return_documents: Optional[bool] = None,
        max_tokens_per_doc: Optional[int] = None,
    ) -> RerankResponse:
        body: Dict[str, Any] = {"model": model, "query": query}
        if documents is not None:
            body["documents"] = documents
        if document_objects is not None:
            body["document_objects"] = document_objects
        if top_n is not None:
            body["top_n"] = top_n
        if return_documents is not None:
            body["return_documents"] = return_documents
        if max_tokens_per_doc is not None:
            body["max_tokens_per_doc"] = max_tokens_per_doc
        data = await self._transport.request("POST", "/v1/rerank", json_body=body)
        return RerankResponse.model_validate(data)
