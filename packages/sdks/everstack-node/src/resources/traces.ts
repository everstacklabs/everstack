/**
 * Traces resource
 *
 * Wraps the TracesService gRPC API: list/stream traces, fetch span trees,
 * query rich traces, performance breakdowns, resource utilization, and
 * trace scores.
 *
 * The streaming endpoints (`list`, `streamObservations`) return AsyncIterables
 * — `for await (const trace of client.traces.list({...}))`.
 *
 * @example
 * ```typescript
 * const client = new Everstack({ apiKey: "pk_..." });
 *
 * // Last hour of traces
 * for await (const trace of client.traces.list({ limit: 100 })) {
 *   console.log(trace.traceId, trace.totalCost);
 * }
 *
 * // Inspect a specific trace
 * const tree = await client.traces.getTree({ traceId: "abc123" });
 *
 * // Score a trace from human review
 * await client.traces.scores.create({
 *   traceId: "abc123",
 *   name: "quality",
 *   source: "ANNOTATION",
 *   dataType: "NUMERIC",
 *   numericValue: 0.9,
 *   comment: "Followed the rubric.",
 * });
 * ```
 */

import type { Client } from "@connectrpc/connect";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { TracesService } from "@everstack/proto/everstack/traces/v1/traces_service_pb.js";
import { AsyncLocalStorage } from "node:async_hooks";
import { randomUUID } from "node:crypto";

import { fromConnectError } from "../errors.js";
import { OtelSpan, type OtelSpanOptions } from "../tracing/otel-span.js";

/**
 * Config the Traces resource needs to emit OTLP spans directly to the instance's
 * `/v1/traces` receiver (separate from the Connect/gRPC transport used for the
 * query and overlay APIs).
 */
export interface TraceEmitConfig {
  baseUrl: string;
  apiKey: string;
  headers?: Record<string, string>;
}

/**
 * Canonical fix-attempt verdict values.
 *
 * - `win`:        attempt achieved its goal (test passed, bug fixed)
 * - `fail`:       change was made but the goal was not reached
 * - `draw`:       change was made, behavioral outcome unchanged
 * - `no_change`:  no edit emitted (loop / refusal / wasted iterations)
 */
export type FixAttemptVerdict = "win" | "fail" | "draw" | "no_change";

/** Reserved score name validated server-side. */
export const FIX_ATTEMPT_VERDICT_NAME = "fix_attempt_verdict";

type TracesClient = Client<typeof TracesService>;

type MethodInput<K extends keyof TracesClient> = Parameters<TracesClient[K]>[0];
type MethodOptions<K extends keyof TracesClient> = Parameters<
  TracesClient[K]
>[1];
type MethodOutput<K extends keyof TracesClient> = Awaited<
  ReturnType<TracesClient[K]>
>;

export interface TraceSpanOptions {
  traceId: string;
  name: string;
  type?: "SPAN" | "GENERATION" | "EVENT" | "CHAIN" | "TOOL" | string;
  parentObservationId?: string;
  source?: "SDK" | "API" | "USER" | string;
  level?: "DEBUG" | "DEFAULT" | "WARNING" | "ERROR" | string;
  statusMessage?: string;
  model?: string;
  input?: unknown;
  output?: unknown;
  inputMimeType?: string;
  outputMimeType?: string;
  inputTokens?: number | bigint;
  outputTokens?: number | bigint;
  totalTokens?: number | bigint;
  inputCost?: number;
  outputCost?: number;
  totalCost?: number;
  metadata?: Record<string, string>;
  tags?: string[];
}

export interface CaptureOptions {
  /** LLM provider (e.g. "openai", "anthropic"); sets the issue's provider facet. */
  provider?: string;
  /** Model id; sets the issue's model facet. */
  model?: string;
  /** HTTP status code, if the failure came from an HTTP call. */
  statusCode?: number;
  /** Group the capture into an existing trace; defaults to a fresh trace id. */
  traceId?: string;
  /** Display name for the captured event; defaults to the error type. */
  name?: string;
  /** Arbitrary extra tags recorded on the issue's events. */
  context?: Record<string, string>;
}

export class TraceSpan {
  readonly id: string;
  readonly startedAt: Date;
  options: TraceSpanOptions;

  constructor(id: string, options: TraceSpanOptions) {
    this.id = id;
    this.startedAt = new Date();
    this.options = { ...options };
  }

  setInput(input: unknown, mimeType?: string) {
    const payload = serializeTracePayload(input, mimeType);
    this.options.input = payload.data;
    this.options.inputMimeType = payload.mimeType;
  }

