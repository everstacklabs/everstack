#!/usr/bin/env node

// Usage: node sync-models-dev.mjs --source <api.json> [--write]
//          [--only <provider[,provider...]>] [--added-in-version <x.y.z>]
//          [--reconcile [--include-verified] [--deprecate-missing]]
//
// By default the script only ADDS models the catalog does not have yet:
// existing model files are never read for rewriting, so curated prices,
// statuses and descriptors stay exactly as they are. Without --write it is a
// dry run that prints the path of every file it would add or change.
//
// --only restricts the run (additions, reconcile and deprecation) to the
// listed catalog provider slugs, e.g. --only aws-bedrock,vertex-ai. Unknown
// slugs are an error. Without it every provider is processed.
// --added-in-version stamps `added_in_version: "<x.y.z>"` on newly written
// files (the field is not touched on existing files).
// --additions-only is accepted for compatibility; it is now the default.
//
// --reconcile opts in to rewriting EXISTING files from models.dev (status,
// release date, cost, limits, capabilities, modalities). The dry run then
// lists the changed top-level keys per file. Even with --reconcile:
//   - a file whose header has a line starting `# Verified` is skipped unless
//     --include-verified is also passed (hand-verified data wins);
//   - `status: "deprecated"` is never changed back;
//   - cost keys models.dev does not carry (`long_context`,
//     `cache_write_1h_per_1k`, ...) are kept, and a hand-written
//     `unsupported_parameters` block is kept verbatim; the run aborts rather
//     than drop any of them.
// --deprecate-missing (requires --reconcile) marks full-inventory models that
// models.dev no longer lists as deprecated; review the dry run first.
//
// Every check that can fail runs before the first file is written, so an
// error leaves the catalog untouched.

import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const SOURCE_URL = "https://models.dev/api.json";
const AUDITED_AFTER = "2026-05-09";

const sourceProviders = {
  anthropic: "anthropic",
  "aws-bedrock": "amazon-bedrock",
  "azure-openai": "azure",
  cerebras: "cerebras",
  cohere: "cohere",
  deepseek: "deepseek",
  fireworks: "fireworks-ai",
  google: "google",
  groq: "groq",
  huggingface: "huggingface",
  minimax: "minimax",
  mistral: "mistral",
  moonshot: "moonshotai",
  "nvidia-nim": "nvidia",
  openai: "openai",
  openrouter: "openrouter",
  perplexity: "perplexity",
  qwen: "alibaba",
  together: "togetherai",
  "vertex-ai": "google-vertex",
  xai: "xai",
  zai: "zai",
  // Curated OpenAI-compatible hosts added in 2.11.0. None is in
  // fullInventoryProviders: their catalogs hold a reviewed subset of each
  // host's inventory, so only reconcile (or explicit additions) touches them.
  baseten: "baseten",
  chutes: "chutes",
  deepinfra: "deepinfra",
  meta: "meta",
  nebius: "nebius",
  novita: "novita-ai",
  sambanova: "sambanova",
  tencent: "tencent-tokenhub",
};

// Direct providers can be reconciled against the complete current
// text-generation inventory. Deployment-scoped and meta providers stay
// curated because their actual model IDs depend on the account or region.
const fullInventoryProviders = new Set([
  "anthropic",
  "cerebras",
  "cohere",
  "deepseek",
  "google",
  "groq",
  "minimax",
  "mistral",
  "moonshot",
  "openai",
  "perplexity",
  "qwen",
  "xai",
  "zai",
]);

// Providers whose adapters translate the normalized reasoning_effort setting
// into their native request format. Restored verbatim from d21619be1/172200fd8:
// these match the providers whose existing model files carry effort `variants:`
// blocks (anthropic, azure-openai, cerebras, ... xai). aws-bedrock, vertex-ai,
// qwen and zai have none, so they are deliberately absent.
const effortVariantProviders = new Set([
  "anthropic",
  "azure-openai",
  "cerebras",
  "cohere",
  "deepseek",
  "fireworks",
  "google",
  "groq",
  "huggingface",
  "minimax",
  "mistral",
  "moonshot",
  "nvidia-nim",
  "openai",
  "openrouter",
  "perplexity",
  "together",
  "xai",
]);

// Budget and toggle controls are only published when the provider adapter
// translates them to its native API. This avoids rendering settings that are
// accepted by the UI but silently discarded at runtime.
const reasoningBudgetProviders = new Set([
  "anthropic",
  "cohere",
  "google",
  "qwen",
]);
const reasoningToggleProviders = new Set([
  "anthropic",
  "cohere",
  "google",
  "qwen",
]);

// Alibaba documents these open Qwen models as supporting thinking only on
// streaming requests. Keep the constraint in catalog metadata so the UI can
// explain it and the runtime can reject an incompatible synchronous request.
const qwenStreamingOnlyReasoningModels = new Set([
  "qwen3-235b-a22b",
  "qwen3-32b",
]);

