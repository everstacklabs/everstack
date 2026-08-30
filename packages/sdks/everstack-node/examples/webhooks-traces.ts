#!/usr/bin/env tsx
/**
 * Complex webhook + traces example.
 *
 * This example shows two related flows:
 *
 * 1. A local webhook receiver that emits a first-class OTLP trace tree for
 *    every inbound webhook request.
 * 2. Optional setup/test of an Everstack agent webhook trigger, with the setup,
 *    synthetic trigger execution, and execution-history lookup traced too.
 *
 * Quick local trace-only run:
 *
 *   EVERSTACK_GATEWAY_URL=http://localhost:8089 \
 *   EVERSTACK_API_KEY=sk-local-placeholder \
 *   pnpm --dir packages/sdks/everstack-node run example:webhooks-traces
 *
 * Keep the receiver running:
 *
 *   WEBHOOK_SECRET=dev-secret pnpm --dir packages/sdks/everstack-node run example:webhooks-traces -- --listen
 *
 * Also create and test-fire an Everstack agent webhook trigger:
 *
 *   EVERSTACK_API_KEY=pk_... RUN_AGENT_TRIGGER=1 \
 *   pnpm --dir packages/sdks/everstack-node run example:webhooks-traces
 *
 * Multi-tenant/cloud:
 *
 *   EVERSTACK_ORG_ID=org_... EVERSTACK_USER_ID=user_... ...
 */

import { createHmac, randomUUID, timingSafeEqual } from "node:crypto";
import {
  createServer,
  type IncomingMessage,
  type ServerResponse,
} from "node:http";

import Everstack, { type OtelSpan } from "../src/index.js";

type JsonRecord = Record<string, unknown>;

const args = new Set(process.argv.slice(2));

if (args.has("--help") || args.has("-h")) {
  printHelp();
  process.exit(0);
}

const config = {
  apiKey: process.env.EVERSTACK_API_KEY ?? "sk-local-placeholder",
  baseUrl: process.env.EVERSTACK_GATEWAY_URL ?? "http://localhost:8089",
  orgId: process.env.EVERSTACK_ORG_ID,
  userId: process.env.EVERSTACK_USER_ID,
  port: parsePort(process.env.WEBHOOK_PORT ?? "4877"),
  webhookSecret: process.env.WEBHOOK_SECRET ?? "local-webhook-secret",
  model: process.env.MODEL ?? "gpt-4o-mini",
  runAgentTrigger: envFlag("RUN_AGENT_TRIGGER") || args.has("--setup-trigger"),
  runLLM: envFlag("WEBHOOK_RUN_LLM") || args.has("--run-llm"),
  keepAgent: envFlag("KEEP_AGENT") || args.has("--keep-agent"),
  listen: args.has("--listen"),
  sendSample: !args.has("--no-sample"),
  existingAgentId: process.env.AGENT_ID,
};

const tenantId = config.orgId ?? "";
const client = new Everstack({
  apiKey: config.apiKey,
  baseUrl: config.baseUrl,
  orgId: config.orgId,
  userId: config.userId,
});

const server = createServer((req, res) => {
  route(req, res).catch((error) => {
    console.error("request failed:", errorMessage(error));
    if (!res.headersSent) {
      sendJson(res, 500, { error: errorMessage(error) });
    } else {
      res.end();
    }
  });
});

await listen(server, config.port);

console.log("Everstack webhook trace example");
console.log(`  Gateway:  ${config.baseUrl}`);
console.log(`  Receiver: http://127.0.0.1:${config.port}/webhooks/orders`);
console.log(`  Secret:   ${config.webhookSecret ? "configured" : "disabled"}`);
console.log("");

if (config.runAgentTrigger) {
  await setupAndTestAgentWebhookTrigger();
}

if (config.sendSample) {
  await sendSampleWebhook();
}

if (!config.listen) {
  await close(server);
  console.log(
    "done. open /observability/traces and search for `node-sdk.webhook`.",
  );
} else {
  console.log("listening. Ctrl-C to stop.");
}

