/**
 * OTLP span emitter
 *
 * Emits self-contained OpenTelemetry spans directly into the instance's trace
 * store (the `otel_traces` table) via the OTLP/HTTP receiver at
 * `POST {baseUrl}/v1/traces`. Unlike custom observations (append-only overlays
 * that only render grafted onto an existing base trace), a span emitted here is
 * a first-class trace: it shows up in the trace list and renders in the detail
 * sheet on its own.
 *
 * The setters are a transport-agnostic attribute bag: they map friendly names
 * onto the exact span-attribute keys the trace sheet reads
 * (`input.value`, `output.value`, `llm.request.messages`, `llm.response.choices`).
 * Keeping them decoupled from the HTTP sink means a future OpenTelemetry-backed
 * adapter can reuse them unchanged.
 *
 * Wire encoding note: the receiver decodes JSON with google `protojson`, where
 * proto `bytes` fields (trace_id / span_id) are base64, NOT hex. IDs are held as
 * hex internally (so `span.traceId` is URL/query friendly) and converted to
 * base64 only at export time.
 */

import { randomBytes } from "node:crypto";

/** OTLP span kind (numeric proto values). */
export type SpanKind =
  | "INTERNAL"
  | "SERVER"
  | "CLIENT"
  | "PRODUCER"
  | "CONSUMER";

const SPAN_KIND_CODE: Record<SpanKind, number> = {
  INTERNAL: 1,
  SERVER: 2,
  CLIENT: 3,
  PRODUCER: 4,
  CONSUMER: 5,
};

/**
 * Observation type. `GENERATION` marks an LLM call — it is stamped as the
 * `observation.type` attribute the trace sheet uses to pick the primary span
 * for input/output extraction.
 */
export type ObservationType =
  | "SPAN"
  | "GENERATION"
  | "EVENT"
  | "CHAIN"
  | "TOOL"
  | "RETRIEVER"
  | "EMBEDDING";

export interface OtelSpanOptions {
  /** Span display name. */
  name: string;
  /** Semantic type; `GENERATION` lights up the LLM input/output panels. */
  type?: ObservationType;
  /**
   * Attach to an existing trace by hex trace id. Omit to start a new trace
   * (a fresh 16-byte id). Ignored when `parent` is set (inherited from it).
   */
  traceId?: string;
  /** Parent span, for building span trees. Inherits the parent's trace id. */
  parent?: OtelSpan;
  /** OTLP span kind. Defaults to CLIENT for GENERATION, else INTERNAL. */
  kind?: SpanKind;
  /** Model id; sets llm.request.model / llm.response.model / gen_ai.request.model. */
  model?: string;
  /** Arbitrary span attributes set up front (verbatim keys). */
  attributes?: Record<string, string>;
}

type AnyValue = { stringValue: string } | { intValue: string };

const strVal = (s: string): AnyValue => ({ stringValue: s });
const intVal = (n: number | bigint): AnyValue => ({ intValue: String(n) });

const hexId = (bytes: number) => randomBytes(bytes).toString("hex");
const hexToBase64 = (hex: string) => Buffer.from(hex, "hex").toString("base64");
const nowNanos = () => BigInt(Date.now()) * 1_000_000n;

const serialize = (value: unknown): string => {
  if (typeof value === "string") return value;
  const serialized = JSON.stringify(value, (_key, nested) =>
    typeof nested === "bigint" ? nested.toString() : nested,
  );
  return serialized ?? String(value);
};

/** Minimal OTLP JSON shapes (protojson field names, camelCase). */
export interface OtlpSpan {
  traceId: string;
  spanId: string;
  parentSpanId?: string;
  name: string;
  kind: number;
  startTimeUnixNano: string;
  endTimeUnixNano: string;
  attributes: { key: string; value: AnyValue }[];
  status: { code: number; message?: string };
}

export interface OtlpExportRequest {
  resourceSpans: {
    resource: { attributes: { key: string; value: AnyValue }[] };
    scopeSpans: {
      scope: { name: string; version?: string };
      spans: OtlpSpan[];
    }[];
  }[];
}

/** Sink that flushes a finished span. Injected by the Traces resource. */
export type SpanSink = (span: OtelSpan) => Promise<void>;

/**
 * A single emittable OTLP span. Create via `client.traces.startSpan(...)`,
 * populate with setters, then `await span.end()` to flush it.
 */
export class OtelSpan {
  /** Hex trace id (32 chars). Stable for the whole trace; use for URLs/queries. */
  readonly traceId: string;
  /** Hex span id (16 chars). */
  readonly spanId: string;
  readonly parentSpanId?: string;
  readonly name: string;

  private readonly kind: number;
  private readonly startTimeNanos: bigint;
  private endTimeNanos?: bigint;
  private readonly attrs = new Map<string, AnyValue>();
  private statusCode = 1; // OK
  private statusMessage?: string;
  private ended = false;

