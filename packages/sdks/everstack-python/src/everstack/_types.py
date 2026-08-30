"""Shared types for the Everstack SDK."""

from __future__ import annotations

from typing import Any, Dict, List, Literal, Optional, Union

from pydantic import BaseModel


# ---------------------------------------------------------------------------
# Chat types
# ---------------------------------------------------------------------------

class ChatMessage(BaseModel):
    role: Literal["system", "user", "assistant", "tool", "function"]
    content: Optional[Union[str, List[Dict[str, Any]]]] = None
    name: Optional[str] = None
    tool_call_id: Optional[str] = None
    tool_calls: Optional[List[Dict[str, Any]]] = None


class Usage(BaseModel):
    prompt_tokens: int = 0
    completion_tokens: int = 0
    total_tokens: int = 0


class FallbackInfo(BaseModel):
    fallback_used: bool = False
    requested_model: str = ""
    actual_model: str = ""
    fallback_reason: Optional[str] = None
    fallback_attempts: Optional[int] = None


class ChoiceMessage(BaseModel):
    role: str = "assistant"
    content: Optional[str] = None
    tool_calls: Optional[List[Dict[str, Any]]] = None


class Choice(BaseModel):
    index: int = 0
    message: ChoiceMessage = ChoiceMessage()
    finish_reason: Optional[str] = None
    logprobs: Optional[Any] = None


class ChatCompletionResponse(BaseModel):
    id: str = ""
    object: str = "chat.completion"
    created: int = 0
    model: str = ""
    choices: List[Choice] = []
    usage: Usage = Usage()
    fallback_info: Optional[FallbackInfo] = None


class DeltaMessage(BaseModel):
    role: Optional[str] = None
    content: Optional[str] = None


class ChunkChoice(BaseModel):
    index: int = 0
    delta: DeltaMessage = DeltaMessage()
    finish_reason: Optional[str] = None
    logprobs: Optional[Any] = None


class ChatCompletionChunk(BaseModel):
    id: str = ""
    object: str = "chat.completion.chunk"
    created: int = 0
    model: str = ""
    choices: List[ChunkChoice] = []
    usage: Optional[Usage] = None


# ---------------------------------------------------------------------------
# Embeddings types
# ---------------------------------------------------------------------------

class EmbeddingData(BaseModel):
    object: str = "embedding"
    embedding: List[float] = []
    index: int = 0


class EmbeddingsUsage(BaseModel):
    prompt_tokens: int = 0
    total_tokens: int = 0


class EmbeddingsResponse(BaseModel):
    object: str = "list"
    data: List[EmbeddingData] = []
    model: str = ""
    usage: EmbeddingsUsage = EmbeddingsUsage()


# ---------------------------------------------------------------------------
# Models types
# ---------------------------------------------------------------------------

class Model(BaseModel):
    id: str
    object: str = "model"
    created: int = 0
    owned_by: str = ""


class ModelsListResponse(BaseModel):
    object: str = "list"
    data: List[Model] = []


# ---------------------------------------------------------------------------
# Audio types
# ---------------------------------------------------------------------------

class TranscriptionWord(BaseModel):
    word: str = ""
    start: float = 0.0
    end: float = 0.0


class TranscriptionSegment(BaseModel):
    id: int = 0
    seek: int = 0
    start: float = 0.0
    end: float = 0.0
    text: str = ""
    tokens: List[int] = []
    temperature: float = 0.0
    avg_logprob: float = 0.0
    compression_ratio: float = 0.0
    no_speech_prob: float = 0.0


class TranscriptionResponse(BaseModel):
    text: str = ""
    task: str = ""
    language: str = ""
    duration: float = 0.0
    words: List[TranscriptionWord] = []
    segments: List[TranscriptionSegment] = []


class TranslationResponse(BaseModel):
    text: str = ""
    task: str = ""
    language: str = ""
    duration: float = 0.0
    segments: List[TranscriptionSegment] = []


class SpeechResponse(BaseModel):
    audio: bytes = b""
    format: str = ""
    content_type: str = ""
    duration_seconds: float = 0.0
    input_characters: int = 0


# ---------------------------------------------------------------------------
# Image types
# ---------------------------------------------------------------------------

class ImageData(BaseModel):
    b64_json: Optional[str] = None
    url: Optional[str] = None
    revised_prompt: Optional[str] = None


class ImageUsage(BaseModel):
    input_tokens: int = 0
    output_tokens: int = 0
    total_tokens: int = 0


class ImageResponse(BaseModel):
    created: int = 0
    data: List[ImageData] = []
    model: str = ""
    usage: Optional[ImageUsage] = None


# ---------------------------------------------------------------------------
# Moderation types
# ---------------------------------------------------------------------------

class ModerationCategories(BaseModel):
    hate: bool = False
    hate_threatening: bool = False
    harassment: bool = False
    harassment_threatening: bool = False
    illicit: bool = False
    illicit_violent: bool = False
    self_harm: bool = False
    self_harm_intent: bool = False
    self_harm_instructions: bool = False
    sexual: bool = False
    sexual_minors: bool = False
    violence: bool = False
    violence_graphic: bool = False


class ModerationCategoryScores(BaseModel):
    hate: float = 0.0
    hate_threatening: float = 0.0
    harassment: float = 0.0
    harassment_threatening: float = 0.0
    illicit: float = 0.0
    illicit_violent: float = 0.0
    self_harm: float = 0.0
    self_harm_intent: float = 0.0
    self_harm_instructions: float = 0.0
    sexual: float = 0.0
    sexual_minors: float = 0.0
    violence: float = 0.0
    violence_graphic: float = 0.0


class ModerationResult(BaseModel):
    flagged: bool = False
    categories: ModerationCategories = ModerationCategories()
    category_scores: ModerationCategoryScores = ModerationCategoryScores()


class ModerationResponse(BaseModel):
    id: str = ""
    model: str = ""
    results: List[ModerationResult] = []


# ---------------------------------------------------------------------------
# Rerank types
# ---------------------------------------------------------------------------

class RerankResult(BaseModel):
    index: int = 0
    relevance_score: float = 0.0
    document: Optional[str] = None


class RerankMeta(BaseModel):
    version: Optional[str] = None


class RerankResponse(BaseModel):
    id: str = ""
    model: str = ""
    results: List[RerankResult] = []
    meta: Optional[RerankMeta] = None


# ---------------------------------------------------------------------------
# Responses API types
# ---------------------------------------------------------------------------

class ResponseOutputItem(BaseModel):
    id: str = ""
    type: str = ""
    status: str = ""
    role: Optional[str] = None
    content: Optional[List[Dict[str, Any]]] = None
    call_id: Optional[str] = None
    name: Optional[str] = None
    arguments: Optional[str] = None
    output: Optional[str] = None


class ResponseUsage(BaseModel):
    input_tokens: int = 0
    output_tokens: int = 0
    total_tokens: int = 0


class ResponseObject(BaseModel):
    id: str = ""
    object: str = ""
    created_at: int = 0
    status: str = ""
    model: str = ""
    output: List[ResponseOutputItem] = []
    usage: Optional[ResponseUsage] = None
    metadata: Dict[str, str] = {}
    temperature: float = 0.0
    top_p: float = 0.0
    max_output_tokens: int = 0
    previous_response_id: Optional[str] = None


class DeleteResponseResult(BaseModel):
    id: str = ""
    object: str = ""
    deleted: bool = False


class ListResponsesResult(BaseModel):
    data: List[ResponseObject] = []
    first_id: str = ""
    last_id: str = ""
    has_more: bool = False
