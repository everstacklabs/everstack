import { banner, isMain, makeClient } from "./lib/client";

/** Unary call: lists models. Proves auth + the Connect-JSON unary path work. */
export async function run(): Promise<boolean> {
  banner("01 · connectivity (models.list)");
  try {
    const client = makeClient();
    const models = await client.models.list();
    console.log(`✅ models.list returned ${models.data.length} model(s)`);
    for (const m of models.data.slice(0, 20)) {
      console.log(`   - ${m.id} (${m.owned_by})`);
    }
    if (models.data.length > 20) {
      console.log(`   … and ${models.data.length - 20} more`);
    }
    return true;
  } catch (err) {
    console.error("❌ connectivity failed:", err instanceof Error ? err.message : err);
    return false;
  }
}

if (isMain(import.meta.url)) {
  run().then((ok) => process.exit(ok ? 0 : 1));
}
