/**
 * Datasets and evaluations resources
 */

import type { Client } from "@connectrpc/connect";
import {
  DatasetService,
  EvalService,
} from "@everstack/proto/everstack/datasets/v1/datasets_service_pb.js";

import { fromConnectError } from "../errors.js";

type DatasetClient = Client<typeof DatasetService>;
type EvalClient = Client<typeof EvalService>;

type DatasetMethodInput<K extends keyof DatasetClient> = Parameters<
  DatasetClient[K]
>[0];
type DatasetMethodOptions<K extends keyof DatasetClient> = Parameters<
  DatasetClient[K]
>[1];
type DatasetMethodOutput<K extends keyof DatasetClient> = Awaited<
  ReturnType<DatasetClient[K]>
>;

type EvalMethodInput<K extends keyof EvalClient> = Parameters<EvalClient[K]>[0];
type EvalMethodOptions<K extends keyof EvalClient> = Parameters<
  EvalClient[K]
>[1];
type EvalMethodOutput<K extends keyof EvalClient> = Awaited<
  ReturnType<EvalClient[K]>
>;

/**
 * Datasets resource
 */
export class Datasets {
  /** Raw generated Connect client for advanced usage */
  readonly raw: DatasetClient;

  readonly items = {
    create: (
      request: DatasetMethodInput<"createDatasetItem">,
      options?: DatasetMethodOptions<"createDatasetItem">,
    ) => this.createItem(request, options),
    createBatch: (
      request: DatasetMethodInput<"createDatasetItemBatch">,
      options?: DatasetMethodOptions<"createDatasetItemBatch">,
    ) => this.createItemBatch(request, options),
    get: (
      request: DatasetMethodInput<"getDatasetItem">,
      options?: DatasetMethodOptions<"getDatasetItem">,
    ) => this.getItem(request, options),
    list: (
      request: DatasetMethodInput<"listDatasetItems">,
      options?: DatasetMethodOptions<"listDatasetItems">,
    ) => this.listItems(request, options),
    update: (
      request: DatasetMethodInput<"updateDatasetItem">,
      options?: DatasetMethodOptions<"updateDatasetItem">,
    ) => this.updateItem(request, options),
    delete: (
      request: DatasetMethodInput<"deleteDatasetItem">,
      options?: DatasetMethodOptions<"deleteDatasetItem">,
    ) => this.deleteItem(request, options),
  };

  readonly scoreConfigs = {
    create: (
      request: DatasetMethodInput<"createScoreConfig">,
      options?: DatasetMethodOptions<"createScoreConfig">,
    ) => this.createScoreConfig(request, options),
    get: (
      request: DatasetMethodInput<"getScoreConfig">,
      options?: DatasetMethodOptions<"getScoreConfig">,
    ) => this.getScoreConfig(request, options),
    list: (
      request: DatasetMethodInput<"listScoreConfigs">,
      options?: DatasetMethodOptions<"listScoreConfigs">,
    ) => this.listScoreConfigs(request, options),
    update: (
      request: DatasetMethodInput<"updateScoreConfig">,
      options?: DatasetMethodOptions<"updateScoreConfig">,
    ) => this.updateScoreConfig(request, options),
    delete: (
      request: DatasetMethodInput<"deleteScoreConfig">,
      options?: DatasetMethodOptions<"deleteScoreConfig">,
    ) => this.deleteScoreConfig(request, options),
  };

  readonly metrics = {
    listBuiltin: (
      request: DatasetMethodInput<"listBuiltinMetrics">,
      options?: DatasetMethodOptions<"listBuiltinMetrics">,
    ) => this.listBuiltinMetrics(request, options),
  };

  /** @internal */
  constructor(client: DatasetClient) {
    this.raw = client;
  }

  private async _call<K extends keyof DatasetClient>(
    method: K,
    request: DatasetMethodInput<K>,
    options?: DatasetMethodOptions<K>,
  ): Promise<DatasetMethodOutput<K>> {
    try {
      const fn = this.raw[method] as (
        req: DatasetMethodInput<K>,
        opt?: DatasetMethodOptions<K>,
      ) => Promise<DatasetMethodOutput<K>>;
      return await fn(request, options);
    } catch (error) {
      throw fromConnectError(error);
    }
  }