// Provider-compatible additions for deployment-scoped and meta providers.
const additions = {
  anthropic: [
    "claude-opus-4-8",
    "claude-fable-5",
    "claude-sonnet-5",
    "claude-opus-5",
  ],
  "aws-bedrock": [
    "anthropic.claude-opus-4-8",
    "anthropic.claude-fable-5",
    "anthropic.claude-sonnet-5",
    "anthropic.claude-opus-5",
    "xai.grok-4.6",
    "anthropic.claude-haiku-5-5",
    "anthropic.claude-fable-5-2",
    "us.anthropic.claude-fable-5-2",
    "global.anthropic.claude-fable-5-2",
    "us.moonshotai.kimi-k3.1",
    "global.moonshotai.kimi-k3.1",
    "us.openai.gpt-6.1-terra",
    "global.openai.gpt-6.1-terra",
    "us.openai.gpt-6.2-sol",
    "global.openai.gpt-6.2-sol",
    "us.openai.gpt-6.2-astra",
    "global.openai.gpt-6.2-astra",
  ],
  "azure-openai": [
    "gpt-5.6-luna",
    "gpt-5.6-sol",
    "gpt-5.6-terra",
    "gpt-6.1-terra",
    "gpt-6.2-sol",
    "gpt-6.2-astra",
    "claude-haiku-5-5",
    "claude-fable-5-2",
  ],
  cohere: ["command-a-plus-05-2026", "north-mini-code-1-0"],
  fireworks: [
    "accounts/fireworks/models/glm-5p2",
    "accounts/fireworks/routers/glm-5p2-fast",
    "accounts/fireworks/models/kimi-k2p7-code",
    "accounts/fireworks/routers/kimi-k2p7-code-fast",
    "accounts/fireworks/models/minimax-m3",
    "accounts/fireworks/models/qwen3p7-plus",
    "accounts/fireworks/models/inkling",
    "accounts/fireworks/models/kimi-k3",
    "accounts/fireworks/routers/kimi-k3-fast",
    "accounts/fireworks/models/deepseek-v4-flash-0731",
    "accounts/fireworks/models/deepseek-v4-pro-0813",
    "accounts/fireworks/models/qwen3p8-max",
    "accounts/fireworks/models/nemotron-lightning-3p5-30b-a3b",
    "accounts/fireworks/models/muse-glimmer-30b",
    "accounts/fireworks/models/muse-spark-open",
    "accounts/fireworks/routers/deepseek-v4p1-flash-1017-ultrafast",
    "accounts/fireworks/routers/deepseek-v4p1-flash-1017-spot",
    "accounts/fireworks/routers/deepseek-flash-latest",
    "accounts/fireworks/models/deepseek-v4p1-flash-1017",
    "accounts/fireworks/models/minimax-m3p1-flash",
    "accounts/fireworks/models/kimi-k3p1-pro",
    "accounts/fireworks/routers/deepseek-v4p1-flash-1017-flex",
    "accounts/fireworks/routers/deepseek-v4p1-flash-1017-us",
    "accounts/fireworks/routers/kimi-k3p1-pro-us",
    "accounts/fireworks/routers/kimi-latest",
    "accounts/fireworks/models/ultramarine-1-flash-us",
    "accounts/fireworks/models/qwen4-max",
    "accounts/fireworks/models/hy4-flash",
  ],
  google: [
    "gemini-3.5-flash",
    "gemini-flash-latest",
    "gemini-3.5-flash-lite",
    "gemini-3.6-flash",
  ],
  huggingface: [
    "zai-org/GLM-5.2",
    "moonshotai/Kimi-K2.7-Code",
    "MiniMaxAI/MiniMax-M3",
    "stepfun-ai/Step-3.7-Flash",
    "thinkingmachines/Inkling",
    "thinkingmachines/Inkling-Small",
    "moonshotai/Kimi-K3",
    "deepseek-ai/DeepSeek-V4-Flash-0731",
    "deepseek-ai/DeepSeek-V4-Pro-0813",
    "Qwen/Qwen3.8-2.4T-A95B",
    "deepseek-ai/DeepSeek-V4.1-Flash-1017",
    "MiniMaxAI/MiniMax-M3.1-Flash",
    "moonshotai/Kimi-K3.1-Pro",
    "tencent/Hy4",
    "thinkingmachines/Inkling-2-Small",
    "tencent/Hy4-flash",
    "mistralai/Mistral-Large-4-1T-A52B",
    "reflection/Beam-1.0-501B-A23B",
    "nvidia/NVIDIA-Nemotron-3.5-Super-VL-120B-A12B-NVFP4",
    "nvidia/NVIDIA-Nemotron-3.5-Super-VL-120B-A12B-BF16",
    "meta-models/Muse-Spark-Open-816B-A42B",
    "thinkingmachines/Inkling-2-Mini",
  ],
  minimax: ["MiniMax-M3"],
  moonshot: ["kimi-k2.7-code", "kimi-k2.7-code-highspeed", "kimi-k3"],
  "nvidia-nim": [
    "z-ai/glm-5.2",
    "nvidia/nemotron-3-ultra-550b-a55b",
    "minimaxai/minimax-m3",
    "stepfun-ai/step-3.7-flash",
    "thinkingmachines/inkling",
    "moonshotai/kimi-k3",
    "deepseek-ai/deepseek-v4-flash-0731",
    "meta/muse-glimmer-30b",
    "nvidia/nemotron-3.5-lightning-30b-a3b",
    "nvidia/nemotron-3.5-super-vl-120b-a12b",
    "minimaxai/minimax-m3.1-flash",
  ],
  openai: [
    "gpt-realtime-2.1",
    "gpt-5.6",
    "gpt-5.6-luna",
    "gpt-5.6-sol",
    "gpt-5.6-terra",
  ],
  openrouter: [
    "anthropic/claude-opus-4.8",
    "anthropic/claude-opus-4.8-fast",
    "anthropic/claude-fable-5",
    "anthropic/claude-sonnet-5",
    "anthropic/claude-opus-5",
    "anthropic/claude-opus-5-fast",
    "google/gemini-3.5-flash",
    "google/gemini-3.5-flash-lite",
    "google/gemini-3.6-flash",
    "moonshotai/kimi-k2.7-code",
    "moonshotai/kimi-k3",
    "openai/gpt-5.6-luna",
    "openai/gpt-5.6-luna-pro",
    "openai/gpt-5.6-sol",
    "openai/gpt-5.6-sol-pro",
    "openai/gpt-5.6-terra",
    "openai/gpt-5.6-terra-pro",
    "x-ai/grok-4.5",
    "z-ai/glm-5.2",
    "qwen/qwen3.7-max",
    "qwen/qwen3.7-plus",
    "minimax/minimax-m3",
    "nvidia/nemotron-3-ultra-550b-a55b",
    "stepfun/step-3.7-flash",
    "thinkingmachines/inkling",
    "x-ai/grok-4.6",
    "google/gemini-3.7-flash",
    "z-ai/glm-5.3",
    "qwen/qwen3.8-max",
    "qwen/qwen3.8-27b",
    "qwen/qwen3.8-2.4t-a95b",
    "deepseek/deepseek-v4-flash-0731",
    "deepseek/deepseek-v4-pro-0813",
    "deepseek/deepseek-v4-flash-vision-exp",
    "bytedance-seed/seed-2-1-turbo",
    "upstage/solar-pro4",
    "nvidia/nemotron-3.5-lightning",
    "meta/muse-glimmer-30b",
    "meta/muse-spark-1.2",
    "thinkingmachines/inkling-small",
    "tencent/hy-mt2-30b-a3b",
    "sakana/sakana-namazu",
  ],
  qwen: ["qwen3.7-max", "qwen3.7-plus"],
  together: [
    "thinkingmachines/Inkling",
    "zai-org/GLM-5.2",
    "moonshotai/Kimi-K2.7-Code",
    "MiniMaxAI/MiniMax-M3",
    "nvidia/nemotron-3-ultra-550b-a55b",
    "Qwen/Qwen3.7-Max",
    "deepseek-ai/DeepSeek-V4-Flash-0731",
    "deepseek-ai/DeepSeek-V4-Pro-0813",
    "reflection/Beam-1.0-501B-A23B",
    "nvidia/NVIDIA-Nemotron-3.5-Super-VL",
    "meta-models/Muse-Spark-Open-816B-A42B",
    "deepseek-ai/DeepSeek-V4.1-Flash-1017",
    "MiniMaxAI/MiniMax-M3.1-Flash",
    "moonshotai/Kimi-K3.1-Pro",
    "tencent/Hy4",
    "thinkingmachines/Inkling-2-Small",
    "Qwen/Qwen4-Max",
    "elorian-labs/glimpse-1.0",
    "deepseek-ai/DeepSeek-V4.1-Flash-1017-fast",
    "ricursive-ai/Ripple-65B-Preview",
  ],
  "vertex-ai": [
    "claude-fable-5@default",
    "claude-opus-4-8@default",
    "claude-sonnet-5@default",
    "claude-opus-5@default",
    "gemini-3.5-flash",
    "gemini-flash-latest",
    "gemini-3.5-flash-lite",
    "gemini-3.6-flash",
    "gemini-3.7-flash",
    "gemini-4-argon",
    "gemini-4-carbon",
    "gemini-4-hydrogen",
    "gemini-4-neon",
    "claude-haiku-5-5@default",
    "claude-fable-5-2@default",
  ],
  xai: ["grok-4.5"],
};

