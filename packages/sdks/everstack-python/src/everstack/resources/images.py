"""Images resource — generation, editing, variations."""

from __future__ import annotations

import base64
from typing import Any, Dict, Optional

from .._transport import Transport, AsyncTransport
from .._types import ImageResponse


class Images:
    def __init__(self, transport: Transport) -> None:
        self._transport = transport

    def generate(
        self,
        *,
        prompt: str,
        model: Optional[str] = None,
        n: Optional[int] = None,
        quality: Optional[str] = None,
        response_format: Optional[str] = None,
        size: Optional[str] = None,
        style: Optional[str] = None,
        user: Optional[str] = None,
        **kwargs: Any,
    ) -> ImageResponse:
        """Generate images from a text prompt."""
        body: Dict[str, Any] = {"prompt": prompt}
        if model:
            body["model"] = model
        if n is not None:
            body["n"] = n
        if quality:
            body["quality"] = quality
        if response_format:
            body["response_format"] = response_format
        if size:
            body["size"] = size
        if style:
            body["style"] = style
        if user:
            body["user"] = user
        body.update(kwargs)
        data = self._transport.request("POST", "/v1/images/generations", json_body=body)
        return ImageResponse.model_validate(data)

    def edit(
        self,
        *,
        image: bytes,
        prompt: str,
        mask: Optional[bytes] = None,
        model: Optional[str] = None,
        n: Optional[int] = None,
        size: Optional[str] = None,
        **kwargs: Any,
    ) -> ImageResponse:
        """Edit an existing image."""
        body: Dict[str, Any] = {
            "image": base64.b64encode(image).decode(),
            "prompt": prompt,
        }
        if mask:
            body["mask"] = base64.b64encode(mask).decode()
        if model:
            body["model"] = model
        if n is not None:
            body["n"] = n
        if size:
            body["size"] = size
        body.update(kwargs)
        data = self._transport.request("POST", "/v1/images/edits", json_body=body)
        return ImageResponse.model_validate(data)

    def create_variation(
        self,
        *,
        image: bytes,
        model: Optional[str] = None,
        n: Optional[int] = None,
        size: Optional[str] = None,
        **kwargs: Any,
    ) -> ImageResponse:
        """Create variations of an image."""
        body: Dict[str, Any] = {"image": base64.b64encode(image).decode()}
        if model:
            body["model"] = model
        if n is not None:
            body["n"] = n
        if size:
            body["size"] = size
        body.update(kwargs)
        data = self._transport.request("POST", "/v1/images/variations", json_body=body)
        return ImageResponse.model_validate(data)


class AsyncImages:
    def __init__(self, transport: AsyncTransport) -> None:
        self._transport = transport

    async def generate(self, *, prompt: str, **kwargs: Any) -> ImageResponse:
        body: Dict[str, Any] = {"prompt": prompt, **kwargs}
        data = await self._transport.request("POST", "/v1/images/generations", json_body=body)
        return ImageResponse.model_validate(data)

    async def edit(self, *, image: bytes, prompt: str, **kwargs: Any) -> ImageResponse:
        body: Dict[str, Any] = {
            "image": base64.b64encode(image).decode(),
            "prompt": prompt,
            **kwargs,
        }
        data = await self._transport.request("POST", "/v1/images/edits", json_body=body)
        return ImageResponse.model_validate(data)

    async def create_variation(self, *, image: bytes, **kwargs: Any) -> ImageResponse:
        body: Dict[str, Any] = {"image": base64.b64encode(image).decode(), **kwargs}
        data = await self._transport.request("POST", "/v1/images/variations", json_body=body)
        return ImageResponse.model_validate(data)