async function route(req: IncomingMessage, res: ServerResponse) {
  const path = new URL(req.url ?? "/", "http://localhost").pathname;

  if (req.method === "GET" && path === "/healthz") {
    sendJson(res, 200, { ok: true });
    return;
  }

  if (req.method !== "POST" || path !== "/webhooks/orders") {
    sendJson(res, 404, { error: "not found" });
    return;
  }

  await handleOrderWebhook(req, res);
}

async function handleOrderWebhook(req: IncomingMessage, res: ServerResponse) {
  const startedAt = Date.now();
  const rawBody = await readBody(req);
  const bodyText = rawBody.toString("utf8");
  const requestId = header(req, "x-request-id") ?? randomUUID();
  const provider = header(req, "x-webhook-provider") ?? "sample-commerce";

  const root = client.traces.startSpan({
    name: "node-sdk.webhook.orders.receive",
    kind: "SERVER",
    type: "CHAIN",
    attributes: {
      "http.method": req.method ?? "POST",
      "http.route": "/webhooks/orders",
      "webhook.provider": provider,
      "webhook.request_id": requestId,
    },
  });

  root.setInput({
    headers: redactHeaders(req.headers),
    rawBody: truncate(bodyText, 20_000),
  });

  try {
    await tracedStep(
      root,
      "node-sdk.webhook.verify_signature",
      "SPAN",
      async (span) => {
        const signature = header(req, "x-everstack-signature");
        span.setInput({ signature: signature ? "[redacted]" : null });

        if (
          config.webhookSecret &&
          !verifySignature(rawBody, signature, config.webhookSecret)
        ) {
          throw new Error("invalid webhook signature");
        }

        const result = { verified: Boolean(config.webhookSecret) };
        span.setOutput(result);
        return result;
      },
    );

    const payload = await tracedStep(
      root,
      "node-sdk.webhook.parse_payload",
      "SPAN",
      async (span) => {
        const parsed = parseJson(bodyText);
        span.setOutput(parsed);
        return parsed;
      },
    );

    const event = await tracedStep(
      root,
      "node-sdk.webhook.normalize_event",
      "SPAN",
      async (span) => {
        const normalized = normalizeOrderEvent(payload, requestId, provider);
        span.setAttribute("webhook.event_type", normalized.type);
        span.setAttribute("webhook.event_id", normalized.id);
        span.setOutput(normalized);
        return normalized;
      },
    );

    let summary: string | undefined;
    if (config.runLLM) {
      summary = await tracedStep(
        root,
        "node-sdk.webhook.llm_summary",
        "GENERATION",
        async (span) => {
          const prompt = `Summarize this order webhook for an operations log:\n${JSON.stringify(event, null, 2)}`;
          span.setModel(config.model);
          span.setMessages([{ role: "user", content: prompt }]);

          const response = await client.chat.completions.create({
            model: config.model as never,
            messages: [
              {
                role: "system",
                content: "You write concise operations summaries.",
              },
              { role: "user", content: prompt },
            ],
            max_tokens: 120,
          });

          const content = response.choices[0]?.message?.content ?? "";
          span.setChoices(response.choices);
          if (response.usage) {
            span.setUsage({
              inputTokens: response.usage.prompt_tokens,
              outputTokens: response.usage.completion_tokens,
              totalTokens: response.usage.total_tokens,
            });
          }
          span.setOutput(content);
          return content;
        },
      );
    }

    await tracedStep(
      root,
      "node-sdk.webhook.dispatch",
      "SPAN",
      async (span) => {
        const dispatchResult = {
          destination: "internal.order-events",
          action:
            event.type === "order.created"
              ? "enqueue_fulfillment_check"
              : "record_event",
          summary: summary ?? null,
        };
        span.setInput(event);
        span.setOutput(dispatchResult);
        return dispatchResult;
      },
    );

    const response = {
      ok: true,
      request_id: requestId,
      trace_id: root.traceId,
      received_event: event.type,
      elapsed_ms: Date.now() - startedAt,
    };
    root.setOutput(response);
    await root.end();
    sendJson(res, 202, response);
  } catch (error) {
    root.setError(error);
    root.setOutput({
      ok: false,
      request_id: requestId,
      error: errorMessage(error),
    });
    await root.end();
    sendJson(res, 400, {
      ok: false,
      request_id: requestId,
      trace_id: root.traceId,
      error: errorMessage(error),
    });
  }
}

