"""HTTP transport layer for the Everstack SDK."""

from __future__ import annotations

import json
from typing import Any, AsyncIterator, Dict, Iterator, Optional

import httpx

from ._errors import (
    ConnectionError,
    TimeoutError,
    _raise_for_status,
)

GATEWAY_URL = "https://gateway.everstack.ai"

# Header names matching the Node SDK transport
_HEADER_API_KEY = "x-evs-api-key"
_HEADER_PROVIDER = "x-evs-provider"
_HEADER_ORG_ID = "x-evs-org-id"
_HEADER_USER_ID = "x-evs-user-id"


class Transport:
    """Sync HTTP transport backed by httpx."""

    def __init__(
        self,
        *,
        base_url: str,
        api_key: str,
        provider: Optional[str] = None,
        org_id: Optional[str] = None,
        user_id: Optional[str] = None,
        headers: Optional[Dict[str, str]] = None,
        timeout: float = 60.0,
    ) -> None:
        self._base_url = base_url.rstrip("/")

        default_headers: Dict[str, str] = {
            _HEADER_API_KEY: api_key,
            "Content-Type": "application/json",
        }
        if provider:
            default_headers[_HEADER_PROVIDER] = provider
        if org_id:
            default_headers[_HEADER_ORG_ID] = org_id
        if user_id:
            default_headers[_HEADER_USER_ID] = user_id
        if headers:
            default_headers.update(headers)

        self._client = httpx.Client(
            base_url=self._base_url,
            headers=default_headers,
            timeout=httpx.Timeout(timeout),
        )

    def request(
        self,
        method: str,
        path: str,
        *,
        json_body: Optional[Any] = None,
        params: Optional[Dict[str, Any]] = None,
    ) -> Any:
        """Make a synchronous HTTP request and return the parsed JSON body."""
        try:
            response = self._client.request(
                method,
                path,
                json=json_body,
                params=_clean_params(params),
            )
        except httpx.TimeoutException as exc:
            raise TimeoutError(str(exc)) from exc
        except httpx.ConnectError as exc:
            raise ConnectionError(str(exc)) from exc

        if response.status_code >= 400:
            try:
                body = response.json()
            except Exception:
                body = {"message": response.text}
            _raise_for_status(response.status_code, body)

        if response.status_code == 204:
            return None

        return response.json()

    def stream(
        self,
        method: str,
        path: str,
        *,
        json_body: Optional[Any] = None,
    ) -> Iterator[Dict[str, Any]]:
        """Make a streaming request and yield parsed SSE/JSON-line events."""
        try:
            with self._client.stream(method, path, json=json_body) as response:
                if response.status_code >= 400:
                    body_text = response.read().decode()
                    try:
                        body = json.loads(body_text)
                    except Exception:
                        body = {"message": body_text}
                    _raise_for_status(response.status_code, body)

                for line in response.iter_lines():
                    if not line:
                        continue
                    # Handle SSE format: "data: {...}"
                    if line.startswith("data: "):
                        data = line[6:]
                        if data == "[DONE]":
                            break
                        try:
                            yield json.loads(data)
                        except json.JSONDecodeError:
                            continue
                    else:
                        # Plain JSON lines
                        try:
                            yield json.loads(line)
                        except json.JSONDecodeError:
                            continue
        except httpx.TimeoutException as exc:
            raise TimeoutError(str(exc)) from exc
        except httpx.ConnectError as exc:
            raise ConnectionError(str(exc)) from exc

    def close(self) -> None:
        self._client.close()


class AsyncTransport:
    """Async HTTP transport backed by httpx."""

    def __init__(
        self,
        *,
        base_url: str,
        api_key: str,
        provider: Optional[str] = None,
        org_id: Optional[str] = None,
        user_id: Optional[str] = None,
        headers: Optional[Dict[str, str]] = None,
        timeout: float = 60.0,
    ) -> None:
        self._base_url = base_url.rstrip("/")

        default_headers: Dict[str, str] = {
            _HEADER_API_KEY: api_key,
            "Content-Type": "application/json",
        }
        if provider:
            default_headers[_HEADER_PROVIDER] = provider
        if org_id:
            default_headers[_HEADER_ORG_ID] = org_id
        if user_id:
            default_headers[_HEADER_USER_ID] = user_id
        if headers:
            default_headers.update(headers)

        self._client = httpx.AsyncClient(
            base_url=self._base_url,
            headers=default_headers,
            timeout=httpx.Timeout(timeout),
        )

    async def request(
        self,
        method: str,
        path: str,
        *,
        json_body: Optional[Any] = None,
        params: Optional[Dict[str, Any]] = None,
    ) -> Any:
        """Make an async HTTP request and return the parsed JSON body."""
        try:
            response = await self._client.request(
                method,
                path,
                json=json_body,
                params=_clean_params(params),
            )
        except httpx.TimeoutException as exc:
            raise TimeoutError(str(exc)) from exc
        except httpx.ConnectError as exc:
            raise ConnectionError(str(exc)) from exc

        if response.status_code >= 400:
            try:
                body = response.json()
            except Exception:
                body = {"message": response.text}
            _raise_for_status(response.status_code, body)

        if response.status_code == 204:
            return None

        return response.json()

    async def stream(
        self,
        method: str,
        path: str,
        *,
        json_body: Optional[Any] = None,
    ) -> AsyncIterator[Dict[str, Any]]:
        """Make a streaming request and yield parsed SSE/JSON-line events."""
        try:
            async with self._client.stream(method, path, json=json_body) as response:
                if response.status_code >= 400:
                    body_bytes = await response.aread()
                    body_text = body_bytes.decode()
                    try:
                        body = json.loads(body_text)
                    except Exception:
                        body = {"message": body_text}
                    _raise_for_status(response.status_code, body)

                async for line in response.aiter_lines():
                    if not line:
                        continue
                    if line.startswith("data: "):
                        data = line[6:]
                        if data == "[DONE]":
                            break
                        try:
                            yield json.loads(data)
                        except json.JSONDecodeError:
                            continue
                    else:
                        try:
                            yield json.loads(line)
                        except json.JSONDecodeError:
                            continue
        except httpx.TimeoutException as exc:
            raise TimeoutError(str(exc)) from exc
        except httpx.ConnectError as exc:
            raise ConnectionError(str(exc)) from exc

    async def close(self) -> None:
        await self._client.aclose()


def _clean_params(params: Optional[Dict[str, Any]]) -> Optional[Dict[str, str]]:
    """Remove None values and convert to strings for query params."""
    if params is None:
        return None
    return {k: str(v) for k, v in params.items() if v is not None}
