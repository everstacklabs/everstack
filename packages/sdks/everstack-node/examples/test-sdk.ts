#!/usr/bin/env tsx
/**
 * Test script for the Everstack Node.js SDK
 *
 * Usage:
 *   EVERSTACK_API_KEY=your-key tsx examples/test-sdk.ts
 *
 * Or set environment variables:
 *   export EVERSTACK_API_KEY=your-key
 *   export EVERSTACK_GATEWAY_URL=http://localhost:8080 (optional)
 */

import { Everstack } from "../src/index.js";
import type { AllModels } from "../src/generated/models.js";

async function main() {
    const apiKey = process.env.EVERSTACK_API_KEY;
    const gatewayUrl = process.env.EVERSTACK_GATEWAY_URL ?? "http://localhost:8089";

    if (!apiKey) {
        console.error("Error: EVERSTACK_API_KEY environment variable is required");
        console.error("Usage: EVERSTACK_API_KEY=your-key tsx examples/test-sdk.ts");
        process.exit(1);
    }

    console.log("🚀 Testing Everstack SDK");
    console.log(`   Gateway URL: ${gatewayUrl}`);
    console.log("");

    // Initialize the client
    const client = new Everstack({
        apiKey,
        baseUrl: gatewayUrl,
    });

    // Test 1: List available models
    console.log("📋 Test 1: Listing available models...");
    let availableModels: { id: string; owned_by: string }[] = [];
    try {
        const models = await client.models.list();
        availableModels = models.data;
        console.log(`   Found ${models.data.length} models`);
        if (models.data.length > 0) {
            console.log("   All models:");
            models.data.forEach((model) => {
                console.log(`     - ${model.id} (${model.owned_by})`);
            });
        }
        console.log("   ✅ Models list succeeded\n");
    } catch (error) {
        console.error("   ❌ Models list failed:", error);
    }

    // Find a chat model to test with (prefer openai, then anthropic)
    const chatModel = availableModels.find(
        (m) => m.owned_by === "openai" && !m.id.includes("embedding")
    ) ?? availableModels.find(
        (m) => m.owned_by === "anthropic"
    ) ?? availableModels[0];

    if (!chatModel) {
        console.error("❌ No models available for testing. Exiting.");
        process.exit(1);
    }

    console.log(`   Using model for tests: ${chatModel.id}\n`);

    // Test 2: Non-streaming chat completion
    console.log("💬 Test 2: Non-streaming chat completion...");
    try {
        const response = await client.chat.completions.create({
            model: chatModel.id as AllModels,
            messages: [
                { role: "system", content: "You are a helpful assistant. Be concise." },
                { role: "user", content: "What is 2 + 2? Answer in one word." },
            ],
            max_tokens: 10,
        });

        console.log(`   Model: ${response.model}`);
        console.log(`   Response: ${response.choices[0]?.message?.content}`);
        console.log(`   Usage: ${response.usage?.prompt_tokens} prompt + ${response.usage?.completion_tokens} completion = ${response.usage?.total_tokens} total tokens`);
        console.log("   ✅ Non-streaming chat succeeded\n");
    } catch (error) {
        console.error("   ❌ Non-streaming chat failed:", error);
    }

    // Test 3: Streaming chat completion
    console.log("🌊 Test 3: Streaming chat completion...");
    try {
        const stream = await client.chat.completions.create({
            model: chatModel.id as AllModels,
            messages: [
                { role: "user", content: "radius of the earth?" },
            ],
            max_tokens: 50,
            stream: true,
        });

        process.stdout.write("   Response: ");
        for await (const chunk of stream) {
            console.log(chunk);
            const content = chunk.choices[0]?.delta?.content;
            if (content) {
                process.stdout.write(content);
            }
        }
        console.log("\n   ✅ Streaming chat succeeded\n");
    } catch (error) {
        console.error("   ❌ Streaming chat failed:", error);
    }

    // Test 4: Embeddings (only if an embedding model is available)
    const embeddingModel = availableModels.find(
        (m) => m.id.includes("embedding") || m.id.includes("text-embedding")
    );

    if (embeddingModel) {
        console.log("🔢 Test 4: Creating embeddings...");
        try {
            const embeddings = await client.embeddings.create({
                model: embeddingModel.id as AllModels,
                input: "Hello, world!",
            });

            console.log(`   Model: ${embeddings.model}`);
            console.log(`   Embeddings count: ${embeddings.data.length}`);
            console.log(`   Dimensions: ${embeddings.data[0]?.embedding.length}`);
            console.log(`   Usage: ${embeddings.usage.total_tokens} tokens`);
            console.log("   ✅ Embeddings succeeded\n");
        } catch (error) {
            console.error("   ❌ Embeddings failed:", error);
        }
    } else {
        console.log("🔢 Test 4: Skipped (no embedding models available)\n");
    }

    console.log("🎉 All tests completed!");
}

main().catch((error) => {
    console.error("Fatal error:", error);
    process.exit(1);
});