async function setupAndTestAgentWebhookTrigger() {
  const root = client.traces.startSpan({
    name: "node-sdk.webhook.agent_trigger.setup",
    kind: "CLIENT",
    type: "CHAIN",
    attributes: {
      "everstack.example": "webhooks-traces",
      "everstack.org_id": tenantId || "self-hosted",
    },
  });

  root.setInput({
    existingAgentId: config.existingAgentId ?? null,
    keepAgent: config.keepAgent,
  });

  let createdAgentId: string | undefined;
  let triggerId: string | undefined;

  try {
    const agentId =
      config.existingAgentId ??
      (await tracedStep(
        root,
        "node-sdk.webhook.agent.create",
        "SPAN",
        async (span) => {
          const request = {
            name: `sdk-webhook-trace-${Date.now()}`,
            description:
              "Temporary agent for the Node SDK webhook trace example",
            model: config.model,
            system_prompt:
              "You receive webhook payloads and produce short operational summaries.",
            max_turns: 3,
            hidden: true,
          };
          span.setInput(request);
          const agent = await client.agents.create(request);
          createdAgentId = agent.id;
          span.setOutput({ agentId: agent.id, name: agent.name });
          return agent.id;
        },
      ));

    const trigger = await tracedStep(
      root,
      "node-sdk.webhook.agent_trigger.create",
      "SPAN",
      async (span) => {
        const request = {
          tenantId,
          agentId,
          name: `sdk-webhook-trigger-${Date.now()}`,
          triggerType: "webhook",
          inputTemplate:
            "Webhook payload received. Summarize the event type, order id, customer id, and recommended next action.\n\nPayload:\n{{ .payload }}",
          maxRetries: 1,
          retryDelaySeconds: 5,
          timeoutSeconds: 180,
          maxConcurrent: 1,
        };

        span.setInput({ ...request, tenantId: tenantId || "self-hosted" });
        const response = await client.agents.triggers.create(request);
        triggerId = response.trigger?.id;
        span.setOutput({
          triggerId,
          webhookUrl: response.webhookUrl,
          webhookSecret: response.webhookSecret
            ? "[shown once, redacted]"
            : null,
        });
        return response;
      },
    );

    if (!trigger.trigger?.id) {
      throw new Error("trigger creation did not return an id");
    }

    await tracedStep(
      root,
      "node-sdk.webhook.agent_trigger.test_fire",
      "SPAN",
      async (span) => {
        const testPayload = {
          id: `evt_${Date.now()}`,
          type: "order.created",
          source: "node-sdk-webhooks-traces",
          data: {
            order_id: "ord_sdk_1001",
            customer_id: "cus_sdk_42",
            total_cents: 12900,
            currency: "USD",
            priority: "high",
          },
        };

        span.setInput(testPayload);
        const response = await client.agents.triggers.test({
          tenantId,
          id: trigger.trigger.id,
          testPayload,
        });
        span.setOutput({
          executionId: response.execution?.id,
          status: response.execution?.status,
          sessionId: response.execution?.sessionId,
        });
        return response;
      },
    );

    await tracedStep(
      root,
      "node-sdk.webhook.agent_trigger.executions.list",
      "SPAN",
      async (span) => {
        const response = await client.agents.triggers.listExecutions({
          tenantId,
          triggerId: trigger.trigger!.id,
          limit: 5,
          offset: 0,
        });
        span.setOutput({
          total: response.total,
          executions: response.executions.map((execution) => ({
            id: execution.id,
            status: execution.status,
            sessionId: execution.sessionId,
            durationMs: execution.durationMs,
            outputPreview: truncate(execution.outputPreview, 500),
          })),
        });
        return response;
      },
    );

    root.setOutput({
      ok: true,
      agentId,
      triggerId: trigger.trigger.id,
      webhookUrl: trigger.webhookUrl,
      cleanup: config.keepAgent ? "kept" : "scheduled",
    });
  } catch (error) {
    root.setError(error);
    root.setOutput({ ok: false, error: errorMessage(error) });
    throw error;
  } finally {
    if (!config.keepAgent && triggerId) {
      await tracedCleanup(
        root,
        "node-sdk.webhook.agent_trigger.delete",
        async () => {
          await client.agents.triggers.delete({ tenantId, id: triggerId! });
          return { triggerId };
        },
      );
    }

    if (!config.keepAgent && createdAgentId) {
      await tracedCleanup(root, "node-sdk.webhook.agent.delete", async () => {
        await client.agents.delete(createdAgentId!);
        return { agentId: createdAgentId };
      });
    }

    await root.end();
  }
}