  setOutput(output: unknown, mimeType?: string) {
    const payload = serializeTracePayload(output, mimeType);
    this.options.output = payload.data;
    this.options.outputMimeType = payload.mimeType;
  }

  setMetadata(metadata: Record<string, string>) {
    this.options.metadata = { ...(this.options.metadata ?? {}), ...metadata };
  }

  setTags(tags: string[]) {
    this.options.tags = tags;
  }
}

const currentObservationId = new AsyncLocalStorage<string>();

export class Traces {
  /** Raw generated Connect client for advanced usage */
  readonly raw: TracesClient;

  /**
   * Score-related operations on a trace. Scores can come from manual
   * annotation, an API call, or an LLM-as-judge eval run.
   */
  readonly scores = {
    list: (
      request: MethodInput<"getTraceScores">,
      options?: MethodOptions<"getTraceScores">,
    ) => this._call("getTraceScores", request, options),
    create: (
      request: MethodInput<"createScore">,
      options?: MethodOptions<"createScore">,
    ) => this._call("createScore", request, options),
    delete: (
      request: MethodInput<"deleteScore">,
      options?: MethodOptions<"deleteScore">,
    ) => this._call("deleteScore", request, options),

    /**
     * One-line helper to label a trace with the canonical fix-attempt
     * verdict (`win` | `fail` | `draw` | `no_change`). Wraps `create()`
     * with the reserved score name and `CATEGORICAL` data type so the
     * server validator accepts it.
     *
     * @example
     * ```typescript
     * // After running your test suite against the agent's output:
     * await client.traces.scores.verdict({
     *   traceId,
     *   verdict: testsPass ? "win" : "fail",
     *   source: "API",
     *   comment: "ci-run #4821",
     * });
     * ```
     */
    verdict: (
      input: {
        traceId: string;
        verdict: FixAttemptVerdict;
        source?: "ANNOTATION" | "API" | "EVAL";
        observationId?: string;
        comment?: string;
      },
      options?: MethodOptions<"createScore">,
    ) =>
      this._call(
        "createScore",
        {
          traceId: input.traceId,
          name: FIX_ATTEMPT_VERDICT_NAME,
          dataType: "CATEGORICAL",
          source: input.source ?? "API",
          stringValue: input.verdict,
          observationId: input.observationId,
          comment: input.comment,
        } as MethodInput<"createScore">,
        options,
      ),
  };

  /**
   * Append-only trace presentation overlays. These do not mutate raw OTEL
   * spans; they render on top of the immutable trace data.
   */
  readonly overlays = {
    get: (
      request: MethodInput<"getTraceOverlay">,
      options?: MethodOptions<"getTraceOverlay">,
    ) => this._call("getTraceOverlay", request, options),
    update: (
      request: MethodInput<"updateTraceOverlay">,
      options?: MethodOptions<"updateTraceOverlay">,
    ) => this._call("updateTraceOverlay", request, options),
  };

  /**
   * User/API-authored observations that render alongside raw spans.
   */
  readonly customObservations = {
    create: (
      request: MethodInput<"createCustomObservation">,
      options?: MethodOptions<"createCustomObservation">,
    ) => this._call("createCustomObservation", request, options),
    createBatch: (
      request: MethodInput<"batchCreateCustomObservations">,
      options?: MethodOptions<"batchCreateCustomObservations">,
    ) => this._call("batchCreateCustomObservations", request, options),
    list: (
      request: MethodInput<"listCustomObservations">,
      options?: MethodOptions<"listCustomObservations">,
    ) => this._call("listCustomObservations", request, options),
  };

  readonly annotations = {
    create: (
      request: MethodInput<"createTraceAnnotation">,
      options?: MethodOptions<"createTraceAnnotation">,
    ) => this._call("createTraceAnnotation", request, options),
    list: (
      request: MethodInput<"listTraceAnnotations">,
      options?: MethodOptions<"listTraceAnnotations">,
    ) => this._call("listTraceAnnotations", request, options),
  };

  /**
   * Workflow-level analysis.
   */
  readonly workflow = {
    getMetrics: (
      request: MethodInput<"getWorkflowMetrics">,
      options?: MethodOptions<"getWorkflowMetrics">,
    ) => this._call("getWorkflowMetrics", request, options),
  };

  /**
   * Observation-level queries (step-ordered, full I/O).
   */
  readonly observations = {
    listByStep: (
      request: MethodInput<"listObservationsByStep">,
      options?: MethodOptions<"listObservationsByStep">,
    ) => this._call("listObservationsByStep", request, options),
    getIO: (
      request: MethodInput<"getObservationIO">,
      options?: MethodOptions<"getObservationIO">,
    ) => this._call("getObservationIO", request, options),
  };

