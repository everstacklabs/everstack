import { strict as assert } from "node:assert";
import test from "node:test";

import { Identity } from "./identity.js";

test("identity.whoami maps the authenticated organization", async () => {
  const raw = {
    whoami: async () => ({
      userId: "user-1",
      email: "dev@example.com",
      orgId: "org-1",
      orgSlug: "example",
      planTier: "dev",
    }),
  } as unknown as ConstructorParameters<typeof Identity>[0];

  const identity = new Identity(raw);
  const result = await identity.whoami();

  assert.deepEqual(result, {
    user_id: "user-1",
    email: "dev@example.com",
    org_id: "org-1",
    org_slug: "example",
    plan_tier: "dev",
  });
});
