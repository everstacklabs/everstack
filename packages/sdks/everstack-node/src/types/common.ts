/**
 * Common types shared across resources
 */

/**
 * Message role in a conversation
 */
export type Role = "system" | "user" | "assistant" | "tool" | "function";

/**
 * Finish reason for a completion
 */
export type FinishReason = "stop" | "length" | "tool_calls" | "content_filter" | "function_call" | null;

/**
 * Response format type
 */
export type ResponseFormatType = "text" | "json_object" | "json_schema";

/**
 * Response format configuration
 */
export interface ResponseFormat {
    type: ResponseFormatType;
    json_schema?: {
        name: string;
        schema: Record<string, unknown>;
        strict?: boolean;
    };
}

/**
 * Token usage statistics
 */
export interface Usage {
    prompt_tokens: number;
    completion_tokens: number;
    total_tokens: number;
}

/**
 * Content part for multimodal messages
 */
export type ContentPart =
    | { type: "text"; text: string }
    | { type: "image_url"; image_url: { url: string; detail?: "auto" | "low" | "high" } };

/**
 * Message content (string or multimodal parts)
 */
export type MessageContent = string | ContentPart[];

/**
 * Tool call in an assistant message
 */
export interface ToolCall {
    id: string;
    type: "function";
    function: {
        name: string;
        arguments: string;
    };
}

/**
 * Function definition for tool use
 */
export interface FunctionDefinition {
    name: string;
    description?: string;
    parameters?: Record<string, unknown>;
}

/**
 * Tool definition
 */
export interface Tool {
    type: "function";
    function: FunctionDefinition;
}

/**
 * Tool choice options
 */
export type ToolChoice =
    | "auto"
    | "none"
    | "required"
    | { type: "function"; function: { name: string } };
