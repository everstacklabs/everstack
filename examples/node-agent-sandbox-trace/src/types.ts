import type Everstack from "@everstack/node";
import type { FallbackInfo } from "@everstack/node";

import type { BenchmarkResult } from "./benchmark.js";

export type EverstackClient = Everstack<string>;

export type ExampleConfig = {
  apiKey: string;
  baseUrl: string;
  orgId?: string;
  userId?: string;
  model?: string;
  provider?: string;
  sandboxImage: string;
  prompt: string;
  benchmarkMode: boolean;
  traceWaitMs: number;
  keepResources: boolean;
};

export type Route = {
  model: string;
  provider?: string;
};

export type AgentFallback = {
  from: string;
  to: string;
  attempt: number;
};

export type RunState = {
  agentId?: string;
  agentName?: string;
  sessionId?: string;
  sandboxId?: string;
  gatewayBrief?: string;
  gatewayFallback?: FallbackInfo;
  agentOutput?: string;
  agentFallbacks: AgentFallback[];
  agentRoutingEvents: string[];
  unexpectedToolNames: string[];
  sandboxLanguage?: string;
  sandboxTimeoutSeconds?: number;
  sandboxToolResult?: string;
  sandboxToolDurationMs?: number;
  benchmarkResult?: BenchmarkResult;
  sandboxToolCallCount: number;
  sandboxExecStarted: boolean;
  sandboxResultObserved: boolean;
  sandboxExecutionSucceeded: boolean;
  cleanupErrors: string[];
};

export function createRunState(): RunState {
  return {
    agentFallbacks: [],
    agentRoutingEvents: [],
    unexpectedToolNames: [],
    sandboxToolCallCount: 0,
    sandboxExecStarted: false,
    sandboxResultObserved: false,
    sandboxExecutionSucceeded: false,
    cleanupErrors: [],
  };
}