async function sendSampleWebhook() {
  const payload = {
    id: `evt_${Date.now()}`,
    type: "order.created",
    source: "sample-commerce",
    data: {
      order_id: "ord_local_1001",
      customer_id: "cus_local_42",
      total_cents: 12900,
      currency: "USD",
      line_items: [
        { sku: "everstack-pro", quantity: 1 },
        { sku: "support-pack", quantity: 2 },
      ],
    },
  };
  const body = Buffer.from(JSON.stringify(payload));
  const signature = signBody(body, config.webhookSecret);
  const url = `http://127.0.0.1:${config.port}/webhooks/orders`;

  console.log(`sending sample webhook to ${url}`);
  const response = await fetch(url, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-Request-Id": randomUUID(),
      "X-Webhook-Provider": "sample-commerce",
      "X-Everstack-Signature": signature,
    },
    body,
  });

  const text = await response.text();
  console.log(`sample response: ${response.status} ${text}`);
  if (!response.ok) {
    throw new Error(`sample webhook failed with HTTP ${response.status}`);
  }
}

async function tracedStep<T>(
  parent: OtelSpan,
  name: string,
  type: "SPAN" | "GENERATION" | "CHAIN" | "TOOL",
  fn: (span: OtelSpan) => Promise<T>,
): Promise<T> {
  const span = parent.startChild({ name, type });
  try {
    const result = await fn(span);
    await span.end();
    return result;
  } catch (error) {
    span.setError(error);
    span.setOutput({ error: errorMessage(error) });
    await span.end();
    throw error;
  }
}

async function tracedCleanup<T>(
  parent: OtelSpan,
  name: string,
  fn: () => Promise<T>,
): Promise<void> {
  const span = parent.startChild({ name, type: "SPAN" });
  try {
    const result = await fn();
    span.setOutput(result);
  } catch (error) {
    span.setError(error);
    span.setOutput({ error: errorMessage(error) });
    console.warn(`${name} failed: ${errorMessage(error)}`);
  } finally {
    await span.end();
  }
}

function normalizeOrderEvent(
  payload: JsonRecord,
  requestId: string,
  provider: string,
) {
  const data = asRecord(payload.data);
  return {
    id: stringValue(payload.id) ?? requestId,
    type: stringValue(payload.type) ?? "unknown",
    provider,
    source: stringValue(payload.source) ?? provider,
    orderId: stringValue(data.order_id),
    customerId: stringValue(data.customer_id),
    totalCents: numberValue(data.total_cents),
    currency: stringValue(data.currency),
    raw: payload,
  };
}

function parseJson(body: string): JsonRecord {
  const parsed = JSON.parse(body) as unknown;
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("webhook body must be a JSON object");
  }
  return parsed as JsonRecord;
}

function verifySignature(
  body: Buffer,
  signature: string | undefined,
  secret: string,
): boolean {
  if (!signature) return false;
  const expected = signBody(body, secret);
  return (
    secureEqual(signature, expected) ||
    secureEqual(signature, expected.replace(/^sha256=/, ""))
  );
}

function signBody(body: Buffer, secret: string): string {
  const digest = createHmac("sha256", secret).update(body).digest("hex");
  return `sha256=${digest}`;
}