  /** @internal */
  constructor(
    options: OtelSpanOptions,
    private readonly sink: SpanSink,
    private readonly serviceName: string,
  ) {
    this.name = options.name;
    this.traceId = options.parent?.traceId ?? options.traceId ?? hexId(16);
    this.spanId = hexId(8);
    this.parentSpanId = options.parent?.spanId;
    this.kind =
      SPAN_KIND_CODE[
        options.kind ?? (options.type === "GENERATION" ? "CLIENT" : "INTERNAL")
      ];
    this.startTimeNanos = nowNanos();

    if (options.type) this.attrs.set("observation.type", strVal(options.type));
    if (options.model) this.setModel(options.model);
    for (const [k, v] of Object.entries(options.attributes ?? {})) {
      this.attrs.set(k, strVal(v));
    }
  }

  /**
   * Request-side content. A plain string renders as flat markdown; a
   * message-shaped payload prefers {@link setMessages}. Maps to `input.value`.
   */
  setInput(value: unknown): this {
    this.attrs.set("input.value", strVal(serialize(value)));
    return this;
  }

  /**
   * Response-side content. A plain string renders as flat markdown. Maps to
   * `output.value`.
   */
  setOutput(value: unknown): this {
    this.attrs.set("output.value", strVal(serialize(value)));
    return this;
  }

  /**
   * OpenAI-shaped request messages (role/content, tool_calls, ...). Drives the
   * structured conversation view. Maps to `llm.request.messages`.
   */
  setMessages(messages: unknown): this {
    this.attrs.set("llm.request.messages", strVal(serialize(messages)));
    return this;
  }

  /**
   * OpenAI-shaped response choices. Reasoning renders when a choice's `message`
   * carries `reasoning_content` (there is no standalone reasoning attribute).
   * Maps to `llm.response.choices`.
   */
  setChoices(choices: unknown): this {
    this.attrs.set("llm.response.choices", strVal(serialize(choices)));
    return this;
  }

  /** Sets the model id across the keys the UI reads for model attribution. */
  setModel(model: string): this {
    this.attrs.set("llm.request.model", strVal(model));
    this.attrs.set("llm.response.model", strVal(model));
    this.attrs.set("gen_ai.request.model", strVal(model));
    return this;
  }

  /** Token usage. Maps to gen_ai.usage.* (input/output/total). */
  setUsage(usage: {
    inputTokens?: number;
    outputTokens?: number;
    totalTokens?: number;
  }): this {
    if (usage.inputTokens !== undefined)
      this.attrs.set("gen_ai.usage.input_tokens", intVal(usage.inputTokens));
    if (usage.outputTokens !== undefined)
      this.attrs.set("gen_ai.usage.output_tokens", intVal(usage.outputTokens));
    if (usage.totalTokens !== undefined)
      this.attrs.set("gen_ai.usage.total_tokens", intVal(usage.totalTokens));
    return this;
  }

  /** Set an arbitrary span attribute (verbatim key). Escape hatch. */
  setAttribute(key: string, value: string | number): this {
    this.attrs.set(
      key,
      typeof value === "number" ? intVal(value) : strVal(value),
    );
    return this;
  }

  /** Mark the span as failed: status ERROR + exception attributes. */
  setError(error: unknown): this {
    const err = error instanceof Error ? error : undefined;
    this.statusCode = 2; // ERROR
    this.statusMessage = err ? err.message : String(error);
    this.attrs.set("observation.level", strVal("ERROR"));
    this.attrs.set("exception.type", strVal(err ? err.name : "Error"));
    this.attrs.set("exception.message", strVal(this.statusMessage));
    return this;
  }

  /**
   * Start a child span sharing this span's trace id, parented to it. Flush it
   * with its own `.end()`.
   */
  startChild(options: Omit<OtelSpanOptions, "parent" | "traceId">): OtelSpan {
    return new OtelSpan(
      { ...options, parent: this },
      this.sink,
      this.serviceName,
    );
  }

  /** OTLP span object with base64-encoded ids (protojson-ready). */
  toOtlpSpan(): OtlpSpan {
    const end = this.endTimeNanos ?? nowNanos();
    return {
      traceId: hexToBase64(this.traceId),
      spanId: hexToBase64(this.spanId),
      ...(this.parentSpanId
        ? { parentSpanId: hexToBase64(this.parentSpanId) }
        : {}),
      name: this.name,
      kind: this.kind,
      startTimeUnixNano: this.startTimeNanos.toString(),
      endTimeUnixNano: end.toString(),
      attributes: [...this.attrs.entries()].map(([key, value]) => ({
        key,
        value,
      })),
      status: {
        code: this.statusCode,
        ...(this.statusMessage ? { message: this.statusMessage } : {}),
      },
    };
  }

  /** Full OTLP ExportTraceServiceRequest wrapping just this span. */
  toExportRequest(): OtlpExportRequest {
    return {
      resourceSpans: [
        {
          resource: {
            attributes: [
              { key: "service.name", value: strVal(this.serviceName) },
            ],
          },
          scopeSpans: [
            {
              scope: { name: this.serviceName, version: "1.0.0" },
              spans: [this.toOtlpSpan()],
            },
          ],
        },
      ],
    };
  }

  /**
   * Finish the span and flush it to the instance. Idempotent: a second call is
   * a no-op. Optionally set final output first.
   */
  async end(output?: unknown): Promise<void> {
    if (this.ended) return;
    if (output !== undefined) this.setOutput(output);
    this.endTimeNanos = nowNanos();
    this.ended = true;
    await this.sink(this);
  }
}
