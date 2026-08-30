#!/usr/bin/env node

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
  ],
  "azure-openai": ["gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6-terra"],
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
const sourceIndex = args.indexOf("--source");
const sourcePath = sourceIndex >= 0 ? args[sourceIndex + 1] : undefined;

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

const added = [];
const updated = [];
const deprecated = [];

for (const [provider, sourceProvider] of Object.entries(sourceProviders)) {
  const sourceModels = source[sourceProvider]?.models;
  if (!sourceModels) {
    throw new Error(`models.dev provider ${sourceProvider} is missing`);
  }

  const modelsDir = path.join(providersDir, provider, "models");
  fs.mkdirSync(modelsDir, { recursive: true });

  const existing = new Map();
  for (const filename of fs
    .readdirSync(modelsDir)
    .filter((name) => name.endsWith(".yaml"))) {
    const filePath = path.join(modelsDir, filename);
    const contents = fs.readFileSync(filePath, "utf8");
    const modelName = readModelName(contents);
    if (modelName) existing.set(modelName, { filePath, contents });
  }

  // Reconcile every exact upstream match, including prices, limits,
  // capabilities, modalities, and model-scoped parameter metadata.
  for (const [modelName, file] of existing) {
    const model = getSourceModel(provider, modelName, sourceModels);
    if (!model) {
      if (shouldDeprecateMissing && fullInventoryProviders.has(provider)) {
        const next = setTopLevelScalar(file.contents, "status", "deprecated");
        if (next !== file.contents) {
          deprecated.push(`${provider}/${modelName}`);
          if (shouldWrite) fs.writeFileSync(file.filePath, next);
        }
      }
      continue;
    }

    const next = reconcileModel(provider, modelName, file.contents, model);
    if (next === file.contents) continue;
    updated.push(`${provider}/${modelName}`);
    if (shouldWrite) fs.writeFileSync(file.filePath, next);
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
    added.push(`${provider}/${modelName}`);
    if (shouldWrite)
      fs.writeFileSync(filePath, renderModel(provider, modelName, model));
  }
}

console.log(
  `${shouldWrite ? "Updated" : "Would update"} ${updated.length} existing models`,
);
console.log(
  `${shouldWrite ? "Marked" : "Would mark"} ${deprecated.length} models deprecated`,
);
for (const model of deprecated) console.log(`  deprecate ${model}`);
console.log(`${shouldWrite ? "Added" : "Would add"} ${added.length} models`);
for (const model of added) console.log(`  ${model}`);

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
    ["fireworks", "groq", "huggingface", "nvidia-nim", "together"].includes(
      provider,
    )
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
    fireworks: "Fireworks",
    huggingface: "Hugging Face",
    "nvidia-nim": "NVIDIA NIM",
    openrouter: "OpenRouter",
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

  return `${lines.join("\n")}\n`;
}

function reconcileModel(provider, modelName, contents, model) {
  let next = contents;
  next = setTopLevelScalar(next, "status", modelStatus(modelName, model));
  if (model.release_date) {
    next = setTopLevelScalar(
      next,
      "release_date",
      model.release_date,
      "status",
    );
  }
  next = replaceTopLevelBlock(next, "cost", renderCost(model));
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

  for (const key of ["structured_output", "parameters", "variants"]) {
    next = removeTopLevelBlock(next, key);
  }
  // Regenerate rather than only strip: the Parameters tab is driven by these
  // descriptors, so a sync that dropped them would empty it for every model.
  const metadata = renderParameterMetadata(provider, model);
  if (metadata.length > 0) {
    next = `${next.trimEnd()}\n\n${metadata.join("\n")}\n`;
  }
  return next;
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
    /^(knowledge_cutoff|notes|structured_output|parameters|variants):/m;
  if (metadataAnchor.test(next)) {
    return next.replace(metadataAnchor, `${block}\n\n$&`);
  }
  return `${next.trimEnd()}\n\n${block}\n`;
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