  /**
   * Performance & resource analysis for a specific trace.
   */
  readonly performance = {
    breakdown: (
      request: MethodInput<"getPerformanceBreakdown">,
      options?: MethodOptions<"getPerformanceBreakdown">,
    ) => this._call("getPerformanceBreakdown", request, options),
    utilization: (
      request: MethodInput<"getResourceUtilization">,
      options?: MethodOptions<"getResourceUtilization">,
    ) => this._call("getResourceUtilization", request, options),
  };

  /** @internal Config for the OTLP span emitter; undefined disables startSpan. */
  private readonly emitConfig?: TraceEmitConfig;

  /** @internal */
  constructor(client: TracesClient, emitConfig?: TraceEmitConfig) {
    this.raw = client;
    this.emitConfig = emitConfig;
  }

  /**
   * Start a self-contained OTLP span that is emitted directly to the instance's
   * trace store. Unlike {@link withSpan} / {@link wrap} (append-only overlays
   * that render only on top of an existing trace), a span started here is a
   * first-class trace: it appears in the trace list and renders on its own.
   *
   * Populate it with setters, then `await span.end()` to flush. Use
   * {@link OtelSpan.startChild} to build a span tree.
   *
   * @example
   * ```typescript
   * const span = client.traces.startSpan({ name: "chat", type: "GENERATION", model: "gpt-4o" });
   * span.setMessages([{ role: "user", content: "Hello" }]);
   * span.setChoices([{ index: 0, message: { role: "assistant", content: "Hi!" } }]);
   * await span.end();
   * console.log(span.traceId); // hex id, for building a trace URL
   * ```
   */
  startSpan(options: OtelSpanOptions): OtelSpan {
    if (!this.emitConfig) {
      throw new Error(
        "traces.startSpan requires a configured client (apiKey + baseUrl). " +
          "Create the client via `new Everstack({ apiKey, baseUrl })`.",
      );
    }
    return new OtelSpan(options, this.emitSpan, "everstack-sdk");
  }

