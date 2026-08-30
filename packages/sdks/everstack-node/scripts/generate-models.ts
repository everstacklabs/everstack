#!/usr/bin/env tsx
/// <reference types="node" />
/**
 * Model Type Generator
 *
 * Reads the model catalog from model-catalog/providers and generates
 * TypeScript types for type-safe model autocomplete in the SDK.
 *
 * Usage: pnpm generate:models
 */

import * as fs from "fs";
import * as path from "path";
import { fileURLToPath } from "url";
import { parse } from "yaml";

// Get directory name in ESM
const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const CATALOG_DIR = path.resolve(
    __dirname,
    process.env.EVERSTACK_MODEL_CATALOG_PATH ?? "../../../../model-catalog"
);
const OUTPUT_PATH = path.resolve(
    __dirname,
    "../src/generated/models.ts"
);

type Capability = string;
type Status = string;

interface ModelEntry {
    name: string;
    display_name: string;
    capabilities: Capability[];
    max_tokens: number;
    input_cost_per_1k: number;
    output_cost_per_1k: number;
    status: Status;
}

interface ProviderEntry {
    name: string;
    base_url: string;
    models: ModelEntry[];
}

interface Catalog {
    providers: Record<string, ProviderEntry>;
}

interface ProviderConfig {
    name?: string;
    display_name?: string;
    base_url?: string;
}

interface CatalogModelConfig {
    name?: string;
    display_name?: string;
    capabilities?: Capability[];
    status?: Status;
    max_tokens?: number;
    input_cost_per_1k?: number;
    output_cost_per_1k?: number;
    cost?: {
        input_per_1k?: number;
        output_per_1k?: number;
    };
    limits?: {
        max_tokens?: number;
    };
}

interface CatalogMetadata {
    version?: string;
    generatedAt?: string;
}

function toTitleCase(str: string): string {
    return str
        .split(/[-_]/)
        .map((word) => word.charAt(0).toUpperCase() + word.slice(1).toLowerCase())
        .join("");
}

function quote(value: string): string {
    return JSON.stringify(value);
}

function readYamlFile<T>(filePath: string): T {
    return parse(fs.readFileSync(filePath, "utf-8")) as T;
}

function normalizeModel(model: CatalogModelConfig, modelPath: string): ModelEntry {
    if (!model.name) {
        throw new Error(`Model file missing name: ${modelPath}`);
    }

    return {
        name: model.name,
        display_name: model.display_name ?? model.name,
        capabilities: model.capabilities ?? [],
        max_tokens: model.limits?.max_tokens ?? model.max_tokens ?? 0,
        input_cost_per_1k: model.cost?.input_per_1k ?? model.input_cost_per_1k ?? 0,
        output_cost_per_1k: model.cost?.output_per_1k ?? model.output_cost_per_1k ?? 0,
        status: model.status ?? "stable",
    };
}

function loadCatalog(catalogDir: string): Catalog {
    const providersDir = path.join(catalogDir, "providers");
    if (!fs.existsSync(providersDir)) {
        throw new Error(`Catalog providers directory not found: ${providersDir}`);
    }

    const providers: Record<string, ProviderEntry> = {};
    const providerIds = fs
        .readdirSync(providersDir, { withFileTypes: true })
        .filter((entry) => entry.isDirectory())
        .map((entry) => entry.name)
        .sort();

    for (const providerId of providerIds) {
        const providerDir = path.join(providersDir, providerId);
        const providerPath = path.join(providerDir, "provider.yaml");
        if (!fs.existsSync(providerPath)) {
            continue;
        }

        const providerConfig = readYamlFile<ProviderConfig>(providerPath);
        const modelsDir = path.join(providerDir, "models");
        const models = fs.existsSync(modelsDir)
            ? fs
                  .readdirSync(modelsDir, { withFileTypes: true })
                  .filter((entry) => entry.isFile() && entry.name.endsWith(".yaml"))
                  .map((entry) => {
                      const modelPath = path.join(modelsDir, entry.name);
                      return normalizeModel(readYamlFile<CatalogModelConfig>(modelPath), modelPath);
                  })
            : [];

        providers[providerId] = {
            name: providerConfig.display_name ?? providerConfig.name ?? providerId,
            base_url: providerConfig.base_url ?? "",
            models,
        };
    }

    return { providers };
}

