"""Everstack client — the main entry point for the SDK."""

from __future__ import annotations

from typing import Optional, Dict

from ._transport import Transport, AsyncTransport, GATEWAY_URL
from .resources.chat import Chat, AsyncChat
from .resources.embeddings import Embeddings, AsyncEmbeddings
from .resources.models import Models, AsyncModels
from .resources.audio import Audio, AsyncAudio
from .resources.images import Images, AsyncImages
from .resources.moderations import Moderations, AsyncModerations
from .resources.rerank import Rerank, AsyncRerank
from .resources.responses import Responses, AsyncResponses
from .resources.agents import Agents, AsyncAgents
from .resources.datasets import Datasets, AsyncDatasets
from .resources.evaluations import Evaluations, AsyncEvaluations
from .resources.observability import Observability, AsyncObservability
from .resources.traces import Traces, AsyncTraces


class Everstack:
    """Synchronous Everstack client.

    Example::

        from everstack import Everstack

        client = Everstack(api_key="pk_...")
        response = client.chat.completions.create(
            model="@openai/gpt-4o",
            messages=[{"role": "user", "content": "Hello!"}],
        )
        print(response.choices[0].message.content)
    """

    chat: Chat
    embeddings: Embeddings
    models: Models
    audio: Audio
    images: Images
    moderations: Moderations
    rerank: Rerank
    responses: Responses
    agents: Agents
    datasets: Datasets
    evaluations: Evaluations
    observability: Observability
    traces: Traces

    def __init__(
        self,
        api_key: str,
        *,
        base_url: Optional[str] = None,
        provider: Optional[str] = None,
        org_id: Optional[str] = None,
        user_id: Optional[str] = None,
        headers: Optional[Dict[str, str]] = None,
        timeout: float = 60.0,
    ) -> None:
        self._transport = Transport(
            base_url=base_url or GATEWAY_URL,
            api_key=api_key,
            provider=provider,
            org_id=org_id,
            user_id=user_id,
            headers=headers,
            timeout=timeout,
        )
        self._api_key = api_key

        self.chat = Chat(self._transport)
        self.embeddings = Embeddings(self._transport)
        self.models = Models(self._transport)
        self.audio = Audio(self._transport)
        self.images = Images(self._transport)
        self.moderations = Moderations(self._transport)
        self.rerank = Rerank(self._transport)
        self.responses = Responses(self._transport)
        self.agents = Agents(self._transport)
        self.datasets = Datasets(self._transport)
        self.evaluations = Evaluations(self._transport)
        self.observability = Observability(self._transport)
        self.traces = Traces(self._transport)

    def capture_exception(self, error: BaseException, **kwargs: object) -> Dict:
        """Report a caught exception so it surfaces as an Issue.

        Convenience delegate for :meth:`everstack.resources.traces.Traces.capture_exception`::

            try:
                ...
            except Exception as e:
                client.capture_exception(e, provider="openai", model="gpt-4o")
        """
        return self.traces.capture_exception(error, **kwargs)  # type: ignore[arg-type]

    def capture_message(self, message: str, **kwargs: object) -> Dict:
        """Report a free-form failure message as an Issue (ERROR level)."""
        return self.traces.capture_message(message, **kwargs)  # type: ignore[arg-type]

    def close(self) -> None:
        """Close the underlying HTTP client."""
        self._transport.close()

    def __enter__(self) -> "Everstack":
        return self

    def __exit__(self, *args: object) -> None:
        self.close()


class AsyncEverstack:
    """Asynchronous Everstack client.

    Example::

        from everstack import AsyncEverstack

        async with AsyncEverstack(api_key="pk_...") as client:
            response = await client.chat.completions.create(
                model="@openai/gpt-4o",
                messages=[{"role": "user", "content": "Hello!"}],
            )
            print(response.choices[0].message.content)
    """

    chat: AsyncChat
    embeddings: AsyncEmbeddings
    models: AsyncModels
    audio: AsyncAudio
    images: AsyncImages
    moderations: AsyncModerations
    rerank: AsyncRerank
    responses: AsyncResponses
    agents: AsyncAgents
    datasets: AsyncDatasets
    evaluations: AsyncEvaluations
    observability: AsyncObservability
    traces: AsyncTraces

    def __init__(
        self,
        api_key: str,
        *,
        base_url: Optional[str] = None,
        provider: Optional[str] = None,
        org_id: Optional[str] = None,
        user_id: Optional[str] = None,
        headers: Optional[Dict[str, str]] = None,
        timeout: float = 60.0,
    ) -> None:
        self._transport = AsyncTransport(
            base_url=base_url or GATEWAY_URL,
            api_key=api_key,
            provider=provider,
            org_id=org_id,
            user_id=user_id,
            headers=headers,
            timeout=timeout,
        )
        self._api_key = api_key

        self.chat = AsyncChat(self._transport)
        self.embeddings = AsyncEmbeddings(self._transport)
        self.models = AsyncModels(self._transport)
        self.audio = AsyncAudio(self._transport)
        self.images = AsyncImages(self._transport)
        self.moderations = AsyncModerations(self._transport)
        self.rerank = AsyncRerank(self._transport)
        self.responses = AsyncResponses(self._transport)
        self.agents = AsyncAgents(self._transport)
        self.datasets = AsyncDatasets(self._transport)
        self.evaluations = AsyncEvaluations(self._transport)
        self.observability = AsyncObservability(self._transport)
        self.traces = AsyncTraces(self._transport)

    async def capture_exception(self, error: BaseException, **kwargs: object) -> Dict:
        """Async mirror of :meth:`Everstack.capture_exception`."""
        return await self.traces.capture_exception(error, **kwargs)  # type: ignore[arg-type]

    async def capture_message(self, message: str, **kwargs: object) -> Dict:
        """Async mirror of :meth:`Everstack.capture_message`."""
        return await self.traces.capture_message(message, **kwargs)  # type: ignore[arg-type]

    async def close(self) -> None:
        """Close the underlying HTTP client."""
        await self._transport.close()

    async def __aenter__(self) -> "AsyncEverstack":
        return self

    async def __aexit__(self, *args: object) -> None:
        await self.close()
