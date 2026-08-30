"""Audio resource — TTS, transcription, translation."""

from __future__ import annotations

import base64
from typing import Any, Dict, Optional, List

from .._transport import Transport, AsyncTransport
from .._types import SpeechResponse, TranscriptionResponse, TranslationResponse


class Speech:
    def __init__(self, transport: Transport) -> None:
        self._transport = transport

    def create(
        self,
        *,
        model: str,
        input: str,
        voice: str,
        response_format: Optional[str] = None,
        speed: Optional[float] = None,
    ) -> SpeechResponse:
        """Generate audio from text."""
        body: Dict[str, Any] = {"model": model, "input": input, "voice": voice}
        if response_format:
            body["response_format"] = response_format
        if speed is not None:
            body["speed"] = speed
        data = self._transport.request("POST", "/v1/audio/speech", json_body=body)
        return SpeechResponse.model_validate(data)


class Transcriptions:
    def __init__(self, transport: Transport) -> None:
        self._transport = transport

    def create(
        self,
        *,
        file: bytes,
        model: str,
        language: Optional[str] = None,
        prompt: Optional[str] = None,
        response_format: Optional[str] = None,
        temperature: Optional[float] = None,
        timestamp_granularities: Optional[List[str]] = None,
        filename: Optional[str] = None,
    ) -> TranscriptionResponse:
        """Transcribe audio to text."""
        body: Dict[str, Any] = {
            "file": base64.b64encode(file).decode(),
            "model": model,
        }
        if language:
            body["language"] = language
        if prompt:
            body["prompt"] = prompt
        if response_format:
            body["response_format"] = response_format
        if temperature is not None:
            body["temperature"] = temperature
        if timestamp_granularities:
            body["timestamp_granularities"] = timestamp_granularities
        if filename:
            body["filename"] = filename
        data = self._transport.request("POST", "/v1/audio/transcriptions", json_body=body)
        return TranscriptionResponse.model_validate(data)


class Translations:
    def __init__(self, transport: Transport) -> None:
        self._transport = transport

    def create(
        self,
        *,
        file: bytes,
        model: str,
        prompt: Optional[str] = None,
        response_format: Optional[str] = None,
        temperature: Optional[float] = None,
        filename: Optional[str] = None,
    ) -> TranslationResponse:
        """Translate audio to English text."""
        body: Dict[str, Any] = {
            "file": base64.b64encode(file).decode(),
            "model": model,
        }
        if prompt:
            body["prompt"] = prompt
        if response_format:
            body["response_format"] = response_format
        if temperature is not None:
            body["temperature"] = temperature
        if filename:
            body["filename"] = filename
        data = self._transport.request("POST", "/v1/audio/translations", json_body=body)
        return TranslationResponse.model_validate(data)


class Audio:
    speech: Speech
    transcriptions: Transcriptions
    translations: Translations

    def __init__(self, transport: Transport) -> None:
        self.speech = Speech(transport)
        self.transcriptions = Transcriptions(transport)
        self.translations = Translations(transport)


# ---------------------------------------------------------------------------
# Async variants
# ---------------------------------------------------------------------------

class AsyncSpeech:
    def __init__(self, transport: AsyncTransport) -> None:
        self._transport = transport

    async def create(self, *, model: str, input: str, voice: str, **kwargs: Any) -> SpeechResponse:
        body: Dict[str, Any] = {"model": model, "input": input, "voice": voice, **kwargs}
        data = await self._transport.request("POST", "/v1/audio/speech", json_body=body)
        return SpeechResponse.model_validate(data)


class AsyncTranscriptions:
    def __init__(self, transport: AsyncTransport) -> None:
        self._transport = transport

    async def create(self, *, file: bytes, model: str, **kwargs: Any) -> TranscriptionResponse:
        body: Dict[str, Any] = {"file": base64.b64encode(file).decode(), "model": model, **kwargs}
        data = await self._transport.request("POST", "/v1/audio/transcriptions", json_body=body)
        return TranscriptionResponse.model_validate(data)


class AsyncTranslations:
    def __init__(self, transport: AsyncTransport) -> None:
        self._transport = transport

    async def create(self, *, file: bytes, model: str, **kwargs: Any) -> TranslationResponse:
        body: Dict[str, Any] = {"file": base64.b64encode(file).decode(), "model": model, **kwargs}
        data = await self._transport.request("POST", "/v1/audio/translations", json_body=body)
        return TranslationResponse.model_validate(data)


class AsyncAudio:
    speech: AsyncSpeech
    transcriptions: AsyncTranscriptions
    translations: AsyncTranslations

    def __init__(self, transport: AsyncTransport) -> None:
        self.speech = AsyncSpeech(transport)
        self.transcriptions = AsyncTranscriptions(transport)
        self.translations = AsyncTranslations(transport)