  // Datasets
  create(
    request: DatasetMethodInput<"createDataset">,
    options?: DatasetMethodOptions<"createDataset">,
  ): Promise<DatasetMethodOutput<"createDataset">> {
    return this._call("createDataset", request, options);
  }

  get(
    request: DatasetMethodInput<"getDataset">,
    options?: DatasetMethodOptions<"getDataset">,
  ): Promise<DatasetMethodOutput<"getDataset">> {
    return this._call("getDataset", request, options);
  }

  list(
    request: DatasetMethodInput<"listDatasets">,
    options?: DatasetMethodOptions<"listDatasets">,
  ): Promise<DatasetMethodOutput<"listDatasets">> {
    return this._call("listDatasets", request, options);
  }

  update(
    request: DatasetMethodInput<"updateDataset">,
    options?: DatasetMethodOptions<"updateDataset">,
  ): Promise<DatasetMethodOutput<"updateDataset">> {
    return this._call("updateDataset", request, options);
  }

  delete(
    request: DatasetMethodInput<"deleteDataset">,
    options?: DatasetMethodOptions<"deleteDataset">,
  ): Promise<DatasetMethodOutput<"deleteDataset">> {
    return this._call("deleteDataset", request, options);
  }

  // Dataset items
  createItem(
    request: DatasetMethodInput<"createDatasetItem">,
    options?: DatasetMethodOptions<"createDatasetItem">,
  ): Promise<DatasetMethodOutput<"createDatasetItem">> {
    return this._call("createDatasetItem", request, options);
  }

  createItemBatch(
    request: DatasetMethodInput<"createDatasetItemBatch">,
    options?: DatasetMethodOptions<"createDatasetItemBatch">,
  ): Promise<DatasetMethodOutput<"createDatasetItemBatch">> {
    return this._call("createDatasetItemBatch", request, options);
  }

  getItem(
    request: DatasetMethodInput<"getDatasetItem">,
    options?: DatasetMethodOptions<"getDatasetItem">,
  ): Promise<DatasetMethodOutput<"getDatasetItem">> {
    return this._call("getDatasetItem", request, options);
  }

  listItems(
    request: DatasetMethodInput<"listDatasetItems">,
    options?: DatasetMethodOptions<"listDatasetItems">,
  ): Promise<DatasetMethodOutput<"listDatasetItems">> {
    return this._call("listDatasetItems", request, options);
  }

  updateItem(
    request: DatasetMethodInput<"updateDatasetItem">,
    options?: DatasetMethodOptions<"updateDatasetItem">,
  ): Promise<DatasetMethodOutput<"updateDatasetItem">> {
    return this._call("updateDatasetItem", request, options);
  }

  deleteItem(
    request: DatasetMethodInput<"deleteDatasetItem">,
    options?: DatasetMethodOptions<"deleteDatasetItem">,
  ): Promise<DatasetMethodOutput<"deleteDatasetItem">> {
    return this._call("deleteDatasetItem", request, options);
  }

  // Score configs and builtin metrics
  createScoreConfig(
    request: DatasetMethodInput<"createScoreConfig">,
    options?: DatasetMethodOptions<"createScoreConfig">,
  ): Promise<DatasetMethodOutput<"createScoreConfig">> {
    return this._call("createScoreConfig", request, options);
  }

  getScoreConfig(
    request: DatasetMethodInput<"getScoreConfig">,
    options?: DatasetMethodOptions<"getScoreConfig">,
  ): Promise<DatasetMethodOutput<"getScoreConfig">> {
    return this._call("getScoreConfig", request, options);
  }

  listScoreConfigs(
    request: DatasetMethodInput<"listScoreConfigs">,
    options?: DatasetMethodOptions<"listScoreConfigs">,
  ): Promise<DatasetMethodOutput<"listScoreConfigs">> {
    return this._call("listScoreConfigs", request, options);
  }

