/**
 * Authenticated identity resource.
 *
 * Resolves the user and organization attached to the current API key. This is
 * especially useful for user-account API keys, whose tenant is not encoded in
 * the key itself.
 */

import type { Client } from "@connectrpc/connect";
import { CLIService } from "@everstack/proto/everstack/cli/v1/cli_service_pb.js";

import { fromConnectError } from "../errors.js";

type IdentityClient = Client<typeof CLIService>;

export interface IdentityInfo {
  user_id: string;
  email: string;
  org_id: string;
  org_slug: string;
  plan_tier: string;
}

export class Identity {
  /** Raw generated Connect client for advanced usage. */
  readonly raw: IdentityClient;

  /** @internal */
  constructor(client: IdentityClient) {
    this.raw = client;
  }

  /** Return the user and organization resolved from the current credentials. */
  async whoami(): Promise<IdentityInfo> {
    try {
      const response = await this.raw.whoami({});
      return {
        user_id: response.userId,
        email: response.email,
        org_id: response.orgId,
        org_slug: response.orgSlug,
        plan_tier: response.planTier,
      };
    } catch (error) {
      throw fromConnectError(error);
    }
  }
}
