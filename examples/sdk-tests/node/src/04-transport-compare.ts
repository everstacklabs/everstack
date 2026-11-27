import { banner, isMain, makeClient, resolveChatModel } from "./lib/client";
import type { AllModels, TransportProtocol } from "@everstack/node";

/**
 * Side-by-side demonstration of the 0.2.2 fix:
 *   - default (connect): should succeed against a Connect-JSON gateway
 *   - grpc (the 0.2.1 default): typically fails with "missing status" against
 *     gateways that expose Connect JSON through their edge proxy
 */
async function tryChat(label: string, transport?: TransportProtocol): Promise<boolean> {
  console.log(`\n── ${label} ──`);
  try {
    const client = makeClient(transport ? { transport } : undefined);
    const model = await resolveChatModel(client);
    const res = await client.chat.completions.create({
      model: model as AllModels,
      messages: [{ role: "user", content: "Reply with the single word: pong" }],
      max_tokens: 8,
    });
    console.log(`✅ ${label}: ok — "${res.choices[0]?.message?.content?.trim()}"`);
    return true;
  } catch (err) {
    console.log(`❌ ${label}: ${err instanceof Error ? err.message : err}`);
    return false;
  }
}

export async function run(): Promise<boolean> {
  banner("04 · transport comparison (connect vs grpc)");

  const connectOk = await tryChat("default transport (connect)");
  const grpcOk = await tryChat('transport: "grpc"', "grpc");

  console.log("\nSummary:");
  console.log(`  default/connect : ${connectOk ? "PASS" : "FAIL"}`);
  console.log(
    `  grpc            : ${grpcOk ? "works (gateway supports gRPC trailers)" : "fails as expected on a Connect-JSON gateway"}`,
  );

  // Only the default path is required to pass — that is what the SDK ships.
  return connectOk;
}

if (isMain(import.meta.url)) {
  run().then((ok) => process.exit(ok ? 0 : 1));
}