// Primary-source overrides for provider entries that have shipped but are not
// yet represented in the models.dev provider inventory.
const sourceModelOverrides = {
  "vertex-ai/claude-fable-5@default": {
    sourceProvider: "anthropic",
    sourceModel: "claude-fable-5",
    releaseDate: "2026-06-09",
  },
};

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const catalogDir = path.dirname(scriptDir);
const providersDir = path.join(catalogDir, "providers");
const args = process.argv.slice(2);
const shouldWrite = args.includes("--write");
// Marking every catalog model that models.dev does not list as deprecated is
// unsafe for this catalog: it carries dated snapshot ids such as
// gpt-5.1-2025-11-13 and non-text products (realtime, audio, image,
// embeddings, moderation) that the upstream text-generation feed never
// contains. Opt in deliberately after reviewing the printed list.
const shouldDeprecateMissing = args.includes("--deprecate-missing");
const shouldReconcile = args.includes("--reconcile");
const includeVerified = args.includes("--include-verified");
if (args.includes("--additions-only") && shouldReconcile) {
  throw new Error("--additions-only and --reconcile are mutually exclusive");
}
if (includeVerified && !shouldReconcile) {
  throw new Error("--include-verified requires --reconcile");
}
if (shouldDeprecateMissing && !shouldReconcile) {
  throw new Error("--deprecate-missing requires --reconcile");
}
const addedInVersionIndex = args.indexOf("--added-in-version");
const addedInVersion =
  addedInVersionIndex >= 0 ? args[addedInVersionIndex + 1] : undefined;
