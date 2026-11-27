import { sdkVersion } from "./lib/client";
import { run as connectivity } from "./01-connectivity";
import { run as chat } from "./02-chat";
import { run as streaming } from "./03-streaming";
import { run as transportCompare } from "./04-transport-compare";

const tests: Array<[string, () => Promise<boolean>]> = [
  ["connectivity", connectivity],
  ["chat", chat],
  ["streaming", streaming],
  ["transport-compare", transportCompare],
];

async function main(): Promise<void> {
  console.log(`Running @everstack/node v${sdkVersion()} smoke tests`);
  const results: Array<[string, boolean]> = [];
  for (const [name, fn] of tests) {
    let ok = false;
    try {
      ok = await fn();
    } catch (err) {
      console.error(`   (${name} threw)`, err instanceof Error ? err.message : err);
    }
    results.push([name, ok]);
  }

  console.log("\n========== RESULTS ==========");
  for (const [name, ok] of results) {
    console.log(`  ${ok ? "✅ PASS" : "❌ FAIL"}  ${name}`);
  }
  const failed = results.filter(([, ok]) => !ok);
  console.log("=============================");
  console.log(`${results.length - failed.length}/${results.length} passed`);
  process.exit(failed.length === 0 ? 0 : 1);
}

main();
