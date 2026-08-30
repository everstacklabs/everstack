import { client } from "./client.js";

const result = await client.models.list();

console.log(`Found ${result.data.length} models:\n`);
const byProvider = new Map<string, string[]>();
for (const m of result.data) {
  const arr = byProvider.get(m.owned_by) ?? [];
  arr.push(m.id);
  byProvider.set(m.owned_by, arr);
}

for (const [provider, ids] of byProvider) {
  console.log(`  ${provider} (${ids.length})`);
  for (const id of ids.slice(0, 5)) console.log(`    - ${id}`);
  if (ids.length > 5) console.log(`    ... +${ids.length - 5} more`);
}
