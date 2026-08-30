/**
 * Embeddings types
 */

/**
 * Input for embeddings (single string or array)
 */
export type EmbeddingInput = string | string[];

/**
 * Encoding format for embeddings
 */
export type EmbeddingEncodingFormat = "float" | "base64";

/**
 * Parameters for an embeddings request
 */
export interface EmbeddingsParams<TModel extends string = string> {
    /** ID of the model to use (e.g., "@openai/text-embedding-3-small") */
    model: TModel;

    /** Input text(s) to embed */
    input: EmbeddingInput;

    /** Encoding format for the embeddings */
    encoding_format?: EmbeddingEncodingFormat;

    /** The number of dimensions for the output embeddings */
    dimensions?: number;

    /** A unique identifier for the end-user */
    user?: string;

    /** Custom metadata for logging/tracking */
    metadata?: Record<string, unknown>;
}

/**
 * A single embedding in the response
 */
export interface EmbeddingData {
    /** Object type (always "embedding") */
    object: "embedding";

    /** The embedding vector */
    embedding: number[];

    /** The index of this embedding */
    index: number;
}

/**
 * An embeddings response
 */
export interface EmbeddingsResponse {
    /** Object type (always "list") */
    object: "list";

    /** The list of embeddings */
    data: EmbeddingData[];

    /** The model used for the embeddings */
    model: string;

    /** Token usage statistics */
    usage: {
        prompt_tokens: number;
        total_tokens: number;
    };
}
