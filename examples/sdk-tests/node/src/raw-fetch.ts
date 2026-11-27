import { banner, getConfig, isMain } from "./lib/client";

/**
 * Control probe: hit the gateway's Connect-RPC endpoint directly with raw
 * fetch, the same way the "fetch workaround" does. This proves what protocol
 * the server actually speaks, independent of the SDK.
 *
 * The SDK routes chat completions through:
 *   POST {baseUrl}/everstack.gateway.v1.GatewayService/ChatCompletion
 *   Content-Type: application/json
 *   Connect-Protocol-Version: 1
 *
 * A 200 means full success. A Connect-format JSON error (e.g. invalid_argument)
 * still proves the gateway speaks Connect JSON — which is exactly what the
 * default Node transport in 0.2.2 relies on.
 */
export async function run(): Promise<boolean> {
  banner("raw · Connect-JSON probe (no SDK)");

  const { apiKey, baseUrl, modelOverride } = getConfig();
  const url = `${baseUrl.replace(/\/+$/, "")}/everstack.gateway.v1.GatewayService/ChatCompletion`;
  const model = modelOverride ?? "@openai/gpt-4o-mini";

  const body = {
    model,
    messages: [{ role: "USER", content: "ping" }],
    samplingParams: { maxTokens: 8 },
  };

  console.log(`POST ${url}`);
  try {
    const resp = await fetch(url, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        "connect-protocol-version": "1",
        "x-evs-api-key": apiKey,
      },
      body: JSON.stringify(body),
    });

    const contentType = resp.headers.get("content-type") ?? "(none)";
    const text = await resp.text();
    console.log(`   status      : ${resp.status} ${resp.statusText}`);
    console.log(`   content-type : ${contentType}`);
    console.log(`   body         : ${text.slice(0, 400)}${text.length > 400 ? "…" : ""}`);

    const speaksConnectJson = contentType.includes("application/json");
    console.log(
      speaksConnectJson
        ? "✅ gateway responded with Connect JSON — SDK default transport is correct"
        : "⚠️  gateway did not respond with application/json — inspect the response above",
    );
    return speaksConnectJson;
  } catch (err) {
    console.error("❌ raw probe failed:", err instanceof Error ? err.message : err);
    return false;
  }
}

if (isMain(import.meta.url)) {
  run().then((ok) => process.exit(ok ? 0 : 1));
}
