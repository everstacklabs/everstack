import { banner, isMain, makeClient, resolveChatModel } from "./lib/client";
import type { AllModels } from "@everstack/node";

/**
 * Non-streaming chat completion — the exact unary path that failed on 0.2.1
 * (gRPC transport → "protocol error: missing status").
 */
export async function run(): Promise<boolean> {
  banner("02 · chat completion (non-streaming)");
  try {
    const client = makeClient();
    const model = await resolveChatModel(client);
    console.log(`Using model: ${model}`);

    const res = await client.chat.completions.create({
      model: model as AllModels,
      messages: [
        { role: "system", content: "You are a helpful assistant. Be concise." },
        { role: "user", content: "What is 2 + 2? Answer in one word." },
      ],
      max_tokens: 16,
    });

    const text = res.choices[0]?.message?.content;
    console.log(`✅ response: ${text}`);
    console.log(
      `   usage: ${res.usage?.prompt_tokens ?? "?"} prompt + ${res.usage?.completion_tokens ?? "?"} completion = ${res.usage?.total_tokens ?? "?"} total`,
    );
    return Boolean(text);
  } catch (err) {
    console.error("❌ chat completion failed:", err instanceof Error ? err.message : err);
    return false;
  }
}

if (isMain(import.meta.url)) {
  run().then((ok) => process.exit(ok ? 0 : 1));
}
