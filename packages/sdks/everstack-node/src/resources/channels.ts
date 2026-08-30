/**
 * Channels resource
 *
 * Provides access to the ChannelsService for managing channel bindings
 * (Slack, Discord, etc.) and testing connectivity.
 */

import type { Client } from "@connectrpc/connect";
import { ChannelsService } from "@everstack/proto/everstack/channels/v1/channels_service_pb.js";

import { fromConnectError } from "../errors.js";

type ChannelsClient = Client<typeof ChannelsService>;

type MethodInput<K extends keyof ChannelsClient> = Parameters<
  ChannelsClient[K]
>[0];
type MethodOptions<K extends keyof ChannelsClient> = Parameters<
  ChannelsClient[K]
>[1];
type MethodOutput<K extends keyof ChannelsClient> = Awaited<
  ReturnType<ChannelsClient[K]>
>;

export class Channels {
  readonly raw: ChannelsClient;

  /** @internal */
  constructor(client: ChannelsClient) {
    this.raw = client;
  }

  private async _call<K extends keyof ChannelsClient>(
    method: K,
    request: MethodInput<K>,
    options?: MethodOptions<K>,
  ): Promise<MethodOutput<K>> {
    try {
      const fn = this.raw[method] as (
        req: MethodInput<K>,
        opt?: MethodOptions<K>,
      ) => Promise<MethodOutput<K>>;
      return await fn(request, options);
    } catch (error) {
      throw fromConnectError(error);
    }
  }

  create(
    request: MethodInput<"createChannel">,
    options?: MethodOptions<"createChannel">,
  ): Promise<MethodOutput<"createChannel">> {
    return this._call("createChannel", request, options);
  }

  get(
    request: MethodInput<"getChannel">,
    options?: MethodOptions<"getChannel">,
  ): Promise<MethodOutput<"getChannel">> {
    return this._call("getChannel", request, options);
  }

  update(
    request: MethodInput<"updateChannel">,
    options?: MethodOptions<"updateChannel">,
  ): Promise<MethodOutput<"updateChannel">> {
    return this._call("updateChannel", request, options);
  }

  delete(
    request: MethodInput<"deleteChannel">,
    options?: MethodOptions<"deleteChannel">,
  ): Promise<MethodOutput<"deleteChannel">> {
    return this._call("deleteChannel", request, options);
  }

  list(
    request: MethodInput<"listChannels">,
    options?: MethodOptions<"listChannels">,
  ): Promise<MethodOutput<"listChannels">> {
    return this._call("listChannels", request, options);
  }

  test(
    request: MethodInput<"testChannel">,
    options?: MethodOptions<"testChannel">,
  ): Promise<MethodOutput<"testChannel">> {
    return this._call("testChannel", request, options);
  }

  listStatuses(
    request: MethodInput<"listChannelStatuses">,
    options?: MethodOptions<"listChannelStatuses">,
  ): Promise<MethodOutput<"listChannelStatuses">> {
    return this._call("listChannelStatuses", request, options);
  }

  listSessions(
    request: MethodInput<"listChannelSessions">,
    options?: MethodOptions<"listChannelSessions">,
  ): Promise<MethodOutput<"listChannelSessions">> {
    return this._call("listChannelSessions", request, options);
  }

  listPlatformChannels(
    request: MethodInput<"listPlatformChannels">,
    options?: MethodOptions<"listPlatformChannels">,
  ): Promise<MethodOutput<"listPlatformChannels">> {
    return this._call("listPlatformChannels", request, options);
  }
}
