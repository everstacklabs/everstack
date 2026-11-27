import { banner, isMain, makeClient, resolveChatModel } from "./lib/client";
import type { AllModels } from "@everstack/node";

/**
 * Streaming chat completion — exercises Connect server-streaming over HTTP/1.1,
 * the other path that depends on the transport fix.
 */
export async function run(): Promise<boolean> {
  banner("03 · chat completion (streaming)");
  try {
    const client = makeClient();
    const model = await resolveChatModel(client);
    console.log(`Using model: ${model}`);

    const stream = await client.chat.completions.create({
      model: model as AllModels,
      messages: [{ role: "user", content: "Name three primary colors, comma-separated." }],
      max_tokens: 64,
      stream: true,
    });

    let acc = "";
    let chunks = 0;
    process.stdout.write("   ");
    for await (const chunk of stream) {
      const delta = chunk.choices[0]?.delta?.content;
      if (delta) {
        acc += delta;
        chunks += 1;
        process.stdout.write(delta);
      }
    }
    console.log(`\n✅ streamed ${chunks} chunk(s), ${acc.length} chars`);
    return acc.length > 0;
  } catch (err) {
    console.error("\n❌ streaming failed:", err instanceof Error ? err.message : err);
    return false;
  }
}

if (isMain(import.meta.url)) {
  run().then((ok) => process.exit(ok ? 0 : 1));
}