  /** @internal Flush one finished span to {baseUrl}/v1/traces. */
  private emitSpan = async (span: OtelSpan): Promise<void> => {
    const cfg = this.emitConfig!;
    const url = `${cfg.baseUrl.replace(/\/+$/, "")}/v1/traces`;
    const res = await fetch(url, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        // OTLP ingest authenticates via Bearer / x-evs-api-key on its own
        // ingest path, distinct from the gateway's Connect RPC auth.
        Authorization: `Bearer ${cfg.apiKey}`,
        "x-evs-api-key": cfg.apiKey,
        ...cfg.headers,
      },
      body: JSON.stringify(span.toExportRequest()),
    });
    if (!res.ok) {
      const body = await res.text().catch(() => "");
      throw new Error(
        `failed to emit span to ${url}: HTTP ${res.status} ${res.statusText} ${body.slice(0, 300)}`,
      );
    }
  };

  private async _call<K extends keyof TracesClient>(
    method: K,
    request: MethodInput<K>,
    options?: MethodOptions<K>,
  ): Promise<MethodOutput<K>> {
    try {
      const fn = this.raw[method] as (
        req: MethodInput<K>,
        opt?: MethodOptions<K>,
      ) => Promise<MethodOutput<K>>;
      return await fn(request, options);
    } catch (error) {
      throw fromConnectError(error);
    }
  }

  /**
   * Server-streaming list of traces. Yields each trace as it arrives.
   * When `live=true` (the default unless `to` is set), the iterator stays
   * open and yields new traces in real time — break out of the loop to stop.
   *
   * @example
   * ```typescript
   * const ctrl = new AbortController();
   * for await (const trace of client.traces.list({ limit: 50 }, { signal: ctrl.signal })) {
   *   if (trace.errorCount > 0) {
   *     ctrl.abort();
   *     break;
   *   }
   * }
   * ```
   */
  list(
    request: MethodInput<"listTraces">,
    options?: MethodOptions<"listTraces">,
  ) {
    return this.raw.listTraces(request, options);
  }

  /**
   * Fetch a single trace summary by ID.
   */
  get(
    request: MethodInput<"getTrace">,
    options?: MethodOptions<"getTrace">,
  ): Promise<MethodOutput<"getTrace">> {
    return this._call("getTrace", request, options);
  }

  /**
   * Fetch every span for a trace as a flat list.
   */
  getSpans(
    request: MethodInput<"getTraceByID">,
    options?: MethodOptions<"getTraceByID">,
  ): Promise<MethodOutput<"getTraceByID">> {
    return this._call("getTraceByID", request, options);
  }

  /**
   * Fetch the trace as a hierarchical span tree (root span with nested children).
   */
  getTree(
    request: MethodInput<"getTraceTree">,
    options?: MethodOptions<"getTraceTree">,
  ): Promise<MethodOutput<"getTraceTree">> {
    return this._call("getTraceTree", request, options);
  }

  /**
   * Rich (Langfuse-compatible) representation of a trace, with normalised
   * observations.
   */
  getRich(
    request: MethodInput<"getRichTrace">,
    options?: MethodOptions<"getRichTrace">,
  ): Promise<MethodOutput<"getRichTrace">> {
    return this._call("getRichTrace", request, options);
  }

  /**
   * Run a function inside a custom SDK span. The span write is append-only and
   * can target a trace id before raw OTEL spans for that trace have arrived.
   */
  async withSpan<T>(
    options: TraceSpanOptions,
    fn: (span: TraceSpan) => T | Promise<T>,
  ): Promise<T> {
    const parentObservationId =
      options.parentObservationId ?? currentObservationId.getStore();
    const span = new TraceSpan(randomUUID(), {
      ...options,
      parentObservationId,
      source: options.source ?? "SDK",
    });

    return currentObservationId.run(span.id, async () => {
      let thrown: unknown;
      try {
        return await fn(span);
      } catch (error) {
        thrown = error;
        throw error;
      } finally {
        await this.customObservations.create(traceSpanToRequest(span, thrown));
      }
    });
  }

  /**
   * Wrap a function so every invocation records a custom SDK span.
   */
  wrap<Args extends unknown[], Result>(
    options: Omit<TraceSpanOptions, "input" | "output">,
    fn: (...args: Args) => Result | Promise<Result>,
  ): (...args: Args) => Promise<Result> {
    return async (...args: Args) =>
      this.withSpan(
        { ...options, input: args.length === 1 ? args[0] : args },
        async (span) => {
          const result = await fn(...args);
          span.setOutput(result);
          return result;
        },
      );
  }

  /**
   * Report a caught error so it surfaces as an Issue.
   *
   * Records an ERROR-level observation; the backend groups recurring failures
   * by a normalized signature of the error message into a single Issue
   * (first/last seen, count, trend, lifecycle). `provider` / `model` /
   * `statusCode` populate the issue's provider, model and category facets;
   * `context` adds arbitrary tags.
   *
   * @example
   * ```typescript
   * try {
   *   await openai.chat.completions.create(...);
   * } catch (err) {
   *   client.captureException(err, { provider: "openai", model: "gpt-4o" });
   *   throw err;
   * }
   * ```
   */
  captureException(
    error: unknown,
    options: CaptureOptions = {},
  ): Promise<MethodOutput<"createCustomObservation">> {
    const err = error instanceof Error ? error : undefined;
    const message = err ? err.message : String(error);
    const errorType = err ? err.name : "Error";
    const now = new Date();
    return this.customObservations.create({
      id: randomUUID(),
      traceId: options.traceId ?? randomUUID(),
      name: options.name ?? errorType,
      type: "EVENT",
      source: "SDK",
      startTime: timestampFromDate(now),
      endTime: timestampFromDate(now),
      duration: 0n,
      level: "ERROR",
      statusMessage: message || errorType,
      model: options.model,
      metadata: issueMetadata(errorType, options),
      tags: [],
    });
  }

  /**
   * Report a free-form failure message as an Issue. Only `ERROR`-level captures
   * become Issues; lower levels are recorded as plain observations on the trace.
   */
  captureMessage(
    message: string,
    options: CaptureOptions & {
      level?: "DEBUG" | "DEFAULT" | "WARNING" | "ERROR";
    } = {},
  ): Promise<MethodOutput<"createCustomObservation">> {
    const now = new Date();
    return this.customObservations.create({
      id: randomUUID(),
      traceId: options.traceId ?? randomUUID(),
      name: options.name ?? "message",
      type: "EVENT",
      source: "SDK",
      startTime: timestampFromDate(now),
      endTime: timestampFromDate(now),
      duration: 0n,
      level: options.level ?? "ERROR",
      statusMessage: message,
      model: options.model,
      metadata: issueMetadata(undefined, options),
      tags: [],
    });
  }

  /**
   * Paginated list of rich traces matching the given filter (session, user,
   * thread, environment, tags, model, provider).
   */
  listRich(
    request: MethodInput<"listRichTraces">,
    options?: MethodOptions<"listRichTraces">,
  ): Promise<MethodOutput<"listRichTraces">> {
    return this._call("listRichTraces", request, options);
  }

  /**
   * Server-streaming enhanced observations for a trace, optionally filtered
   * by workflow id / nodes / step range / observation types.
   *
   * @example
   * ```typescript
   * for await (const obs of client.traces.streamObservations({ traceId: "abc" })) {
   *   console.log(obs.step, obs.node, obs.latency);
   * }
   * ```
   */
  streamObservations(
    request: MethodInput<"streamEnhancedObservations">,
    options?: MethodOptions<"streamEnhancedObservations">,
  ) {
    return this.raw.streamEnhancedObservations(request, options);
  }

  /**
   * Pre-aggregated trace statistics (per-trace span counts, latency
   * percentiles, error rates, resource peaks).
   */
  getAnalytics(
    request: MethodInput<"getTraceAnalytics">,
    options?: MethodOptions<"getTraceAnalytics">,
  ): Promise<MethodOutput<"getTraceAnalytics">> {
    return this._call("getTraceAnalytics", request, options);
  }

  /**
   * Aggregated workflow performance metrics for a trace.
   */
  getWorkflowMetrics(
    request: MethodInput<"getWorkflowMetrics">,
    options?: MethodOptions<"getWorkflowMetrics">,
  ): Promise<MethodOutput<"getWorkflowMetrics">> {
    return this._call("getWorkflowMetrics", request, options);
  }

  /**
   * List observations for a trace ordered by execution step, optionally
   * filtered by workflow / node / step range / type.
   */
  listObservationsByStep(
    request: MethodInput<"listObservationsByStep">,
    options?: MethodOptions<"listObservationsByStep">,
  ): Promise<MethodOutput<"listObservationsByStep">> {
    return this._call("listObservationsByStep", request, options);
  }

  /**
   * Retrieve full input/output data for specific observations.
   */
  getObservationIO(
    request: MethodInput<"getObservationIO">,
    options?: MethodOptions<"getObservationIO">,
  ): Promise<MethodOutput<"getObservationIO">> {
    return this._call("getObservationIO", request, options);
  }
}

