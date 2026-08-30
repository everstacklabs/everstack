/**
 * Error classes for the Everstack SDK
 *
 * Follows OpenAI SDK error patterns for familiarity
 */

/**
 * Base error class for all Everstack errors
 */
export class EverstackError extends Error {
    constructor(message: string) {
        super(message);
        this.name = "EverstackError";
        // Maintains proper stack trace for where our error was thrown
        if (Error.captureStackTrace) {
            Error.captureStackTrace(this, this.constructor);
        }
    }
}

/**
 * Error returned from the Everstack API
 */
export class APIError extends EverstackError {
    readonly status: number;
    readonly code?: string;
    readonly type?: string;
    readonly param?: string;

    constructor(
        status: number,
        message: string,
        options?: {
            code?: string;
            type?: string;
            param?: string;
        }
    ) {
        super(message);
        this.name = "APIError";
        this.status = status;
        this.code = options?.code;
        this.type = options?.type;
        this.param = options?.param;
    }

    static fromResponse(status: number, body: unknown): APIError {
        if (typeof body === "object" && body !== null) {
            const error = (body as Record<string, unknown>).error;
            if (typeof error === "object" && error !== null) {
                const err = error as Record<string, unknown>;
                return new APIError(status, String(err.message || "Unknown error"), {
                    code: err.code as string | undefined,
                    type: err.type as string | undefined,
                    param: err.param as string | undefined,
                });
            }
            return new APIError(status, String((body as Record<string, unknown>).message || "Unknown error"));
        }
        return new APIError(status, String(body) || "Unknown error");
    }
}

/**
 * Thrown when authentication fails (401)
 */
export class AuthenticationError extends APIError {
    constructor(message: string = "Invalid API key") {
        super(401, message, { code: "invalid_api_key" });
        this.name = "AuthenticationError";
    }
}

/**
 * Thrown when authorization fails (403)
 */
export class PermissionDeniedError extends APIError {
    constructor(message: string = "Permission denied") {
        super(403, message, { code: "permission_denied" });
        this.name = "PermissionDeniedError";
    }
}

/**
 * Thrown when a resource is not found (404)
 */
export class NotFoundError extends APIError {
    constructor(message: string = "Resource not found") {
        super(404, message, { code: "not_found" });
        this.name = "NotFoundError";
    }
}

/**
 * Thrown when rate limit is exceeded (429)
 */
export class RateLimitError extends APIError {
    readonly retryAfter?: number;

    constructor(message: string = "Rate limit exceeded", retryAfter?: number) {
        super(429, message, { code: "rate_limit_exceeded" });
        this.name = "RateLimitError";
        this.retryAfter = retryAfter;
    }
}

/**
 * Thrown when there's a server error (5xx)
 */
export class InternalServerError extends APIError {
    constructor(message: string = "Internal server error") {
        super(500, message, { code: "internal_error" });
        this.name = "InternalServerError";
    }
}

/**
 * Thrown when the service is unavailable (503)
 */
export class ServiceUnavailableError extends APIError {
    constructor(message: string = "Service temporarily unavailable") {
        super(503, message, { code: "service_unavailable" });
        this.name = "ServiceUnavailableError";
    }
}

/**
 * Thrown when a request times out
 */
export class TimeoutError extends EverstackError {
    constructor(message: string = "Request timed out") {
        super(message);
        this.name = "TimeoutError";
    }
}

/**
 * Thrown when there's a connection error
 */
export class ConnectionError extends EverstackError {
    constructor(message: string = "Failed to connect to Everstack gateway") {
        super(message);
        this.name = "ConnectionError";
    }
}

/**
 * Thrown when an invalid model is specified
 */
export class InvalidModelError extends EverstackError {
    readonly model: string;

    constructor(model: string) {
        super(`Invalid model: ${model}. Use client.models.list() to see available models.`);
        this.name = "InvalidModelError";
        this.model = model;
    }
}

/**
 * Convert a Connect-RPC error to a Everstack error
 */
export function fromConnectError(error: unknown): EverstackError {
    if (error instanceof EverstackError) {
        return error;
    }

    // Handle Connect-RPC errors
    if (typeof error === "object" && error !== null && "code" in error) {
        const connectError = error as { code: number; message: string; rawMessage?: string };
        const message = connectError.rawMessage || connectError.message;

        // Map Connect-RPC codes to HTTP status codes
        switch (connectError.code) {
            case 16: // Unauthenticated
                return new AuthenticationError(message);
            case 7: // PermissionDenied
                return new PermissionDeniedError(message);
            case 5: // NotFound
                return new NotFoundError(message);
            case 8: // ResourceExhausted (rate limit)
                return new RateLimitError(message);
            case 14: // Unavailable
                return new ServiceUnavailableError(message);
            case 4: // DeadlineExceeded
                return new TimeoutError(message);
            case 13: // Internal
                return new InternalServerError(message);
            default:
                return new APIError(500, message);
        }
    }

    // Handle generic errors
    if (error instanceof Error) {
        if (error.message.includes("fetch") || error.message.includes("network")) {
            return new ConnectionError(error.message);
        }
        return new EverstackError(error.message);
    }

    return new EverstackError(String(error));
}
