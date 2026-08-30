export { createClientFor, toDate } from "./helpers.js";

// TODO: Move this to `./protobuf.ts` and export it from there
export { create, fromJson, toJson } from "@bufbuild/protobuf";
export type { JsonObject } from "@bufbuild/protobuf";
export type { GenService } from "@bufbuild/protobuf/codegenv1";
export { TimestampSchema, timestampDate, timestampFromDate, timestampFromMs, timestampMs } from "@bufbuild/protobuf/wkt";
export { ValueSchema, StructSchema } from "@bufbuild/protobuf/wkt";
export type { Duration, Timestamp, Value, Struct } from "@bufbuild/protobuf/wkt";
export { createConnectTransport } from "@connectrpc/connect-web";
export * as connectQuery from "@connectrpc/connect-query";
export type { Client } from "@connectrpc/connect";
// Re-export Code (value enum) and ConnectError (value class) so consumers can
// `instanceof ConnectError` and switch on `Code.X` without depending on
// @connectrpc/connect directly.
export { Code, ConnectError } from "@connectrpc/connect";