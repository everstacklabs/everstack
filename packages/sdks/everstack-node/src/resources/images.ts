/**
 * Images resource
 *
 * Provides image generation, editing, and variation functionality.
 */

import type { Client } from "@connectrpc/connect";
import type { GatewayService } from "@everstack/proto/everstack/gateway/v1/gateway_service_pb.js";
import { create } from "@bufbuild/protobuf";
import {
  ImageGenerationRequestSchema,
  type ImageGenerationResponse as ProtoImageGenResponse,
  ImageEditRequestSchema,
  type ImageEditResponse as ProtoImageEditResponse,
  ImageVariationRequestSchema,
  type ImageVariationResponse as ProtoImageVariationResponse,
} from "@everstack/proto/everstack/gateway/v1/images_pb.js";

import { fromConnectError } from "../errors.js";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface ImageGenerateParams {
  /** Text prompt describing the desired image */
  prompt: string;
  /** Model to use */
  model?: string;
  /** Number of images to generate (1-10) */
  n?: number;
  /** Image quality (standard, hd) */
  quality?: string;
  /** Response format (url, b64_json) */
  response_format?: string;
  /** Image size (256x256, 512x512, 1024x1024, 1792x1024, 1024x1792) */
  size?: string;
  /** Image style (vivid, natural) */
  style?: string;
  /** User identifier for abuse tracking */
  user?: string;
  /** Background setting */
  background?: string;
  /** Output format */
  output_format?: string;
  /** Moderation level */
  moderation?: string;
}

export interface ImageEditParams {
  /** The image to edit (PNG, max 4MB) */
  image: Uint8Array;
  /** Text prompt describing the desired edits */
  prompt: string;
  /** Optional mask image for inpainting */
  mask?: Uint8Array;
  /** Model to use */
  model?: string;
  /** Number of images to generate */
  n?: number;
  /** Image size */
  size?: string;
  /** Response format */
  response_format?: string;
  /** User identifier */
  user?: string;
  /** Image quality */
  quality?: string;
}

export interface ImageVariationParams {
  /** The image to create variations of */
  image: Uint8Array;
  /** Model to use */
  model?: string;
  /** Number of variations to generate */
  n?: number;
  /** Response format */
  response_format?: string;
  /** Image size */
  size?: string;
  /** User identifier */
  user?: string;
}

export interface ImageData {
  /** Base64-encoded image (if response_format is b64_json) */
  b64_json?: string;
  /** URL of the generated image (if response_format is url) */
  url?: string;
  /** The revised prompt used for generation */
  revised_prompt?: string;
}

export interface ImageUsage {
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
}

export interface ImageResponse {
  created: number;
  data: ImageData[];
  model: string;
  usage?: ImageUsage;
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function transformImageData(
  proto: ProtoImageGenResponse | ProtoImageEditResponse | ProtoImageVariationResponse,
): ImageResponse {
  const usage = "usage" in proto && proto.usage
    ? {
        input_tokens: proto.usage.inputTokens,
        output_tokens: proto.usage.outputTokens,
        total_tokens: proto.usage.totalTokens,
      }
    : undefined;

  return {
    created: Number(proto.created),
    data: proto.data.map((d) => ({
      b64_json: d.b64Json || undefined,
      url: d.url || undefined,
      revised_prompt: d.revisedPrompt || undefined,
    })),
    model: proto.model,
    usage,
  };
}

// ---------------------------------------------------------------------------
// Resource
// ---------------------------------------------------------------------------

/**
 * Images resource for generation, editing, and variations
 */
export class Images {
  /** @internal */
  constructor(private readonly _client: Client<typeof GatewayService>) {}

  /**
   * Generate images from a text prompt
   *
   * @example
   * ```typescript
   * const response = await client.images.generate({
   *   prompt: 'A sunset over mountains',
   *   model: 'dall-e-3',
   *   size: '1024x1024',
   * });
   * console.log(response.data[0].url);
   * ```
   */
  async generate(params: ImageGenerateParams): Promise<ImageResponse> {
    try {
      const request = create(ImageGenerationRequestSchema, {
        prompt: params.prompt,
        model: params.model ?? "",
        n: params.n ?? 0,
        quality: params.quality ?? "",
        responseFormat: params.response_format ?? "",
        size: params.size ?? "",
        style: params.style ?? "",
        user: params.user ?? "",
        background: params.background ?? "",
        outputFormat: params.output_format ?? "",
        moderation: params.moderation ?? "",
      });

      const response = await this._client.imageGeneration(request);
      return transformImageData(response);
    } catch (error) {
      throw fromConnectError(error);
    }
  }

  /**
   * Edit an existing image with a text prompt
   *
   * @example
   * ```typescript
   * const response = await client.images.edit({
   *   image: imageBuffer,
   *   prompt: 'Add a rainbow in the sky',
   * });
   * ```
   */
  async edit(params: ImageEditParams): Promise<ImageResponse> {
    try {
      const request = create(ImageEditRequestSchema, {
        image: params.image,
        prompt: params.prompt,
        mask: params.mask ?? new Uint8Array(),
        model: params.model ?? "",
        n: params.n ?? 0,
        size: params.size ?? "",
        responseFormat: params.response_format ?? "",
        user: params.user ?? "",
        quality: params.quality ?? "",
      });

      const response = await this._client.imageEdit(request);
      return transformImageData(response);
    } catch (error) {
      throw fromConnectError(error);
    }
  }

  /**
   * Create variations of an existing image
   *
   * @example
   * ```typescript
   * const response = await client.images.createVariation({
   *   image: imageBuffer,
   *   n: 3,
   * });
   * ```
   */
  async createVariation(params: ImageVariationParams): Promise<ImageResponse> {
    try {
      const request = create(ImageVariationRequestSchema, {
        image: params.image,
        model: params.model ?? "",
        n: params.n ?? 0,
        responseFormat: params.response_format ?? "",
        size: params.size ?? "",
        user: params.user ?? "",
      });

      const response = await this._client.imageVariation(request);
      return transformImageData(response);
    } catch (error) {
      throw fromConnectError(error);
    }
  }
}
