#!/usr/bin/env tsx
/**
 * Comprehensive SDK test — exercises all resource types.
 *
 * Usage:
 *   EVERSTACK_API_KEY=pk_... tsx examples/test-all.ts
 *   EVERSTACK_API_KEY=pk_... EVERSTACK_GATEWAY_URL=http://localhost:8089 tsx examples/test-all.ts
 */

import { Everstack } from "../src/index.js";
import type { AllModels } from "../src/generated/models.js";

const passed: string[] = [];
const failed: string[] = [];
const skipped: string[] = [];

async function run(name: string, fn: () => Promise<void>) {
    process.stdout.write(`  ${name} ... `);
    try {
        await fn();
        console.log("✅");
        passed.push(name);
    } catch (err: any) {
        console.log(`❌ ${err.message ?? err}`);
        failed.push(name);
    }
}

function skip(name: string, reason: string) {
    console.log(`  ${name} ... ⏭️  ${reason}`);
    skipped.push(name);
}

async function main() {
    const apiKey = process.env.EVERSTACK_API_KEY;
    const baseUrl = process.env.EVERSTACK_GATEWAY_URL ?? "http://localhost:8089";

    if (!apiKey) {
        console.error("EVERSTACK_API_KEY is required");
        process.exit(1);
    }

    console.log(`\n🧪 Everstack Node.js SDK — Full Test Suite`);
    console.log(`   Gateway: ${baseUrl}\n`);

    const client = new Everstack({ apiKey, baseUrl });

    // ── Models ──────────────────────────────────────────────
    let models: { id: string; owned_by: string }[] = [];
    await run("models.list", async () => {
        const res = await client.models.list();
        models = res.data;
        console.log(`(${models.length} models)`);
    });

    const chatModel = (
        models.find((m) => m.owned_by === "openai" && !m.id.includes("embedding")) ??
        models.find((m) => m.owned_by === "anthropic") ??
        models[0]
    ) as { id: string; owned_by: string } | undefined;

    const embeddingModel = models.find(
        (m) => m.id.includes("embedding") || m.id.includes("text-embedding")
    );

    if (!chatModel) {
        console.error("\n❌ No models available. Is the gateway running?");
        process.exit(1);
    }
    console.log(`\n   Using chat model: ${chatModel.id}`);
    if (embeddingModel) console.log(`   Using embedding model: ${embeddingModel.id}`);
    console.log();

    // ── Chat Completions ────────────────────────────────────
    await run("chat.completions.create (non-streaming)", async () => {
        const res = await client.chat.completions.create({
            model: chatModel.id as AllModels,
            messages: [{ role: "user", content: "What is 2+2? One word." }],
            max_tokens: 10,
        });
        const content = res.choices[0]?.message?.content;
        if (!content) throw new Error("No content in response");
        process.stdout.write(`→ "${content.trim()}" `);
    });

    await run("chat.completions.create (streaming)", async () => {
        const stream = await client.chat.completions.create({
            model: chatModel.id as AllModels,
            messages: [{ role: "user", content: "Say hello in 3 words" }],
            max_tokens: 20,
            stream: true,
        });

        let text = "";
        for await (const chunk of stream) {
            const c = chunk.choices[0]?.delta?.content;
            if (c) text += c;
        }
        if (!text) throw new Error("No content from stream");
        process.stdout.write(`→ "${text.trim()}" `);
    });

    // ── Embeddings ──────────────────────────────────────────
    if (embeddingModel) {
        await run("embeddings.create", async () => {
            const res = await client.embeddings.create({
                model: embeddingModel.id as AllModels,
                input: "Hello, world!",
            });
            process.stdout.write(`→ ${res.data[0]?.embedding.length} dims `);
        });
    } else {
        skip("embeddings.create", "no embedding model available");
    }

    // ── Responses API ───────────────────────────────────────
    await run("responses.create", async () => {
        const res = await client.responses.create({
            model: chatModel.id as AllModels,
            input: [{ role: "user", content: "What is 1+1?" }],
        });
        if (!res.id) throw new Error("No response ID");
        process.stdout.write(`→ id=${res.id} status=${res.status} `);
    });

    await run("responses.list", async () => {
        const res = await client.responses.list({ limit: 5 });
        process.stdout.write(`→ ${res.data.length} responses `);
    });

    // ── Agents ──────────────────────────────────────────────
    await run("agents.list", async () => {
        const res = await client.agents.list();
        const count = res.agents?.length ?? 0;
        process.stdout.write(`→ ${count} agents `);
    });

    let testAgentId: string | undefined;
    let testSessionId: string | undefined;

    await run("agents.create", async () => {
        const res = await client.agents.create({
            name: "sdk-test-agent",
            description: "Temporary agent created by SDK test suite",
            model: chatModel.id,
            systemPrompt: "You are a helpful test assistant. Keep answers very short.",
            maxTurns: 5,
        });
        testAgentId = res.agent?.id;
        if (!testAgentId) throw new Error("No agent ID returned");
        process.stdout.write(`→ id=${testAgentId} `);
    });

    if (testAgentId) {
        await run("agents.sessions.create", async () => {
            const res = await client.agents.sessions.create({
                agentId: testAgentId!,
            });
            testSessionId = res.session?.id;
            if (!testSessionId) throw new Error("No session ID returned");
            process.stdout.write(`→ id=${testSessionId} `);
        });
    } else {
        skip("agents.sessions.create", "no agent created");
    }

    if (testSessionId) {
        await run("agents.sessions.runTurn", async () => {
            const res = await client.agents.sessions.runTurn({
                sessionId: testSessionId!,
                userInput: "What is the capital of France? One word.",
            });
            const text = res.turn?.assistantMessage ?? "";
            process.stdout.write(`→ "${text.trim().slice(0, 60)}" `);
        });

        await run("agents.sessions.runTurnStream", async () => {
            const stream = client.agents.sessions.runTurnStream({
                sessionId: testSessionId!,
                userInput: "What is 10 * 10? One word.",
                enableStreaming: true,
            });
            let text = "";
            for await (const event of stream) {
                if (event.textDelta) text += event.textDelta;
            }
            process.stdout.write(`→ "${text.trim().slice(0, 60)}" `);
        });
    } else {
        skip("agents.sessions.runTurn", "no session created");
        skip("agents.sessions.runTurnStream", "no session created");
    }

    // Cleanup: delete the test agent
    if (testAgentId) {
        await run("agents.delete (cleanup)", async () => {
            await client.agents.delete({ id: testAgentId! });
            process.stdout.write(`→ deleted ${testAgentId} `);
        });
    }

    // ── Datasets ────────────────────────────────────────────
    await run("datasets.list", async () => {
        const res = await client.datasets.list();
        process.stdout.write(`→ listed `);
    });

    // ── Evaluations ─────────────────────────────────────────
    await run("evaluations.runs.list", async () => {
        const res = await client.evaluations.runs.list();
        process.stdout.write(`→ listed `);
    });

    // ── Observability ───────────────────────────────────────
    await run("observability.metrics.getDashboard", async () => {
        const res = await client.observability.metrics.getDashboard({});
        process.stdout.write(`→ ok `);
    });

    // ── Summary ─────────────────────────────────────────────
    console.log(`\n${"─".repeat(50)}`);
    console.log(`  ✅ Passed:  ${passed.length}`);
    if (skipped.length) console.log(`  ⏭️  Skipped: ${skipped.length}`);
    if (failed.length) {
        console.log(`  ❌ Failed:  ${failed.length}`);
        failed.forEach((f) => console.log(`     - ${f}`));
        process.exit(1);
    }
    console.log();
}

main().catch((err) => {
    console.error("Fatal:", err);
    process.exit(1);
});