  updateScoreConfig(
    request: DatasetMethodInput<"updateScoreConfig">,
    options?: DatasetMethodOptions<"updateScoreConfig">,
  ): Promise<DatasetMethodOutput<"updateScoreConfig">> {
    return this._call("updateScoreConfig", request, options);
  }

  deleteScoreConfig(
    request: DatasetMethodInput<"deleteScoreConfig">,
    options?: DatasetMethodOptions<"deleteScoreConfig">,
  ): Promise<DatasetMethodOutput<"deleteScoreConfig">> {
    return this._call("deleteScoreConfig", request, options);
  }

  listBuiltinMetrics(
    request: DatasetMethodInput<"listBuiltinMetrics">,
    options?: DatasetMethodOptions<"listBuiltinMetrics">,
  ): Promise<DatasetMethodOutput<"listBuiltinMetrics">> {
    return this._call("listBuiltinMetrics", request, options);
  }
}

/**
 * Evaluations resource
 */
export class Evaluations {
  /** Raw generated Connect client for advanced usage */
  readonly raw: EvalClient;

  readonly runs = {
    create: (
      request: EvalMethodInput<"createEvalRun">,
      options?: EvalMethodOptions<"createEvalRun">,
    ) => this.createRun(request, options),
    get: (
      request: EvalMethodInput<"getEvalRun">,
      options?: EvalMethodOptions<"getEvalRun">,
    ) => this.getRun(request, options),
    list: (
      request: EvalMethodInput<"listEvalRuns">,
      options?: EvalMethodOptions<"listEvalRuns">,
    ) => this.listRuns(request, options),
    cancel: (
      request: EvalMethodInput<"cancelEvalRun">,
      options?: EvalMethodOptions<"cancelEvalRun">,
    ) => this.cancelRun(request, options),
    delete: (
      request: EvalMethodInput<"deleteEvalRun">,
      options?: EvalMethodOptions<"deleteEvalRun">,
    ) => this.deleteRun(request, options),
    retry: (
      request: EvalMethodInput<"retryEvalRun">,
      options?: EvalMethodOptions<"retryEvalRun">,
    ) => this.retryRun(request, options),
    getItems: (
      request: EvalMethodInput<"getEvalRunItems">,
      options?: EvalMethodOptions<"getEvalRunItems">,
    ) => this.getRunItems(request, options),
    getSummary: (
      request: EvalMethodInput<"getEvalRunSummary">,
      options?: EvalMethodOptions<"getEvalRunSummary">,
    ) => this.getRunSummary(request, options),
    compare: (
      request: EvalMethodInput<"compareEvalRuns">,
      options?: EvalMethodOptions<"compareEvalRuns">,
    ) => this.compareRuns(request, options),
    setBaseline: (
      request: EvalMethodInput<"setBaseline">,
      options?: EvalMethodOptions<"setBaseline">,
    ) => this.setBaseline(request, options),
  };

  readonly schedules = {
    create: (
      request: EvalMethodInput<"createEvalSchedule">,
      options?: EvalMethodOptions<"createEvalSchedule">,
    ) => this.createSchedule(request, options),
    get: (
      request: EvalMethodInput<"getEvalSchedule">,
      options?: EvalMethodOptions<"getEvalSchedule">,
    ) => this.getSchedule(request, options),
    list: (
      request: EvalMethodInput<"listEvalSchedules">,
      options?: EvalMethodOptions<"listEvalSchedules">,
    ) => this.listSchedules(request, options),
    update: (
      request: EvalMethodInput<"updateEvalSchedule">,
      options?: EvalMethodOptions<"updateEvalSchedule">,
    ) => this.updateSchedule(request, options),
    delete: (
      request: EvalMethodInput<"deleteEvalSchedule">,
      options?: EvalMethodOptions<"deleteEvalSchedule">,
    ) => this.deleteSchedule(request, options),
  };

