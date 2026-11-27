import type { OtelSpan, OtelSpanOptions } from "@everstack/node";

import type { EverstackClient } from "./types.js";

export type TraceTree = Awaited<
  ReturnType<EverstackClient["traces"]["getTree"]>
>;
export type TraceTreeNode = NonNullable<TraceTree["root"]>;

export type SpanRequirement = {
  pattern: string;
  minimum?: number;
};

const TRACE_POLL_INTERVAL_MS = 500;
const MAX_TRACE_VALUE_LENGTH = 20_000;

export function traceHeaders(span: Pick<OtelSpan, "traceId" | "spanId">): {
  traceparent: string;
} {
  return { traceparent: formatTraceparent(span.traceId, span.spanId) };
}

export function formatTraceparent(traceId: string, spanId: string): string {
  if (!/^[0-9a-f]{32}$/i.test(traceId) || /^0+$/.test(traceId)) {
    throw new Error("Trace ID must be 32 non-zero hexadecimal characters.");
  }
  if (!/^[0-9a-f]{16}$/i.test(spanId) || /^0+$/.test(spanId)) {
    throw new Error("Span ID must be 16 non-zero hexadecimal characters.");
  }
  return `00-${traceId.toLowerCase()}-${spanId.toLowerCase()}-01`;
}

export async function tracedStep<T>(
  parent: OtelSpan,
  options: Omit<OtelSpanOptions, "parent" | "traceId">,
  operation: (span: OtelSpan) => Promise<T>,
): Promise<T> {
  const span = parent.startChild(options);
  try {
    return await operation(span);
  } catch (error) {
    span.setError(error);
    throw error;
  } finally {
    await span.end();
  }
}

export async function waitForTrace(
  client: EverstackClient,
  traceId: string,
  rootSpanId: string,
  waitMs: number,
  requirements: SpanRequirement[],
): Promise<TraceTree> {
  const deadline = Date.now() + waitMs;
  let lastError: Error | undefined;

  do {
    try {
      const tree = await client.traces.getTree({ traceId });
      if (!tree.root?.span) {
        lastError = new Error(
          "Trace exists but its root span is not available yet.",
        );
      } else if (tree.root.span.spanId !== rootSpanId) {
        lastError = new Error(
          `Trace root is ${tree.root.span.spanId}, expected application root ${rootSpanId}.`,
        );
      } else {
        const observed = collectTraceSpanNames(tree.root);
        const missing = missingSpanRequirements(observed, requirements);
        if (missing.length === 0) return tree;
        lastError = new Error(
          `Trace is missing expected spans: ${missing.join(", ")}`,
        );
      }
    } catch (error) {
      lastError = toError(error);
    }
    await delay(TRACE_POLL_INTERVAL_MS);
  } while (Date.now() < deadline);

  throw new Error(
    `Trace ${traceId} was not complete within ${waitMs}ms: ${lastError?.message ?? "unknown error"}`,
  );
}

export function requiredTraceSpans(keepResources: boolean): SpanRequirement[] {
  const requirements: SpanRequirement[] = [
    { pattern: "node-sdk.e2e.agent-sandbox" },
    { pattern: "app.gateway.chat.completions" },
    { pattern: "gateway.chat.completion" },
    { pattern: "provider.*.chat", minimum: 2 },
    { pattern: "app.agent.create" },
    { pattern: "app.agent.session.create" },
    { pattern: "app.agent.turn" },
    { pattern: "agents.turn.stream" },
    { pattern: "agent.session" },
    { pattern: "agent.turn.*" },
    { pattern: "app.agent.tool.sandbox_execute" },
    { pattern: "agent.tool.sandbox_execute" },
    { pattern: "sandbox.exec.*" },
  ];
  if (!keepResources) {
    requirements.push(
      { pattern: "app.cleanup.session.complete" },
      { pattern: "app.cleanup.agent.delete" },
    );
  }
  return requirements;
}

export function missingSpanRequirements(
  observed: string[],
  requirements: SpanRequirement[],
): string[] {
  return requirements.flatMap((requirement) => {
    const minimum = requirement.minimum ?? 1;
    const count = observed.filter((name) =>
      spanNameMatches(name, requirement.pattern),
    ).length;
    return count >= minimum
      ? []
      : [
          minimum === 1
            ? requirement.pattern
            : `${requirement.pattern} (${count}/${minimum})`,
        ];
  });
}

export function spanNameMatches(name: string, pattern: string): boolean {
  const expression = pattern
    .split("*")
    .map((part) => part.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"))
    .join(".*");
  return new RegExp(`^${expression}$`).test(name);
}

export function countTraceNodes(node: TraceTreeNode | undefined): number {
  if (!node) return 0;
  return (
    1 +
    (node.children ?? []).reduce(
      (count, child) => count + countTraceNodes(child),
      0,
    )
  );
}

export function collectTraceSpanNames(
  node: TraceTreeNode | undefined,
): string[] {
  if (!node) return [];
  return [
    ...(node.span?.spanName ? [node.span.spanName] : []),
    ...(node.children ?? []).flatMap(collectTraceSpanNames),
  ];
}

export function parseJson(value: string): Record<string, unknown> | undefined {
  if (!value) return undefined;
  try {
    const parsed: unknown = JSON.parse(value);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
      return undefined;
    }
    return parsed as Record<string, unknown>;
  } catch {
    return undefined;
  }
}

export function truncate(
  value: unknown,
  maxLength = MAX_TRACE_VALUE_LENGTH,
): string {
  const text =
    typeof value === "string"
      ? value
      : (JSON.stringify(value) ?? String(value));
  if (text.length <= maxLength) return text;
  return `${text.slice(0, maxLength)}... [truncated]`;
}

export function toError(error: unknown): Error {
  return error instanceof Error ? error : new Error(String(error));
}

export function traceUrl(instanceUrl: string, traceId: string): string {
  return `${instanceUrl}/observability/traces?trace=${encodeURIComponent(traceId)}`;
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
