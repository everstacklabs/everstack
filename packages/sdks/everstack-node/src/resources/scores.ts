/**
 * Scores resource
 *
 * Provides access to the ScoreService for submitting and querying
 * evaluation scores on traces and observations.
 */

import type { Client } from "@connectrpc/connect";
import { ScoreService } from "@everstack/proto/everstack/scores/v1/service_pb.js";

import { fromConnectError } from "../errors.js";

type ScoresClient = Client<typeof ScoreService>;

type MethodInput<K extends keyof ScoresClient> = Parameters<ScoresClient[K]>[0];
type MethodOptions<K extends keyof ScoresClient> = Parameters<
  ScoresClient[K]
>[1];
type MethodOutput<K extends keyof ScoresClient> = Awaited<
  ReturnType<ScoresClient[K]>
>;

/**
 * Scores resource for submitting and querying evaluation scores.
 *
 * @example
 * ```ts
 * await client.scores.submit({
 *   traceId: "trace_abc",
 *   name: "quality",
 *   dataType: ScoreDataType.NUMERIC,
 *   numericValue: 0.9,
 *   source: ScoreSource.API,
 * });
 *
 * const { scores } = await client.scores.getByTrace({ traceId: "trace_abc" });
 * ```
 */
export class Scores {
  readonly raw: ScoresClient;

  /** @internal */
  constructor(client: ScoresClient) {
    this.raw = client;
  }

  private async _call<K extends keyof ScoresClient>(
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
   * Submit a single score on a trace or observation.
   */
  submit(
    request: MethodInput<"submitScore">,
    options?: MethodOptions<"submitScore">,
  ): Promise<MethodOutput<"submitScore">> {
    return this._call("submitScore", request, options);
  }

  /**
   * Submit multiple scores in a single request.
   */
  submitBatch(
    request: MethodInput<"submitScoreBatch">,
    options?: MethodOptions<"submitScoreBatch">,
  ): Promise<MethodOutput<"submitScoreBatch">> {
    return this._call("submitScoreBatch", request, options);
  }

  /**
   * Get all scores for a specific trace.
   */
  getByTrace(
    request: MethodInput<"getScoresByTrace">,
    options?: MethodOptions<"getScoresByTrace">,
  ): Promise<MethodOutput<"getScoresByTrace">> {
    return this._call("getScoresByTrace", request, options);
  }
}
