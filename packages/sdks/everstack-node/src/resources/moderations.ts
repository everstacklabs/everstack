/**
 * Moderations resource
 *
 * Provides content moderation / classification functionality.
 */

import type { Client } from "@connectrpc/connect";
import type { GatewayService } from "@everstack/proto/everstack/gateway/v1/gateway_service_pb.js";
import { create } from "@bufbuild/protobuf";
import {
  ModerationRequestSchema,
  type ModerationResponse as ProtoModerationResponse,
} from "@everstack/proto/everstack/gateway/v1/moderations_pb.js";

import { fromConnectError } from "../errors.js";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface ModerationParams {
  /** Text input to classify (shorthand for a single text input) */
  input?: string;
  /** Multiple typed inputs (text or image_url) */
  inputs?: Array<
    | { type: "text"; text: string }
    | { type: "image_url"; image_url: { url: string } }
  >;
  /** Model to use */
  model?: string;
}

export interface ModerationCategories {
  hate: boolean;
  "hate/threatening": boolean;
  harassment: boolean;
  "harassment/threatening": boolean;
  illicit: boolean;
  "illicit/violent": boolean;
  "self-harm": boolean;
  "self-harm/intent": boolean;
  "self-harm/instructions": boolean;
  sexual: boolean;
  "sexual/minors": boolean;
  violence: boolean;
  "violence/graphic": boolean;
}

export interface ModerationCategoryScores {
  hate: number;
  "hate/threatening": number;
  harassment: number;
  "harassment/threatening": number;
  illicit: number;
  "illicit/violent": number;
  "self-harm": number;
  "self-harm/intent": number;
  "self-harm/instructions": number;
  sexual: number;
  "sexual/minors": number;
  violence: number;
  "violence/graphic": number;
}

export interface ModerationResult {
  flagged: boolean;
  categories: ModerationCategories;
  category_scores: ModerationCategoryScores;
}

export interface ModerationResponse {
  id: string;
  model: string;
  results: ModerationResult[];
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function transformResponse(proto: ProtoModerationResponse): ModerationResponse {
  return {
    id: proto.id,
    model: proto.model,
    results: proto.results.map((r) => ({
      flagged: r.flagged,
      categories: {
        hate: r.categories?.hate ?? false,
        "hate/threatening": r.categories?.hateThreatening ?? false,
        harassment: r.categories?.harassment ?? false,
        "harassment/threatening": r.categories?.harassmentThreatening ?? false,
        illicit: r.categories?.illicit ?? false,
        "illicit/violent": r.categories?.illicitViolent ?? false,
        "self-harm": r.categories?.selfHarm ?? false,
        "self-harm/intent": r.categories?.selfHarmIntent ?? false,
        "self-harm/instructions": r.categories?.selfHarmInstructions ?? false,
        sexual: r.categories?.sexual ?? false,
        "sexual/minors": r.categories?.sexualMinors ?? false,
        violence: r.categories?.violence ?? false,
        "violence/graphic": r.categories?.violenceGraphic ?? false,
      },
      category_scores: {
        hate: r.categoryScores?.hate ?? 0,
        "hate/threatening": r.categoryScores?.hateThreatening ?? 0,
        harassment: r.categoryScores?.harassment ?? 0,
        "harassment/threatening": r.categoryScores?.harassmentThreatening ?? 0,
        illicit: r.categoryScores?.illicit ?? 0,
        "illicit/violent": r.categoryScores?.illicitViolent ?? 0,
        "self-harm": r.categoryScores?.selfHarm ?? 0,
        "self-harm/intent": r.categoryScores?.selfHarmIntent ?? 0,
        "self-harm/instructions": r.categoryScores?.selfHarmInstructions ?? 0,
        sexual: r.categoryScores?.sexual ?? 0,
        "sexual/minors": r.categoryScores?.sexualMinors ?? 0,
        violence: r.categoryScores?.violence ?? 0,
        "violence/graphic": r.categoryScores?.violenceGraphic ?? 0,
      },
    })),
  };
}

// ---------------------------------------------------------------------------
// Resource
// ---------------------------------------------------------------------------

/**
 * Moderations resource for content classification
 */
export class Moderations {
  /** @internal */
  constructor(private readonly _client: Client<typeof GatewayService>) {}

  /**
   * Classify content for policy violations
   *
   * @example
   * ```typescript
   * const result = await client.moderations.create({
   *   input: 'Some text to check',
   *   model: 'text-moderation-latest',
   * });
   * console.log(result.results[0].flagged);
   * ```
   */
  async create(params: ModerationParams): Promise<ModerationResponse> {
    try {
      const request = create(ModerationRequestSchema, {
        input: params.input ?? "",
        inputs: (params.inputs ?? []).map((i) => {
          if (i.type === "text") {
            return { type: "text", text: i.text, imageUrl: undefined };
          }
          return {
            type: "image_url",
            text: "",
            imageUrl: { url: i.image_url.url },
          };
        }),
        model: params.model ?? "",
      });

      const response = await this._client.moderation(request);
      return transformResponse(response);
    } catch (error) {
      throw fromConnectError(error);
    }
  }
}