function loadCatalogMetadata(catalogDir: string): CatalogMetadata {
    const manifestPath = path.join(catalogDir, "manifest.yaml");
    if (!fs.existsSync(manifestPath)) {
        return {};
    }

    const manifest = readYamlFile<{ version?: string; generated_at?: string }>(manifestPath);
    return {
        version: manifest.version,
        generatedAt: manifest.generated_at,
    };
}

function generateStringUnionType(name: string, values: string[], fallback = "string"): string[] {
    const uniqueValues = [...new Set(values)].sort();
    if (uniqueValues.length === 0) {
        return [`export type ${name} = ${fallback};`];
    }

    return [`export type ${name} =`, uniqueValues.map((value) => `  | ${quote(value)}`).join("\n") + ";"];
}

function generateModelsFile(catalog: Catalog, metadata: CatalogMetadata): string {
    const lines: string[] = [
        "// AUTO-GENERATED FILE - DO NOT EDIT DIRECTLY",
        "// Generated from model-catalog/providers",
        metadata.version ? `// Catalog version: ${metadata.version}` : "// Catalog version: unknown",
        `// Catalog generated: ${metadata.generatedAt ?? new Date().toISOString()}`,
        "",
        "/* eslint-disable */",
        "",
    ];

    const providerTypes: string[] = [];
    const allModelStrings: string[] = [];
    const modelMetadata: Array<{
        id: string;
        provider: string;
        model: string;
        displayName: string;
        capabilities: Capability[];
        maxTokens: number;
        status: string;
    }> = [];

    // Sort providers alphabetically for consistent output
    const sortedProviders = Object.entries(catalog.providers).sort(([a], [b]) =>
        a.localeCompare(b)
    );

    for (const [providerId, provider] of sortedProviders) {
        // Skip providers with no models
        if (!provider.models || provider.models.length === 0) {
            continue;
        }

        const typeName = `${toTitleCase(providerId)}Model`;
        const modelLiterals: string[] = [];

        // Filter out deprecated models and sort by name
        const activeModels = provider.models
            .filter((m) => m.status !== "deprecated")
            .sort((a, b) => a.name.localeCompare(b.name));

        for (const model of activeModels) {
            const fullModelId = `@${providerId}/${model.name}`;
            modelLiterals.push(`  | ${quote(fullModelId)}`);
            allModelStrings.push(fullModelId);

            modelMetadata.push({
                id: fullModelId,
                provider: providerId,
                model: model.name,
                displayName: model.display_name,
                capabilities: model.capabilities,
                maxTokens: model.max_tokens,
                status: model.status,
            });
        }

        if (modelLiterals.length > 0) {
            lines.push(`/**`);
            lines.push(` * ${provider.name} models`);
            lines.push(` */`);
            lines.push(`export type ${typeName} =`);
            lines.push(modelLiterals.join("\n") + ";");
            lines.push("");
            providerTypes.push(typeName);
        }
    }

    // Generate combined AllModels type
    lines.push("/**");
    lines.push(" * Union of all available models across all providers");
    lines.push(" */");
    if (providerTypes.length > 0) {
        lines.push(`export type AllModels =`);
        lines.push(providerTypes.map((t) => `  | ${t}`).join("\n") + ";");
    } else {
        lines.push("export type AllModels = string;");
    }
    lines.push("");

    const allCapabilities = modelMetadata.flatMap((model) => model.capabilities);
    lines.push("/**");
    lines.push(" * Model capability identifiers present in the catalog");
    lines.push(" */");
    lines.push(...generateStringUnionType("Capability", allCapabilities));
    lines.push("");

    const allStatuses = modelMetadata.map((model) => model.status);
    lines.push("/**");
    lines.push(" * Model lifecycle statuses present in the catalog");
    lines.push(" */");
    lines.push(...generateStringUnionType("Status", allStatuses));
    lines.push("");

    // Generate provider list
    lines.push("/**");
    lines.push(" * List of all provider identifiers");
    lines.push(" */");
    lines.push(`export const providers = [`);
    for (const [providerId, provider] of sortedProviders) {
        if (provider.models?.some((model) => model.status !== "deprecated")) {
            lines.push(`  ${quote(providerId)},`);
        }
    }
    lines.push(`] as const;`);
    lines.push("");
    lines.push("export type Provider = (typeof providers)[number];");
    lines.push("");

    // Generate const array of all models
    lines.push("/**");
    lines.push(" * Array of all available model identifiers");
    lines.push(" */");
    lines.push(`export const allModels: readonly AllModels[] = [`);
    for (const modelId of allModelStrings.sort()) {
        lines.push(`  ${quote(modelId)},`);
    }
    lines.push(`] as const;`);
    lines.push("");

    // Generate model metadata interface and array
    lines.push("/**");
    lines.push(" * Metadata for a model");
    lines.push(" */");
    lines.push("export interface ModelMetadata {");
    lines.push("  /** Full model identifier in @provider/model format */");
    lines.push("  id: AllModels;");
    lines.push("  /** Provider identifier */");
    lines.push("  provider: Provider;");
    lines.push("  /** Model name (without provider prefix) */");
    lines.push("  model: string;");
    lines.push("  /** Human-readable display name */");
    lines.push("  displayName: string;");
    lines.push("  /** Model capabilities */");
    lines.push("  capabilities: readonly Capability[];");
    lines.push("  /** Maximum context window in tokens */");
    lines.push("  maxTokens: number;");
    lines.push("  /** Model status */");
    lines.push("  status: Status;");
    lines.push("}");
    lines.push("");

    // Generate metadata array
    lines.push("/**");
    lines.push(" * Metadata for all available models");
    lines.push(" */");
    lines.push("export const modelMetadata: readonly ModelMetadata[] = [");
    for (const meta of modelMetadata.sort((a, b) => a.id.localeCompare(b.id))) {
        lines.push("  {");
        lines.push(`    id: ${quote(meta.id)} as AllModels,`);
        lines.push(`    provider: ${quote(meta.provider)} as Provider,`);
        lines.push(`    model: ${quote(meta.model)},`);
        lines.push(`    displayName: ${quote(meta.displayName)},`);
        lines.push(
            `    capabilities: [${meta.capabilities.map((c) => quote(c)).join(", ")}],`
        );
        lines.push(`    maxTokens: ${meta.maxTokens},`);
        lines.push(`    status: ${quote(meta.status)} as Status,`);
        lines.push("  },");
    }
    lines.push("] as const;");
    lines.push("");

    // Helper functions
    lines.push("/**");
    lines.push(" * Get metadata for a specific model");
    lines.push(" */");
    lines.push(
        "export function getModelMetadata(modelId: AllModels): ModelMetadata | undefined {"
    );
    lines.push("  return modelMetadata.find((m) => m.id === modelId);");
    lines.push("}");
    lines.push("");

    lines.push("/**");
    lines.push(" * Get all models for a specific provider");
    lines.push(" */");
    lines.push(
        "export function getModelsByProvider(provider: Provider): readonly ModelMetadata[] {"
    );
    lines.push("  return modelMetadata.filter((m) => m.provider === provider);");
    lines.push("}");
    lines.push("");

    lines.push("/**");
    lines.push(" * Check if a string is a valid model identifier");
    lines.push(" */");
    lines.push("export function isValidModel(modelId: string): modelId is AllModels {");
    lines.push("  return allModels.includes(modelId as AllModels);");
    lines.push("}");
    lines.push("");

    lines.push("/**");
    lines.push(" * Parse a model identifier into provider and model name");
    lines.push(" */");
    lines.push(
        "export function parseModelId(modelId: AllModels): { provider: Provider; model: string } {"
    );
    lines.push('  const match = modelId.match(/^@([^/]+)\\/(.+)$/);');
    lines.push('  if (!match || !match[1] || !match[2]) throw new Error(`Invalid model ID format: ${modelId}`);');
    lines.push("  return { provider: match[1] as Provider, model: match[2] };");
    lines.push("}");
    lines.push("");

    return lines.join("\n");
}

async function main() {
    console.log("Reading catalog from:", CATALOG_DIR);
    const catalog = loadCatalog(CATALOG_DIR);
    const metadata = loadCatalogMetadata(CATALOG_DIR);

    console.log(
        "Found providers:",
        Object.keys(catalog.providers).join(", ")
    );

    const output = generateModelsFile(catalog, metadata);

    // Ensure output directory exists
    const outputDir = path.dirname(OUTPUT_PATH);
    if (!fs.existsSync(outputDir)) {
        fs.mkdirSync(outputDir, { recursive: true });
    }

    fs.writeFileSync(OUTPUT_PATH, output);
    console.log("Generated:", OUTPUT_PATH);

    // Count models
    let totalModels = 0;
    for (const provider of Object.values(catalog.providers)) {
        totalModels += provider.models?.filter((m) => m.status !== "deprecated").length ?? 0;
    }
    console.log(`Total models: ${totalModels}`);
}

main().catch((err) => {
    console.error("Error:", err);
    process.exit(1);
});
