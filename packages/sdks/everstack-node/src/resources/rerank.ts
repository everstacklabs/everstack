/**
 * Rerank resource
 *
 * Provides document reranking for RAG pipelines.
 */

import type { Client } from "@connectrpc/connect";
import type { GatewayService } from "@everstack/proto/everstack/gateway/v1/gateway_service_pb.js";
import { create } from "@bufbuild/protobuf";
import {
  RerankRequestSchema,
  type RerankResponse as ProtoRerankResponse,
} from "@everstack/proto/everstack/gateway/v1/rerank_pb.js";

import { fromConnectError } from "../errors.js";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface RerankParams {
  /** Reranking model to use */
  model: string;
  /** The search query */
  query: string;
  /** Documents as plain strings (convenience — maps to document_objects internally) */
  documents?: string[];
  /** Documents as objects with text field */
  document_objects?: Array<{ text: string }>;
  /** Number of top results to return */
  top_n?: number;
  /** Whether to include the document text in results */
  return_documents?: boolean;
  /** Maximum tokens per document */
  max_tokens_per_doc?: number;
}

export interface RerankResult {
  /** Original index in the input documents array */
  index: number;
  /** Relevance score (higher is more relevant) */
  relevance_score: number;
  /** Document text (if return_documents was true) */
  document?: string;
}

export interface RerankMeta {
  version?: string;
  billed_units?: {
    search_units: number;
    input_tokens: number;
    output_tokens: number;
  };
  tokens?: {
    input_tokens: number;
    output_tokens: number;
  };
}

export interface RerankResponse {
  id: string;
  model: string;
  results: RerankResult[];
  meta?: RerankMeta;
}

// ---------------------------------------------------------------------------
// Resource
// ---------------------------------------------------------------------------

/**
 * Rerank resource for document relevance scoring
 */
export class Rerank {
  /** @internal */
  constructor(private readonly _client: Client<typeof GatewayService>) {}

  /**
   * Rerank documents by relevance to a query
   *
   * @example
   * ```typescript
   * const result = await client.rerank.create({
   *   model: 'rerank-v3.5',
   *   query: 'What is deep learning?',
   *   documents: [
   *     'Deep learning is a subset of machine learning...',
   *     'The weather today is sunny...',
   *     'Neural networks are inspired by the brain...',
   *   ],
   *   top_n: 2,
   *   return_documents: true,
   * });
   * console.log(result.results); // sorted by relevance_score desc
   * ```
   */
  async create(params: RerankParams): Promise<RerankResponse> {
    try {
      const request = create(RerankRequestSchema, {
        model: params.model,
        query: params.query,
        documents: params.documents ?? [],
        documentObjects: (params.document_objects ?? []).map((d) => ({
          text: d.text,
        })),
        topN: params.top_n ?? 0,
        returnDocuments: params.return_documents ?? false,
        maxTokensPerDoc: params.max_tokens_per_doc ?? 0,
      });

      const response = await this._client.rerank(request);
      return transformResponse(response);
    } catch (error) {
      throw fromConnectError(error);
    }
  }
}

function transformResponse(proto: ProtoRerankResponse): RerankResponse {
  return {
    id: proto.id,
    model: proto.model,
    results: proto.results.map((r) => ({
      index: r.index,
      relevance_score: r.relevanceScore,
      document: r.document || undefined,
    })),
    meta: proto.meta
      ? {
          version: proto.meta.version || undefined,
          billed_units: proto.meta.billedUnits
            ? {
                search_units: proto.meta.billedUnits.searchUnits,
                input_tokens: proto.meta.billedUnits.inputTokens,
                output_tokens: proto.meta.billedUnits.outputTokens,
              }
            : undefined,
          tokens: proto.meta.tokens
            ? {
                input_tokens: proto.meta.tokens.inputTokens,
                output_tokens: proto.meta.tokens.outputTokens,
              }
            : undefined,
        }
      : undefined,
  };
}
