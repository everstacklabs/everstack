"""Datasets resource."""

from __future__ import annotations

from typing import Any, Dict

from .._transport import Transport, AsyncTransport


class _Items:
    def __init__(self, transport: Transport) -> None:
        self._t = transport

    def create(self, dataset_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("POST", f"/v1/datasets/{dataset_id}/items", json_body=kwargs)

    def create_batch(self, dataset_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request(
            "POST", f"/v1/datasets/{dataset_id}/items/batch", json_body=kwargs
        )

    def get(self, item_id: str) -> Dict[str, Any]:
        return self._t.request("GET", f"/v1/datasets/items/{item_id}")

    def list(self, dataset_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("GET", f"/v1/datasets/{dataset_id}/items", params=kwargs)

    def update(self, item_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("PATCH", f"/v1/datasets/items/{item_id}", json_body=kwargs)

    def delete(self, item_id: str) -> Dict[str, Any]:
        return self._t.request("DELETE", f"/v1/datasets/items/{item_id}")


class _ScoreConfigs:
    def __init__(self, transport: Transport) -> None:
        self._t = transport

    def create(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("POST", "/v1/score-configs", json_body=kwargs)

    def get(self, config_id: str) -> Dict[str, Any]:
        return self._t.request("GET", f"/v1/score-configs/{config_id}")

    def list(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("GET", "/v1/score-configs", params=kwargs)

    def update(self, config_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("PATCH", f"/v1/score-configs/{config_id}", json_body=kwargs)

    def delete(self, config_id: str) -> Dict[str, Any]:
        return self._t.request("DELETE", f"/v1/score-configs/{config_id}")


class Datasets:
    """Sync datasets resource."""

    items: _Items
    score_configs: _ScoreConfigs

    def __init__(self, transport: Transport) -> None:
        self._t = transport
        self.items = _Items(transport)
        self.score_configs = _ScoreConfigs(transport)

    def create(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("POST", "/v1/datasets", json_body=kwargs)

    def get(self, dataset_id: str) -> Dict[str, Any]:
        return self._t.request("GET", f"/v1/datasets/{dataset_id}")

    def list(self, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("GET", "/v1/datasets", params=kwargs)

    def update(self, dataset_id: str, **kwargs: Any) -> Dict[str, Any]:
        return self._t.request("PATCH", f"/v1/datasets/{dataset_id}", json_body=kwargs)

    def delete(self, dataset_id: str) -> Dict[str, Any]:
        return self._t.request("DELETE", f"/v1/datasets/{dataset_id}")

    def list_builtin_metrics(self) -> Dict[str, Any]:
        return self._t.request("GET", "/v1/builtin-metrics")


class AsyncDatasets:
    """Async datasets resource."""

    def __init__(self, transport: AsyncTransport) -> None:
        self._t = transport

    async def create(self, **kwargs: Any) -> Dict[str, Any]:
        return await self._t.request("POST", "/v1/datasets", json_body=kwargs)

    async def get(self, dataset_id: str) -> Dict[str, Any]:
        return await self._t.request("GET", f"/v1/datasets/{dataset_id}")

    async def list(self, **kwargs: Any) -> Dict[str, Any]:
        return await self._t.request("GET", "/v1/datasets", params=kwargs)

    async def update(self, dataset_id: str, **kwargs: Any) -> Dict[str, Any]:
        return await self._t.request("PATCH", f"/v1/datasets/{dataset_id}", json_body=kwargs)

    async def delete(self, dataset_id: str) -> Dict[str, Any]:
        return await self._t.request("DELETE", f"/v1/datasets/{dataset_id}")

    async def create_item(self, dataset_id: str, **kwargs: Any) -> Dict[str, Any]:
        return await self._t.request(
            "POST", f"/v1/datasets/{dataset_id}/items", json_body=kwargs
        )

    async def create_item_batch(self, dataset_id: str, **kwargs: Any) -> Dict[str, Any]:
        return await self._t.request(
            "POST", f"/v1/datasets/{dataset_id}/items/batch", json_body=kwargs
        )