if (addedInVersionIndex >= 0 && !/^\d+\.\d+\.\d+$/.test(addedInVersion ?? "")) {
  throw new Error("--added-in-version requires a x.y.z version");
}
const sourceIndex = args.indexOf("--source");
const sourcePath = sourceIndex >= 0 ? args[sourceIndex + 1] : undefined;

const onlyIndex = args.indexOf("--only");
const onlyValue = onlyIndex >= 0 ? args[onlyIndex + 1] : undefined;
if (onlyIndex >= 0 && (!onlyValue || onlyValue.startsWith("--"))) {
  throw new Error("--only requires a comma-separated provider list");
}
const onlyProviders = onlyValue
  ? new Set(onlyValue.split(",").map((name) => name.trim()).filter(Boolean))
  : undefined;
for (const name of onlyProviders ?? []) {
  if (!(name in sourceProviders)) {
    throw new Error(`--only: unknown provider ${name}`);
  }
}

if (sourceIndex >= 0 && !sourcePath) {
  throw new Error("--source requires a JSON file path");
}

const source = sourcePath
  ? JSON.parse(fs.readFileSync(path.resolve(sourcePath), "utf8"))
  : await fetch(SOURCE_URL).then((response) => {
      if (!response.ok) {
        throw new Error(`models.dev returned ${response.status}`);
      }
      return response.json();
    });

// Plan every write first. Anything that throws does so here, before the
// catalog is touched; the write phase below only writes planned contents.
const added = [];
const updated = [];
const deprecated = [];
const skippedVerified = [];
const writes = new Map();

for (const [provider, sourceProvider] of Object.entries(sourceProviders)) {
  if (onlyProviders && !onlyProviders.has(provider)) continue;
  const sourceModels = source[sourceProvider]?.models;
  if (!sourceModels) {
    throw new Error(`models.dev provider ${sourceProvider} is missing`);
  }

  const modelsDir = path.join(providersDir, provider, "models");
  const existing = new Map();
  if (fs.existsSync(modelsDir)) {
    for (const filename of fs
      .readdirSync(modelsDir)
      .filter((name) => name.endsWith(".yaml"))) {
      const filePath = path.join(modelsDir, filename);
      const contents = fs.readFileSync(filePath, "utf8");
      const modelName = readModelName(contents);
      if (modelName) existing.set(modelName, { filePath, contents });
    }
  }

  // Reconcile (opt-in) every exact upstream match, including prices, limits,
  // capabilities, modalities, and model-scoped parameter metadata.
  for (const [modelName, file] of shouldReconcile ? existing : []) {
    if (!includeVerified && hasVerifiedHeader(file.contents)) {
      skippedVerified.push(file.filePath);
      continue;
    }
    const model = getSourceModel(provider, modelName, sourceModels);
    if (!model) {
      if (shouldDeprecateMissing && fullInventoryProviders.has(provider)) {
        const next = setTopLevelScalar(file.contents, "status", "deprecated");
        if (next !== file.contents) {
          deprecated.push({ filePath: file.filePath, keys: ["status"] });
          planWrite(file.filePath, next);
        }
      }
      continue;
    }

    const next = reconcileModel(provider, modelName, file.contents, model);
    // A rewrite that only moves blank lines is churn, not a change.
    if (withoutBlankLines(next) === withoutBlankLines(file.contents)) continue;
    updated.push({
      filePath: file.filePath,
      keys: changedTopLevelKeys(file.contents, next),
    });
    planWrite(file.filePath, next);
  }

  const candidateModels = fullInventoryProviders.has(provider)
    ? Object.entries(sourceModels)
        .filter(([modelName, model]) =>
          isSupportedTextGenerationModel(model, modelName),
        )
        .map(([modelName]) => modelName)
    : (additions[provider] ?? []);

  for (const modelName of candidateModels) {
    if (existing.has(modelName)) continue;

    const model = getSourceModel(provider, modelName, sourceModels);
    if (!model) {
      throw new Error(
        `models.dev model ${sourceProvider}/${modelName} is missing`,
      );
    }
    if (
      !fullInventoryProviders.has(provider) &&
      (!model.release_date || model.release_date <= AUDITED_AFTER)
    ) {
      throw new Error(
        `${sourceProvider}/${modelName} is not a post-${AUDITED_AFTER} addition`,
      );
    }
    if (!isSupportedTextGenerationModel(model, modelName)) {
      throw new Error(
        `${sourceProvider}/${modelName} is not a supported text-generation model`,
      );
    }

    const filePath = path.join(modelsDir, modelFilename(provider, modelName));
    if (fs.existsSync(filePath)) {
      throw new Error(`catalog path collision at ${filePath}`);
    }
    added.push({ filePath, keys: [] });
    planWrite(filePath, renderModel(provider, modelName, model));
  }
}

