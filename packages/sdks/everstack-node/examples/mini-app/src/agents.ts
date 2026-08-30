import { client } from "./client.js";

try {
  const result = await client.agents.list();
  const agents = result.agents ?? [];

  console.log(`Found ${agents.length} agents:\n`);
  for (const a of agents) {
    console.log(`  ${a.name ?? a.id}`);
    console.log(`    id: ${a.id}`);
    if (a.description) console.log(`    desc: ${a.description}`);
    console.log();
  }
} catch (err) {
  console.error("Failed to list agents:", err instanceof Error ? err.message : err);
  process.exit(1);
}
