/**
 * Audio resource
 *
 * Provides text-to-speech, speech-to-text transcription, and audio translation.
 */

import type { Client } from "@connectrpc/connect";
import type { GatewayService } from "@everstack/proto/everstack/gateway/v1/gateway_service_pb.js";
import { create } from "@bufbuild/protobuf";
import {
  SpeechRequestSchema,
  TranscriptionRequestSchema,
  type TranscriptionResponse as ProtoTranscriptionResponse,
  TranslationRequestSchema,
  type TranslationResponse as ProtoTranslationResponse,
} from "@everstack/proto/everstack/gateway/v1/audio_pb.js";

import { fromConnectError } from "../errors.js";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface SpeechParams {
  /** TTS model to use */
  model: string;
  /** The text to generate audio for */
  input: string;
  /** The voice to use */
  voice: string;
  /** Audio format (mp3, opus, aac, flac, wav, pcm) */
  response_format?: string;
  /** Speed of the generated audio (0.25 to 4.0, default 1.0) */
  speed?: number;
}

export interface SpeechResponse {
  /** Raw audio bytes */
  audio: Uint8Array;
  /** Audio format */
  format: string;
  /** MIME content type */
  content_type: string;
  /** Duration in seconds */
  duration_seconds: number;
  /** Number of input characters */
  input_characters: number;
}

export interface TranscriptionParams {
  /** Audio file bytes */
  file: Uint8Array;
  /** STT model to use */
  model: string;
  /** Language of the audio (ISO-639-1) */
  language?: string;
  /** Optional prompt to guide the model */
  prompt?: string;
  /** Output format (json, text, srt, verbose_json, vtt) */
  response_format?: string;
  /** Sampling temperature (0 to 1) */
  temperature?: number;
  /** Timestamp granularities (word, segment) */
  timestamp_granularities?: string[];
  /** Original filename */
  filename?: string;
}

export interface TranscriptionWord {
  word: string;
  start: number;
  end: number;
}

export interface TranscriptionSegment {
  id: number;
  seek: number;
  start: number;
  end: number;
  text: string;
  tokens: number[];
  temperature: number;
  avg_logprob: number;
  compression_ratio: number;
  no_speech_prob: number;
}

export interface TranscriptionResponse {
  text: string;
  task: string;
  language: string;
  duration: number;
  words: TranscriptionWord[];
  segments: TranscriptionSegment[];
}

export interface TranslationParams {
  /** Audio file bytes */
  file: Uint8Array;
  /** Model to use */
  model: string;
  /** Optional prompt to guide the model */
  prompt?: string;
  /** Output format */
  response_format?: string;
  /** Sampling temperature */
  temperature?: number;
  /** Original filename */
  filename?: string;
}

export interface TranslationResponse {
  text: string;
  task: string;
  language: string;
  duration: number;
  segments: TranscriptionSegment[];
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function transformTranscriptionResponse(
  proto: ProtoTranscriptionResponse,
): TranscriptionResponse {
  return {
    text: proto.text,
    task: proto.task,
    language: proto.language,
    duration: proto.duration,
    words: proto.words.map((w) => ({
      word: w.word,
      start: w.start,
      end: w.end,
    })),
    segments: proto.segments.map((s) => ({
      id: s.id,
      seek: s.seek,
      start: s.start,
      end: s.end,
      text: s.text,
      tokens: Array.from(s.tokens),
      temperature: s.temperature,
      avg_logprob: s.avgLogprob,
      compression_ratio: s.compressionRatio,
      no_speech_prob: s.noSpeechProb,
    })),
  };
}

function transformTranslationResponse(
  proto: ProtoTranslationResponse,
): TranslationResponse {
  return {
    text: proto.text,
    task: proto.task,
    language: proto.language,
    duration: proto.duration,
    segments: proto.segments.map((s) => ({
      id: s.id,
      seek: s.seek,
      start: s.start,
      end: s.end,
      text: s.text,
      tokens: Array.from(s.tokens),
      temperature: s.temperature,
      avg_logprob: s.avgLogprob,
      compression_ratio: s.compressionRatio,
      no_speech_prob: s.noSpeechProb,
    })),
  };
}

// ---------------------------------------------------------------------------
// Resources
// ---------------------------------------------------------------------------

/**
 * Speech (TTS) sub-resource
 */
export class Speech {
  /** @internal */
  constructor(private readonly _client: Client<typeof GatewayService>) {}

  /**
   * Generate audio from text
   *
   * @example
   * ```typescript
   * const response = await client.audio.speech.create({
   *   model: 'tts-1',
   *   input: 'Hello, world!',
   *   voice: 'alloy',
   * });
   * // response.audio contains the raw audio bytes
   * ```
   */
  async create(params: SpeechParams): Promise<SpeechResponse> {
    try {
      const request = create(SpeechRequestSchema, {
        model: params.model,
        input: params.input,
        voice: params.voice,
        responseFormat: params.response_format ?? "",
        speed: params.speed ?? 0,
      });

      const response = await this._client.speech(request);
      return {
        audio: response.audio,
        format: response.format,
        content_type: response.contentType,
        duration_seconds: response.durationSeconds,
        input_characters: response.inputCharacters,
      };
    } catch (error) {
      throw fromConnectError(error);
    }
  }
}

/**
 * Transcriptions sub-resource
 */
export class Transcriptions {
  /** @internal */
  constructor(private readonly _client: Client<typeof GatewayService>) {}

  /**
   * Transcribe audio to text
   *
   * @example
   * ```typescript
   * const response = await client.audio.transcriptions.create({
   *   file: audioBuffer,
   *   model: 'whisper-1',
   * });
   * console.log(response.text);
   * ```
   */
  async create(params: TranscriptionParams): Promise<TranscriptionResponse> {
    try {
      const request = create(TranscriptionRequestSchema, {
        file: params.file,
        model: params.model,
        language: params.language ?? "",
        prompt: params.prompt ?? "",
        responseFormat: params.response_format ?? "",
        temperature: params.temperature ?? 0,
        timestampGranularities: params.timestamp_granularities ?? [],
        filename: params.filename ?? "",
      });

      const response = await this._client.transcription(request);
      return transformTranscriptionResponse(response);
    } catch (error) {
      throw fromConnectError(error);
    }
  }
}

/**
 * Translations sub-resource
 */
export class Translations {
  /** @internal */
  constructor(private readonly _client: Client<typeof GatewayService>) {}

  /**
   * Translate audio to English text
   *
   * @example
   * ```typescript
   * const response = await client.audio.translations.create({
   *   file: audioBuffer,
   *   model: 'whisper-1',
   * });
   * console.log(response.text);
   * ```
   */
  async create(params: TranslationParams): Promise<TranslationResponse> {
    try {
      const request = create(TranslationRequestSchema, {
        file: params.file,
        model: params.model,
        prompt: params.prompt ?? "",
        responseFormat: params.response_format ?? "",
        temperature: params.temperature ?? 0,
        filename: params.filename ?? "",
      });

      const response = await this._client.translation(request);
      return transformTranslationResponse(response);
    } catch (error) {
      throw fromConnectError(error);
    }
  }
}

/**
 * Audio resource with speech, transcriptions, and translations sub-resources
 */
export class Audio {
  readonly speech: Speech;
  readonly transcriptions: Transcriptions;
  readonly translations: Translations;

  /** @internal */
  constructor(client: Client<typeof GatewayService>) {
    this.speech = new Speech(client);
    this.transcriptions = new Transcriptions(client);
    this.translations = new Translations(client);
  }
}
