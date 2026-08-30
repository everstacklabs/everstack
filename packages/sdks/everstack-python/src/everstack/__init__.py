"""Everstack — The official Python SDK for the Everstack AI platform."""

from ._version import __version__
from ._client import Everstack, AsyncEverstack
from ._errors import (
    EverstackError,
    APIError,
    AuthenticationError,
    PermissionDeniedError,
    NotFoundError,
    RateLimitError,
    InternalServerError,
    ServiceUnavailableError,
    TimeoutError,
    ConnectionError,
    InvalidModelError,
)
from ._types import (
    # Chat
    ChatMessage,
    Usage,
    FallbackInfo,
    ChoiceMessage,
    Choice,
    ChatCompletionResponse,
    DeltaMessage,
    ChunkChoice,
    ChatCompletionChunk,
    # Embeddings
    EmbeddingData,
    EmbeddingsUsage,
    EmbeddingsResponse,
    # Models
    Model,
    ModelsListResponse,
    # Audio
    TranscriptionWord,
    TranscriptionSegment,
    TranscriptionResponse,
    TranslationResponse,
    SpeechResponse,
    # Images
    ImageData,
    ImageUsage,
    ImageResponse,
    # Moderations
    ModerationCategories,
    ModerationCategoryScores,
    ModerationResult,
    ModerationResponse,
    # Rerank
    RerankResult,
    RerankMeta,
    RerankResponse,
    # Responses API
    ResponseOutputItem,
    ResponseUsage,
    ResponseObject,
    DeleteResponseResult,
    ListResponsesResult,
)
from .evals import (
    TestCase,
    Metric,
    MetricResult,
    EvalResult,
    evaluate,
    assert_test,
)

__all__ = [
    "__version__",
    # Clients
    "Everstack",
    "AsyncEverstack",
    # Pytest-native evals
    "TestCase",
    "Metric",
    "MetricResult",
    "EvalResult",
    "evaluate",
    "assert_test",
    # Errors
    "EverstackError",
    "APIError",
    "AuthenticationError",
    "PermissionDeniedError",
    "NotFoundError",
    "RateLimitError",
    "InternalServerError",
    "ServiceUnavailableError",
    "TimeoutError",
    "ConnectionError",
    "InvalidModelError",
    # Chat types
    "ChatMessage",
    "Usage",
    "FallbackInfo",
    "ChoiceMessage",
    "Choice",
    "ChatCompletionResponse",
    "DeltaMessage",
    "ChunkChoice",
    "ChatCompletionChunk",
    # Embeddings types
    "EmbeddingData",
    "EmbeddingsUsage",
    "EmbeddingsResponse",
    # Models types
    "Model",
    "ModelsListResponse",
    # Audio types
    "TranscriptionWord",
    "TranscriptionSegment",
    "TranscriptionResponse",
    "TranslationResponse",
    "SpeechResponse",
    # Images types
    "ImageData",
    "ImageUsage",
    "ImageResponse",
    # Moderations types
    "ModerationCategories",
    "ModerationCategoryScores",
    "ModerationResult",
    "ModerationResponse",
    # Rerank types
    "RerankResult",
    "RerankMeta",
    "RerankResponse",
    # Responses types
    "ResponseOutputItem",
    "ResponseUsage",
    "ResponseObject",
    "DeleteResponseResult",
    "ListResponsesResult",
]