  readonly samplingRules = {
    create: (
      request: EvalMethodInput<"createSamplingEvalRule">,
      options?: EvalMethodOptions<"createSamplingEvalRule">,
    ) => this._call("createSamplingEvalRule", request, options),
    get: (
      request: EvalMethodInput<"getSamplingEvalRule">,
      options?: EvalMethodOptions<"getSamplingEvalRule">,
    ) => this._call("getSamplingEvalRule", request, options),
    list: (
      request: EvalMethodInput<"listSamplingEvalRules">,
      options?: EvalMethodOptions<"listSamplingEvalRules">,
    ) => this._call("listSamplingEvalRules", request, options),
    update: (
      request: EvalMethodInput<"updateSamplingEvalRule">,
      options?: EvalMethodOptions<"updateSamplingEvalRule">,
    ) => this._call("updateSamplingEvalRule", request, options),
    delete: (
      request: EvalMethodInput<"deleteSamplingEvalRule">,
      options?: EvalMethodOptions<"deleteSamplingEvalRule">,
    ) => this._call("deleteSamplingEvalRule", request, options),
    runNow: (
      request: EvalMethodInput<"runSamplingEvalRuleNow">,
      options?: EvalMethodOptions<"runSamplingEvalRuleNow">,
    ) => this._call("runSamplingEvalRuleNow", request, options),
  };

  /** @internal */
  constructor(client: EvalClient) {
    this.raw = client;
  }

  private async _call<K extends keyof EvalClient>(
    method: K,
    request: EvalMethodInput<K>,
    options?: EvalMethodOptions<K>,
  ): Promise<EvalMethodOutput<K>> {
    try {
      const fn = this.raw[method] as (
        req: EvalMethodInput<K>,
        opt?: EvalMethodOptions<K>,
      ) => Promise<EvalMethodOutput<K>>;
      return await fn(request, options);
    } catch (error) {
      throw fromConnectError(error);
    }
  }

  // Eval runs
  createRun(
    request: EvalMethodInput<"createEvalRun">,
    options?: EvalMethodOptions<"createEvalRun">,
  ): Promise<EvalMethodOutput<"createEvalRun">> {
    return this._call("createEvalRun", request, options);
  }

  getRun(
    request: EvalMethodInput<"getEvalRun">,
    options?: EvalMethodOptions<"getEvalRun">,
  ): Promise<EvalMethodOutput<"getEvalRun">> {
    return this._call("getEvalRun", request, options);
  }

  listRuns(
    request: EvalMethodInput<"listEvalRuns">,
    options?: EvalMethodOptions<"listEvalRuns">,
  ): Promise<EvalMethodOutput<"listEvalRuns">> {
    return this._call("listEvalRuns", request, options);
  }

  cancelRun(
    request: EvalMethodInput<"cancelEvalRun">,
    options?: EvalMethodOptions<"cancelEvalRun">,
  ): Promise<EvalMethodOutput<"cancelEvalRun">> {
    return this._call("cancelEvalRun", request, options);
  }

  deleteRun(
    request: EvalMethodInput<"deleteEvalRun">,
    options?: EvalMethodOptions<"deleteEvalRun">,
  ): Promise<EvalMethodOutput<"deleteEvalRun">> {
    return this._call("deleteEvalRun", request, options);
  }

  retryRun(
    request: EvalMethodInput<"retryEvalRun">,
    options?: EvalMethodOptions<"retryEvalRun">,
  ): Promise<EvalMethodOutput<"retryEvalRun">> {
    return this._call("retryEvalRun", request, options);
  }

  getRunItems(
    request: EvalMethodInput<"getEvalRunItems">,
    options?: EvalMethodOptions<"getEvalRunItems">,
  ): Promise<EvalMethodOutput<"getEvalRunItems">> {
    return this._call("getEvalRunItems", request, options);
  }

  getRunSummary(
    request: EvalMethodInput<"getEvalRunSummary">,
    options?: EvalMethodOptions<"getEvalRunSummary">,
  ): Promise<EvalMethodOutput<"getEvalRunSummary">> {
    return this._call("getEvalRunSummary", request, options);
  }

  compareRuns(
    request: EvalMethodInput<"compareEvalRuns">,
    options?: EvalMethodOptions<"compareEvalRuns">,
  ): Promise<EvalMethodOutput<"compareEvalRuns">> {
    return this._call("compareEvalRuns", request, options);
  }