if (shouldWrite) {
  for (const [filePath, contents] of writes) {
    fs.mkdirSync(path.dirname(filePath), { recursive: true });
    fs.writeFileSync(filePath, contents);
  }
}

const verb = (dry, done) => (shouldWrite ? done : dry);
console.log(`${verb("Would add", "Added")} ${added.length} models`);
for (const entry of added) console.log(`  add ${displayPath(entry.filePath)}`);
if (shouldReconcile) {
  console.log(
    `${verb("Would update", "Updated")} ${updated.length} existing models`,
  );
  for (const entry of updated) {
    console.log(
      `  update ${displayPath(entry.filePath)}: ${entry.keys.join(", ")}`,
    );
  }
  console.log(
    `${verb("Would mark", "Marked")} ${deprecated.length} models deprecated`,
  );
  for (const entry of deprecated) {
    console.log(`  deprecate ${displayPath(entry.filePath)}`);
  }
  console.log(
    `Skipped ${skippedVerified.length} verified files` +
      (skippedVerified.length > 0 ? " (pass --include-verified to reconcile them)" : ""),
  );
} else {
  console.log("Existing models left untouched (pass --reconcile to update them)");
}

function planWrite(filePath, contents) {
  if (writes.has(filePath)) {
    throw new Error(`two planned writes target ${filePath}`);
  }
  writes.set(filePath, contents);
}

function withoutBlankLines(contents) {
  return contents
    .split("\n")
    .filter((line) => line.trim() !== "")
    .join("\n");
}

function displayPath(filePath) {
  return path.relative(catalogDir, filePath);
}

// The header is the run of comment lines at the top of a model file.
function hasVerifiedHeader(contents) {
  for (const line of contents.split("\n")) {
    if (!line.startsWith("#")) return false;
    if (line.startsWith("# Verified")) return true;
  }
  return false;
}

function topLevelKeys(contents) {
  const keys = [];
  for (const line of contents.split("\n")) {
    const match = line.match(/^([A-Za-z_][\w-]*):/);
    if (match) keys.push(match[1]);
  }
  return keys;
}

function changedTopLevelKeys(before, after) {
  const keys = new Set([...topLevelKeys(before), ...topLevelKeys(after)]);
  return [...keys].filter(
    (key) => topLevelBlock(before, key) !== topLevelBlock(after, key),
  );
}

function getSourceModel(provider, modelName, sourceModels) {
  const override = sourceModelOverrides[`${provider}/${modelName}`];
  if (!override) return sourceModels[modelName];

  const model = source[override.sourceProvider]?.models?.[override.sourceModel];
  if (!model) return undefined;
  return {
    ...model,
    release_date: override.releaseDate ?? model.release_date,
  };
}

function readModelName(contents) {
  const match = contents.match(/^name:\s*(.+?)\s*$/m);
  if (!match) return undefined;
  const value = match[1];
  if (value.startsWith('"')) return JSON.parse(value);
  if (value.startsWith("'") && value.endsWith("'")) return value.slice(1, -1);
  return value;
}

function modelFilename(provider, modelName) {
  let filename = modelName;
  if (
    [
      "baseten",
      "chutes",
      "deepinfra",
      "fireworks",
      "groq",
      "huggingface",
      "nebius",
      "novita",
      "nvidia-nim",
      "together",
    ].includes(provider)
  ) {
    filename = filename.replaceAll("/", "__");
  } else if (provider === "openrouter") {
    filename = filename.replaceAll("/", "-");
  }
  filename = filename.replaceAll(":", "-");
  return `${filename}.yaml`;
}