function traceSpanToRequest(
  span: TraceSpan,
  error: unknown,
): MethodInput<"createCustomObservation"> {
  const endedAt = new Date();
  const options = span.options;
  const input = serializeTracePayload(options.input, options.inputMimeType);
  const output = serializeTracePayload(options.output, options.outputMimeType);
  const statusMessage =
    error instanceof Error ? error.message : options.statusMessage;

  return {
    id: span.id,
    traceId: options.traceId,
    parentObservationId: options.parentObservationId,
    name: options.name,
    type: options.type ?? "SPAN",
    source: options.source ?? "SDK",
    startTime: timestampFromDate(span.startedAt),
    endTime: timestampFromDate(endedAt),
    duration:
      BigInt(Math.max(0, endedAt.getTime() - span.startedAt.getTime())) *
      1_000_000n,
    level: error ? "ERROR" : (options.level ?? "DEFAULT"),
    statusMessage,
    model: options.model,
    inputData: input.data,
    outputData: output.data,
    inputMimeType: input.mimeType,
    outputMimeType: output.mimeType,
    inputTokens: toBigInt(options.inputTokens),
    outputTokens: toBigInt(options.outputTokens),
    totalTokens: toBigInt(options.totalTokens),
    inputCost: options.inputCost,
    outputCost: options.outputCost,
    totalCost: options.totalCost,
    metadata: options.metadata ?? {},
    tags: options.tags ?? [],
  };
}

/**
 * Build the observation metadata that the Issues backend reads as span
 * attributes: `llm.provider`, `error.type` and `http.status_code` drive the
 * issue's provider/model/category facets; `context` adds free-form tags.
 */
function issueMetadata(
  errorType: string | undefined,
  options: CaptureOptions,
): Record<string, string> {
  const md: Record<string, string> = {};
  if (options.provider) md["llm.provider"] = options.provider;
  if (errorType) md["error.type"] = errorType;
  if (options.statusCode !== undefined)
    md["http.status_code"] = String(options.statusCode);
  if (options.context) Object.assign(md, options.context);
  return md;
}

function serializeTracePayload(value: unknown, mimeType?: string) {
  if (value === undefined) {
    return { data: undefined, mimeType: undefined };
  }
  if (typeof value === "string") {
    return { data: value, mimeType: mimeType ?? "text/plain" };
  }
  return {
    data: JSON.stringify(value),
    mimeType: mimeType ?? "application/json",
  };
}

function toBigInt(value: number | bigint | undefined): bigint | undefined {
  if (value === undefined) {
    return undefined;
  }
  return typeof value === "bigint" ? value : BigInt(value);
}