function secureEqual(a: string, b: string): boolean {
  const left = Buffer.from(a);
  const right = Buffer.from(b);
  return left.length === right.length && timingSafeEqual(left, right);
}

async function readBody(
  req: IncomingMessage,
  maxBytes = 1_000_000,
): Promise<Buffer> {
  const chunks: Buffer[] = [];
  let size = 0;
  for await (const chunk of req) {
    const buffer = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
    size += buffer.length;
    if (size > maxBytes) {
      throw new Error(`webhook body exceeds ${maxBytes} bytes`);
    }
    chunks.push(buffer);
  }
  return Buffer.concat(chunks);
}

function sendJson(res: ServerResponse, status: number, body: JsonRecord) {
  res.writeHead(status, { "Content-Type": "application/json" });
  res.end(JSON.stringify(body));
}

function header(req: IncomingMessage, name: string): string | undefined {
  const value = req.headers[name.toLowerCase()];
  if (Array.isArray(value)) return value[0];
  return value;
}

function redactHeaders(
  headers: IncomingMessage["headers"],
): Record<string, string> {
  const sensitive = new Set([
    "authorization",
    "cookie",
    "x-api-key",
    "x-everstack-signature",
    "x-mf-api-key",
  ]);
  const out: Record<string, string> = {};
  for (const [key, value] of Object.entries(headers)) {
    out[key] = sensitive.has(key.toLowerCase())
      ? "[redacted]"
      : Array.isArray(value)
        ? value.join(", ")
        : (value ?? "");
  }
  return out;
}

function asRecord(value: unknown): JsonRecord {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as JsonRecord)
    : {};
}

function stringValue(value: unknown): string | undefined {
  return typeof value === "string" ? value : undefined;
}

function numberValue(value: unknown): number | undefined {
  return typeof value === "number" ? value : undefined;
}

function truncate(value: string | undefined, max: number): string {
  if (!value) return "";
  return value.length > max ? `${value.slice(0, max)}...<truncated>` : value;
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function envFlag(name: string): boolean {
  return /^(1|true|yes)$/i.test(process.env[name] ?? "");
}

function parsePort(value: string): number {
  const port = Number(value);
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    throw new Error(`invalid WEBHOOK_PORT: ${value}`);
  }
  return port;
}

function listen(target: ReturnType<typeof createServer>, port: number) {
  return new Promise<void>((resolve, reject) => {
    target.once("error", reject);
    target.listen(port, "127.0.0.1", () => {
      target.off("error", reject);
      resolve();
    });
  });
}

function close(target: ReturnType<typeof createServer>) {
  return new Promise<void>((resolve, reject) => {
    target.close((error) => (error ? reject(error) : resolve()));
  });
}

function printHelp() {
  console.log(`
Usage:
  pnpm --dir packages/sdks/everstack-node run example:webhooks-traces [-- options]

Options:
  --listen          Keep the local webhook server running.
  --no-sample       Do not send the built-in sample webhook request.
  --setup-trigger   Create and test-fire an Everstack agent webhook trigger.
  --run-llm         Also call chat completions and attach a GENERATION child span.
  --keep-agent      Keep the temporary trigger/agent created by --setup-trigger.

Environment:
  EVERSTACK_GATEWAY_URL   Gateway URL. Defaults to http://localhost:8089.
  EVERSTACK_API_KEY       API key. Defaults to sk-local-placeholder.
  EVERSTACK_ORG_ID        Optional org/tenant id.
  EVERSTACK_USER_ID       Optional user attribution id.
  WEBHOOK_PORT            Local receiver port. Defaults to 4877.
  WEBHOOK_SECRET          HMAC secret. Defaults to local-webhook-secret.
  RUN_AGENT_TRIGGER=1     Same as --setup-trigger.
  WEBHOOK_RUN_LLM=1       Same as --run-llm.
  AGENT_ID=agent_...      Reuse an existing agent for --setup-trigger.
  MODEL=gpt-4o-mini       Model for the optional LLM and temporary agent.
`);
}