function renderModel(provider, modelName, model) {
  const capabilities = modelCapabilities(provider, modelName, model);
  const suffix = {
    "aws-bedrock": "Bedrock",
    "azure-openai": "Azure",
    baseten: "Baseten",
    chutes: "Chutes",
    deepinfra: "DeepInfra",
    fireworks: "Fireworks",
    huggingface: "Hugging Face",
    nebius: "Nebius",
    novita: "Novita",
    "nvidia-nim": "NVIDIA NIM",
    openrouter: "OpenRouter",
    sambanova: "SambaNova",
    together: "Together",
    "vertex-ai": "Vertex",
  }[provider];
  const displayName = suffix ? `${model.name} (${suffix})` : model.name;
  const status = modelStatus(modelName, model);
  const lines = [
    `name: ${quote(modelName)}`,
    `display_name: ${quote(displayName)}`,
    `family: ${quote(model.family || modelName)}`,
    `status: ${quote(status)}`,
    `release_date: ${quote(model.release_date)}`,
    "",
    "cost:",
    `  input_per_1k: ${perThousand(model.cost?.input)}`,
    `  output_per_1k: ${perThousand(model.cost?.output)}`,
  ];

  if (typeof model.cost?.cache_read === "number") {
    lines.push(`  cache_read_per_1k: ${perThousand(model.cost.cache_read)}`);
  }
  if (typeof model.cost?.cache_write === "number") {
    lines.push(`  cache_write_per_1k: ${perThousand(model.cost.cache_write)}`);
  }

  lines.push(
    "",
    "limits:",
    `  max_tokens: ${model.limit?.context ?? 0}`,
    `  max_completion_tokens: ${model.limit?.output ?? 0}`,
    "",
    "capabilities:",
    ...capabilities.map((capability) => `  - ${capability}`),
    "",
    "modalities:",
    `  input: ${JSON.stringify(model.modalities?.input ?? ["text"])}`,
    `  output: ${JSON.stringify(model.modalities?.output ?? ["text"])}`,
  );

  if (model.knowledge) {
    lines.push("", `knowledge_cutoff: ${quote(model.knowledge)}`);
  }
  if (model.description) {
    lines.push(`notes: ${quote(model.description)}`);
  }

  // New models get generated parameter descriptors so the Parameters tab is
  // not empty; reconcile never regenerates them afterwards.
  const metadata = renderParameterMetadata(provider, model);
  if (metadata.length > 0) lines.push(...metadata);
  if (addedInVersion) lines.push(`added_in_version: ${quote(addedInVersion)}`);

  return `${lines.join("\n")}\n`;
}

function reconcileModel(provider, modelName, contents, model) {
  let next = contents;
  // A deprecation is a deliberate catalog decision; models.dev reporting the
  // model as live again never reverses it.
  if (readTopLevelScalar(contents, "status") !== "deprecated") {
    next = setTopLevelScalar(next, "status", modelStatus(modelName, model));
  }
  if (model.release_date) {
    next = setTopLevelScalar(
      next,
      "release_date",
      model.release_date,
      "status",
    );
  }
  next = replaceTopLevelBlock(
    next,
    "cost",
    renderCost(model).concat(curatedCostLines(contents)),
  );
  next = replaceTopLevelBlock(next, "limits", renderLimits(model));
  next = replaceTopLevelBlock(next, "capabilities", [
    "capabilities:",
    ...modelCapabilities(provider, modelName, model).map(
      (value) => `  - ${value}`,
    ),
  ]);
  next = replaceTopLevelBlock(next, "modalities", [
    "modalities:",
    `  input: ${JSON.stringify(model.modalities?.input ?? ["text"])}`,
    `  output: ${JSON.stringify(model.modalities?.output ?? ["text"])}`,
  ]);

  // Parameter metadata is generate-once, preserve-thereafter (see d21619be1):
  // curated descriptors carry a per-provider capability matrix this emitter
  // does not encode, so a model that already has them keeps them verbatim.
  const hasCuratedMetadata = ["structured_output", "parameters", "variants"].some(
    (key) => topLevelBlockPresent(next, key),
  );
  if (!hasCuratedMetadata) {
    const metadata = renderParameterMetadata(provider, model);
    if (metadata.length > 0) {
      next = `${next.trimEnd()}\n\n${metadata.join("\n")}\n`;
    }
  }

  // unsupported_parameters is hand-written from provider documentation
  // (models.dev does not carry it). Reconcile never rewrites it; fail loudly
  // if a block edit above ever drops or alters it.
  const before = topLevelBlock(contents, "unsupported_parameters");
  if (before !== topLevelBlock(next, "unsupported_parameters")) {
    throw new Error(
      `${provider}/${modelName}: reconcile would change unsupported_parameters`,
    );
  }
  // Hand-verified cost detail models.dev does not carry must survive.
  for (const key of ["long_context", "cache_write_1h_per_1k"]) {
    const was = costSubBlock(contents, key);
    if (was !== undefined && was !== costSubBlock(next, key)) {
      throw new Error(`${provider}/${modelName}: reconcile would change cost.${key}`);
    }
  }
  return next;
}

