/**
 * Observability resource
 *
 * Provides access to metrics dashboards, time series, sessions, users, and outcome scoring.
 */

import type { Client } from "@connectrpc/connect";
import { ObservabilityService } from "@everstack/proto/everstack/traces/v1/observability_service_pb.js";

import { fromConnectError } from "../errors.js";

type ObservabilityClient = Client<typeof ObservabilityService>;

type MethodInput<K extends keyof ObservabilityClient> = Parameters<
  ObservabilityClient[K]
>[0];
type MethodOptions<K extends keyof ObservabilityClient> = Parameters<
  ObservabilityClient[K]
>[1];
type MethodOutput<K extends keyof ObservabilityClient> = Awaited<
  ReturnType<ObservabilityClient[K]>
>;

/**
 * Observability resource
 */
export class Observability {
  /** Raw generated Connect client for advanced usage */
  readonly raw: ObservabilityClient;

  readonly metrics = {
    getDashboard: (
      request: MethodInput<"getMetricsDashboard">,
      options?: MethodOptions<"getMetricsDashboard">,
    ) => this.getMetricsDashboard(request, options),
    getTimeSeries: (
      request: MethodInput<"getMetricsTimeSeries">,
      options?: MethodOptions<"getMetricsTimeSeries">,
    ) => this.getMetricsTimeSeries(request, options),
  };

  readonly sessions = {
    list: (
      request: MethodInput<"listTraceSessions">,
      options?: MethodOptions<"listTraceSessions">,
    ) => this.listSessions(request, options),
    get: (
      request: MethodInput<"getTraceSession">,
      options?: MethodOptions<"getTraceSession">,
    ) => this.getSession(request, options),
  };

  readonly users = {
    list: (
      request: MethodInput<"listTraceUsers">,
      options?: MethodOptions<"listTraceUsers">,
    ) => this.listUsers(request, options),
    get: (
      request: MethodInput<"getTraceUser">,
      options?: MethodOptions<"getTraceUser">,
    ) => this.getUser(request, options),
  };

  readonly outcomes = {
    getDashboard: (
      request: MethodInput<"getOutcomeDashboard">,
      options?: MethodOptions<"getOutcomeDashboard">,
    ) => this.getOutcomeDashboard(request, options),
    getTimeSeries: (
      request: MethodInput<"getOutcomeTimeSeries">,
      options?: MethodOptions<"getOutcomeTimeSeries">,
    ) => this.getOutcomeTimeSeries(request, options),
  };

  /** @internal */
  constructor(client: ObservabilityClient) {
    this.raw = client;
  }

  private async _call<K extends keyof ObservabilityClient>(
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

  // Metrics
  getMetricsDashboard(
    request: MethodInput<"getMetricsDashboard">,
    options?: MethodOptions<"getMetricsDashboard">,
  ): Promise<MethodOutput<"getMetricsDashboard">> {
    return this._call("getMetricsDashboard", request, options);
  }

  getMetricsTimeSeries(
    request: MethodInput<"getMetricsTimeSeries">,
    options?: MethodOptions<"getMetricsTimeSeries">,
  ): Promise<MethodOutput<"getMetricsTimeSeries">> {
    return this._call("getMetricsTimeSeries", request, options);
  }

  // Sessions
  listSessions(
    request: MethodInput<"listTraceSessions">,
    options?: MethodOptions<"listTraceSessions">,
  ): Promise<MethodOutput<"listTraceSessions">> {
    return this._call("listTraceSessions", request, options);
  }

  getSession(
    request: MethodInput<"getTraceSession">,
    options?: MethodOptions<"getTraceSession">,
  ): Promise<MethodOutput<"getTraceSession">> {
    return this._call("getTraceSession", request, options);
  }

  // Users
  listUsers(
    request: MethodInput<"listTraceUsers">,
    options?: MethodOptions<"listTraceUsers">,
  ): Promise<MethodOutput<"listTraceUsers">> {
    return this._call("listTraceUsers", request, options);
  }

  getUser(
    request: MethodInput<"getTraceUser">,
    options?: MethodOptions<"getTraceUser">,
  ): Promise<MethodOutput<"getTraceUser">> {
    return this._call("getTraceUser", request, options);
  }

  // Outcomes
  getOutcomeDashboard(
    request: MethodInput<"getOutcomeDashboard">,
    options?: MethodOptions<"getOutcomeDashboard">,
  ): Promise<MethodOutput<"getOutcomeDashboard">> {
    return this._call("getOutcomeDashboard", request, options);
  }

  getOutcomeTimeSeries(
    request: MethodInput<"getOutcomeTimeSeries">,
    options?: MethodOptions<"getOutcomeTimeSeries">,
  ): Promise<MethodOutput<"getOutcomeTimeSeries">> {
    return this._call("getOutcomeTimeSeries", request, options);
  }
}
