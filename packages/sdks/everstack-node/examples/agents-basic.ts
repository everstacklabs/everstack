#!/usr/bin/env tsx

import { Everstack } from "../src/index.js";

async function main() {
  const apiKey = process.env.EVERSTACK_API_KEY;
  const baseUrl = process.env.EVERSTACK_GATEWAY_URL ?? "http://localhost:8089";

  if (!apiKey) {
    throw new Error("Set EVERSTACK_API_KEY before running this example.");
  }

  const client = new Everstack({ apiKey, baseUrl });

  const created = await client.agents.definitions.create({
    name: "support-agent",
    displayName: "Support Agent",
    systemPrompt: "You are a concise support assistant.",
  });

  const agentId = created.agent?.id;
  if (!agentId) {
    throw new Error("Agent ID missing in response.");
  }

  const session = await client.agents.sessions.create({ agentId });
  const sessionId = session.session?.id;
  if (!sessionId) {
    throw new Error("Session ID missing in response.");
  }

  const turn = await client.agents.sessions.runTurn({
    sessionId,
    input: "Summarize our current escalation policy in 3 bullets.",
  });

  console.log("Turn result:", turn.outputText ?? "<no text>");
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