// curatedCostLines returns the lines of every cost entry renderCost does not
// produce (long_context, cache_write_1h_per_1k, ...), nested lines included,
// so reconcile can carry them over verbatim.
function curatedCostLines(contents) {
  // Cost keys renderCost emits; every other cost entry is curated by hand.
  const generatedCostKeys = new Set([
    "input_per_1k",
    "output_per_1k",
    "cache_read_per_1k",
    "cache_write_per_1k",
  ]);
  const block = topLevelBlock(contents, "cost");
  if (!block) return [];
  const kept = [];
  let keeping = false;
  for (const line of block.split("\n").slice(1)) {
    const entry = line.match(/^ {2}([A-Za-z_][\w-]*):/);
    if (entry) keeping = !generatedCostKeys.has(entry[1]);
    else if (!/^ {3,}/.test(line)) keeping = false;
    if (keeping && line.trim() !== "") kept.push(line);
  }
  return kept;
}

function costSubBlock(contents, key) {
  const block = topLevelBlock(contents, "cost");
  if (!block) return undefined;
  const lines = block.split("\n");
  const start = lines.findIndex((line) => line.startsWith(`  ${key}:`));
  if (start < 0) return undefined;
  let end = start + 1;
  while (end < lines.length && /^ {3,}/.test(lines[end])) end++;
  return lines.slice(start, end).join("\n");
}

function readTopLevelScalar(contents, key) {
  const match = contents.match(
    new RegExp(`^${escapeRegExp(key)}:\\s*(.+?)\\s*$`, "m"),
  );
  if (!match) return undefined;
  const value = match[1];
  if (value.startsWith('"')) return JSON.parse(value);
  if (value.startsWith("'") && value.endsWith("'")) return value.slice(1, -1);
  return value;
}

function topLevelBlock(contents, key) {
  const lines = contents.split("\n");
  const start = lines.findIndex((line) => line.startsWith(`${key}:`));
  if (start < 0) return undefined;
  let end = start + 1;
  while (end < lines.length && /^[ \t]/.test(lines[end])) end++;
  return lines.slice(start, end).join("\n");
}

function renderCost(model) {
  const lines = [
    "cost:",
    `  input_per_1k: ${perThousand(model.cost?.input)}`,
    `  output_per_1k: ${perThousand(model.cost?.output)}`,
  ];
  if (typeof model.cost?.cache_read === "number") {
    lines.push(`  cache_read_per_1k: ${perThousand(model.cost.cache_read)}`);
  }
  if (typeof model.cost?.cache_write === "number") {
    lines.push(`  cache_write_per_1k: ${perThousand(model.cost.cache_write)}`);
  }
  return lines;
}

function renderLimits(model) {
  return [
    "limits:",
    `  max_tokens: ${model.limit?.context ?? 0}`,
    `  max_completion_tokens: ${model.limit?.output ?? 0}`,
  ];
}

function isSupportedTextGenerationModel(model, modelName = "") {
  const output = model.modalities?.output ?? [];
  if (output.length !== 1 || output[0] !== "text") return false;
  if (
    /(?:^|[\/_.-])(?:embed(?:ding)?|rerank|whisper|transcrib|tts|speech|moderation|safeguard|guard|ocr|translate|translation|realtime|live|computer-use|robotics)(?:$|[\/_.-])/i.test(
      modelName,
    ) ||
    /(?:^|\/)qwen-mt-/i.test(modelName)
  ) {
    return false;
  }
  return (
    model.tool_call === true ||
    model.reasoning === true ||
    model.temperature === true
  );
}

function modelStatus(modelName, model) {
  if (model.status) return model.status;
  return /(?:preview|beta|experimental)/i.test(modelName)
    ? "preview"
    : "stable";
}

function setTopLevelScalar(contents, key, value, insertAfter) {
  const line = `${key}: ${quote(value)}`;
  const pattern = new RegExp(`^${escapeRegExp(key)}:.*$`, "m");
  if (pattern.test(contents)) return contents.replace(pattern, line);
  if (insertAfter) {
    const anchor = new RegExp(`^(${escapeRegExp(insertAfter)}:.*)$`, "m");
    if (anchor.test(contents)) return contents.replace(anchor, `$1\n${line}`);
  }
  return `${contents.trimEnd()}\n${line}\n`;
}

function replaceTopLevelBlock(contents, key, lines) {
  const next = removeTopLevelBlock(contents, key);
  const block = lines.join("\n");
  const metadataAnchor =
    /^(knowledge_cutoff|notes|unsupported_parameters|structured_output|parameters|variants):/m;
  if (metadataAnchor.test(next)) {
    return next.replace(metadataAnchor, `${block}\n\n$&`);
  }
  return `${next.trimEnd()}\n\n${block}\n`;
}

function topLevelBlockPresent(contents, key) {
  return new RegExp(`^${escapeRegExp(key)}:`, "m").test(contents);
}

function titleCase(value) {
  return value.charAt(0).toUpperCase() + value.slice(1);
}

