"""Chat completions resource."""

from __future__ import annotations

from typing import Any, Dict, Iterator, List, Optional, Union, overload

from .._transport import Transport, AsyncTransport
from .._types import ChatCompletionChunk, ChatCompletionResponse


def _parse_model(model: str) -> str:
    """Extract model name from @provider/model format."""
    if model.startswith("@"):
        parts = model.split("/", 1)
        if len(parts) == 2:
            return parts[1]
    return model


def _build_body(
    model: str,
    messages: List[Dict[str, Any]],
    *,
    stream: bool = False,
    temperature: Optional[float] = None,
    top_p: Optional[float] = None,
    max_tokens: Optional[int] = None,
    max_completion_tokens: Optional[int] = None,
    stop: Optional[Union[str, List[str]]] = None,
    tools: Optional[List[Dict[str, Any]]] = None,
    tool_choice: Optional[Any] = None,
    response_format: Optional[Dict[str, Any]] = None,
    **kwargs: Any,
) -> Dict[str, Any]:
    body: Dict[str, Any] = {
        "model": _parse_model(model),
        "messages": messages,
        "stream": stream,
    }
    if temperature is not None:
        body["temperature"] = temperature
    if top_p is not None:
        body["top_p"] = top_p
    if max_tokens is not None:
        body["max_tokens"] = max_tokens
    if max_completion_tokens is not None:
        body["max_completion_tokens"] = max_completion_tokens
    if stop is not None:
        body["stop"] = [stop] if isinstance(stop, str) else stop
    if tools is not None:
        body["tools"] = tools
    if tool_choice is not None:
        body["tool_choice"] = tool_choice
    if response_format is not None:
        body["response_format"] = response_format
    body.update(kwargs)
    return body


class Completions:
    """Sync chat completions."""

    def __init__(self, transport: Transport) -> None:
        self._transport = transport

    @overload
    def create(
        self,
        *,
        model: str,
        messages: List[Dict[str, Any]],
        stream: None = ...,
        **kwargs: Any,
    ) -> ChatCompletionResponse: ...

    @overload
    def create(
        self,
        *,
        model: str,
        messages: List[Dict[str, Any]],
        stream: bool = ...,
        **kwargs: Any,
    ) -> Union[ChatCompletionResponse, Iterator[ChatCompletionChunk]]: ...

    def create(
        self,
        *,
        model: str,
        messages: List[Dict[str, Any]],
        stream: Optional[bool] = None,
        **kwargs: Any,
    ) -> Union[ChatCompletionResponse, Iterator[ChatCompletionChunk]]:
        """Create a chat completion.

        Example::

            response = client.chat.completions.create(
                model="@openai/gpt-4o",
                messages=[{"role": "user", "content": "Hello!"}],
            )
            print(response.choices[0].message.content)
        """
        body = _build_body(model, messages, stream=bool(stream), **kwargs)

        if stream:
            return self._stream(body)

        data = self._transport.request("POST", "/v1/chat/completions", json_body=body)
        return ChatCompletionResponse.model_validate(data)

    def _stream(self, body: Dict[str, Any]) -> Iterator[ChatCompletionChunk]:
        for event in self._transport.stream("POST", "/v1/chat/completions", json_body=body):
            yield ChatCompletionChunk.model_validate(event)


class AsyncCompletions:
    """Async chat completions."""

    def __init__(self, transport: AsyncTransport) -> None:
        self._transport = transport

    async def create(
        self,
        *,
        model: str,
        messages: List[Dict[str, Any]],
        stream: Optional[bool] = None,
        **kwargs: Any,
    ) -> Any:
        """Create a chat completion (async)."""
        body = _build_body(model, messages, stream=bool(stream), **kwargs)

        if stream:
            return self._stream(body)

        data = await self._transport.request("POST", "/v1/chat/completions", json_body=body)
        return ChatCompletionResponse.model_validate(data)

    async def _stream(self, body: Dict[str, Any]) -> Any:
        async for event in self._transport.stream("POST", "/v1/chat/completions", json_body=body):
            yield ChatCompletionChunk.model_validate(event)


class Chat:
    """Sync chat resource with completions sub-resource."""

    completions: Completions

    def __init__(self, transport: Transport) -> None:
        self.completions = Completions(transport)


class AsyncChat:
    """Async chat resource with completions sub-resource."""

    completions: AsyncCompletions

    def __init__(self, transport: AsyncTransport) -> None:
        self.completions = AsyncCompletions(transport)