  setBaseline(
    request: EvalMethodInput<"setBaseline">,
    options?: EvalMethodOptions<"setBaseline">,
  ): Promise<EvalMethodOutput<"setBaseline">> {
    return this._call("setBaseline", request, options);
  }

  // Eval schedules
  createSchedule(
    request: EvalMethodInput<"createEvalSchedule">,
    options?: EvalMethodOptions<"createEvalSchedule">,
  ): Promise<EvalMethodOutput<"createEvalSchedule">> {
    return this._call("createEvalSchedule", request, options);
  }

  getSchedule(
    request: EvalMethodInput<"getEvalSchedule">,
    options?: EvalMethodOptions<"getEvalSchedule">,
  ): Promise<EvalMethodOutput<"getEvalSchedule">> {
    return this._call("getEvalSchedule", request, options);
  }

  listSchedules(
    request: EvalMethodInput<"listEvalSchedules">,
    options?: EvalMethodOptions<"listEvalSchedules">,
  ): Promise<EvalMethodOutput<"listEvalSchedules">> {
    return this._call("listEvalSchedules", request, options);
  }

  updateSchedule(
    request: EvalMethodInput<"updateEvalSchedule">,
    options?: EvalMethodOptions<"updateEvalSchedule">,
  ): Promise<EvalMethodOutput<"updateEvalSchedule">> {
    return this._call("updateEvalSchedule", request, options);
  }

  deleteSchedule(
    request: EvalMethodInput<"deleteEvalSchedule">,
    options?: EvalMethodOptions<"deleteEvalSchedule">,
  ): Promise<EvalMethodOutput<"deleteEvalSchedule">> {
    return this._call("deleteEvalSchedule", request, options);
  }

  // Sampling eval rules
  createSamplingRule(
    request: EvalMethodInput<"createSamplingEvalRule">,
    options?: EvalMethodOptions<"createSamplingEvalRule">,
  ): Promise<EvalMethodOutput<"createSamplingEvalRule">> {
    return this._call("createSamplingEvalRule", request, options);
  }

  getSamplingRule(
    request: EvalMethodInput<"getSamplingEvalRule">,
    options?: EvalMethodOptions<"getSamplingEvalRule">,
  ): Promise<EvalMethodOutput<"getSamplingEvalRule">> {
    return this._call("getSamplingEvalRule", request, options);
  }

  listSamplingRules(
    request: EvalMethodInput<"listSamplingEvalRules">,
    options?: EvalMethodOptions<"listSamplingEvalRules">,
  ): Promise<EvalMethodOutput<"listSamplingEvalRules">> {
    return this._call("listSamplingEvalRules", request, options);
  }

  updateSamplingRule(
    request: EvalMethodInput<"updateSamplingEvalRule">,
    options?: EvalMethodOptions<"updateSamplingEvalRule">,
  ): Promise<EvalMethodOutput<"updateSamplingEvalRule">> {
    return this._call("updateSamplingEvalRule", request, options);
  }

  deleteSamplingRule(
    request: EvalMethodInput<"deleteSamplingEvalRule">,
    options?: EvalMethodOptions<"deleteSamplingEvalRule">,
  ): Promise<EvalMethodOutput<"deleteSamplingEvalRule">> {
    return this._call("deleteSamplingEvalRule", request, options);
  }

  runSamplingRuleNow(
    request: EvalMethodInput<"runSamplingEvalRuleNow">,
    options?: EvalMethodOptions<"runSamplingEvalRuleNow">,
  ): Promise<EvalMethodOutput<"runSamplingEvalRuleNow">> {
    return this._call("runSamplingEvalRuleNow", request, options);
  }

  /**
   * Ask Claude to draft an LLM-judge prompt + rubric for a given evaluation task.
   */
  generateScorer(
    request: EvalMethodInput<"generateScorer">,
    options?: EvalMethodOptions<"generateScorer">,
  ): Promise<EvalMethodOutput<"generateScorer">> {
    return this._call("generateScorer", request, options);
  }
}