function removeTopLevelBlock(contents, key) {
  const lines = contents.split("\n");
  const start = lines.findIndex((line) => line.startsWith(`${key}:`));
  if (start < 0) return contents;

  let end = start + 1;
  while (
    end < lines.length &&
    (lines[end].trim() === "" || /^[ \t]/.test(lines[end]))
  ) {
    end++;
  }
  lines.splice(start, end - start);
  return lines.join("\n").replace(/\n{3,}/g, "\n\n");
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function modelCapabilities(provider, modelName, model) {
  const capabilities = ["chat"];
  if (model.tool_call) capabilities.push("function_calling");
  if (model.modalities?.input?.includes("image")) capabilities.push("vision");
  if (model.reasoning) {
    if (
      provider === "anthropic" ||
      (provider === "aws-bedrock" && modelName.includes("anthropic."))
    ) {
      capabilities.push("extended_thinking");
    } else {
      capabilities.push("reasoning");
    }
  }
  if (provider === "anthropic") capabilities.push("computer_use");
  return capabilities;
}

function perThousand(perMillion) {
  if (typeof perMillion !== "number") return 0;
  return Number((perMillion / 1000).toPrecision(12));
}

function quote(value) {
  return JSON.stringify(String(value));
}

function renderParameterMetadata(provider, model) {
  const parameters = [];
  const variants = [];
  const maxOutputTokens = model.limit?.output;

  if (typeof maxOutputTokens === "number" && maxOutputTokens > 0) {
    parameters.push({
      key: "max_output_tokens",
      displayName: "Max output tokens",
      type: "integer",
      min: 1,
      max: maxOutputTokens,
    });
  }

  if (model.temperature === true) {
    parameters.push({
      key: "temperature",
      displayName: "Temperature",
      type: "number",
    });
  }

  for (const option of model.reasoning_options ?? []) {
    if (
      option.type === "effort" &&
      Array.isArray(option.values) &&
      effortVariantProviders.has(provider)
    ) {
      parameters.push({
        key: "reasoning_effort",
        displayName: "Reasoning effort",
        type: "enum",
        options: option.values,
      });
      for (const value of option.values) {
        variants.push({
          id: value,
          displayName: reasoningVariantDisplayName(value),
          description: reasoningVariantDescription(value),
          parameters: { reasoning_effort: value },
        });
      }
    } else if (
      option.type === "budget_tokens" &&
      reasoningBudgetProviders.has(provider)
    ) {
      parameters.push({
        key: "reasoning_budget_tokens",
        displayName: "Reasoning token budget",
        type: "integer",
        min: option.min,
        max: option.max,
        requiresStreaming:
          provider === "qwen" && qwenStreamingOnlyReasoningModels.has(model.id),
      });
    } else if (
      option.type === "toggle" &&
      reasoningToggleProviders.has(provider)
    ) {
      parameters.push({
        key: "reasoning_enabled",
        displayName: "Reasoning",
        type: "boolean",
        requiresStreaming:
          provider === "qwen" && qwenStreamingOnlyReasoningModels.has(model.id),
      });
    }
  }

  const lines = [`structured_output: ${model.structured_output === true}`];
  if (parameters.length > 0) {
    lines.push("parameters:");
    for (const parameter of parameters) {
      lines.push(
        `  - key: ${quote(parameter.key)}`,
        `    display_name: ${quote(parameter.displayName)}`,
        `    type: ${quote(parameter.type)}`,
      );
      if (parameter.options?.length) {
        lines.push(`    options: ${JSON.stringify(parameter.options)}`);
      }
      if (typeof parameter.min === "number") {
        lines.push(
          `    min_value: ${parameter.min}`,
          "    has_min_value: true",
        );
      }
      if (typeof parameter.max === "number") {
        lines.push(
          `    max_value: ${parameter.max}`,
          "    has_max_value: true",
        );
      }
      if (parameter.requiresStreaming === true) {
        lines.push("    requires_streaming: true");
      }
    }
  }
  if (variants.length > 0) {
    lines.push("variants:");
    for (const variant of variants) {
      lines.push(
        `  - id: ${quote(variant.id)}`,
        `    display_name: ${quote(variant.displayName)}`,
        `    description: ${quote(variant.description)}`,
        "    parameters:",
      );
      for (const [key, value] of Object.entries(variant.parameters)) {
        lines.push(`      ${key}: ${quote(value)}`);
      }
    }
  }
  return lines;
}

function reasoningVariantDescription(value) {
  return (
    {
      none: "Fastest response with reasoning disabled when the model permits it.",
      minimal: "Minimal reasoning for low-latency, high-throughput work.",
      low: "Lower latency and cost for straightforward tasks.",
      medium: "Balanced reasoning for general-purpose work.",
      high: "Deeper reasoning for complex analysis and coding.",
      xhigh: "Extended reasoning for long-horizon agentic work.",
      max: "Maximum available reasoning depth for the model.",
    }[value] ?? `${titleCase(value)} reasoning effort.`
  );
}

function reasoningVariantDisplayName(value) {
  return (
    {
      none: "No reasoning",
      minimal: "Minimal reasoning",
      low: "Low reasoning",
      medium: "Medium reasoning",
      high: "High reasoning",
      xhigh: "Extra-high reasoning",
      max: "Maximum reasoning",
    }[value] ?? `${titleCase(value)} reasoning`
  );
}
