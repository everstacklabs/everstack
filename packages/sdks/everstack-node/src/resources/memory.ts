/**
 * Memory resource — REST/fetch based (no proto types in @everstack/proto yet)
 */

import {
    APIError,
    AuthenticationError,
    NotFoundError,
    RateLimitError,
    ServiceUnavailableError,
    InternalServerError,
    PermissionDeniedError,
    ConnectionError,
} from "../errors.js";
import type {
    AddDocumentsParams,
    AddDocumentsResponse,
    CreateCollectionParams,
    DeleteCollectionResponse,
    DeleteDocumentResponse,
    ListCollectionsResponse,
    MemoryCollection,
    QueryParams,
    QueryResponse,
} from "../types/memory.js";

interface MemoryOptions {
    baseUrl: string;
    apiKey: string;
    tenantId?: string;
    headers?: Record<string, string>;
}

function errorFromStatus(status: number, body: unknown): APIError {
    const message =
        typeof body === "object" && body !== null
            ? String((body as Record<string, unknown>).error ?? (body as Record<string, unknown>).message ?? "Unknown error")
            : String(body);

    switch (status) {
        case 401:
            return new AuthenticationError(message);
        case 403:
            return new PermissionDeniedError(message);
        case 404:
            return new NotFoundError(message);
        case 429:
            return new RateLimitError(message);
        case 503:
            return new ServiceUnavailableError(message);
        default:
            if (status >= 500) return new InternalServerError(message);
            return new APIError(status, message);
    }
}

/**
 * Collections sub-resource for managing memory collections
 */
export class Collections {
    /** @internal */
    private readonly _fetch: (method: string, path: string, body?: unknown) => Promise<unknown>;

    /** @internal */
    constructor(fetchFn: (method: string, path: string, body?: unknown) => Promise<unknown>) {
        this._fetch = fetchFn;
    }

    /**
     * List all collections
     */
    async list(): Promise<ListCollectionsResponse> {
        return (await this._fetch("GET", "/collections")) as ListCollectionsResponse;
    }

    /**
     * Get a collection by name
     */
    async get(name: string): Promise<MemoryCollection> {
        const resp = (await this._fetch("GET", `/collections/${encodeURIComponent(name)}`)) as {
            collection: MemoryCollection;
        };
        return resp.collection;
    }

    /**
     * Create a new collection
     */
    async create(params: CreateCollectionParams): Promise<MemoryCollection> {
        const resp = (await this._fetch("POST", "/collections", params)) as {
            collection: MemoryCollection;
        };
        return resp.collection;
    }

    /**
     * Delete a collection by name
     */
    async delete(name: string): Promise<DeleteCollectionResponse> {
        return (await this._fetch(
            "DELETE",
            `/collections/${encodeURIComponent(name)}`
        )) as DeleteCollectionResponse;
    }

    /**
     * Add documents to a collection
     */
    async addDocuments(name: string, params: AddDocumentsParams): Promise<AddDocumentsResponse> {
        return (await this._fetch(
            "POST",
            `/collections/${encodeURIComponent(name)}/documents`,
            params
        )) as AddDocumentsResponse;
    }

    /**
     * Delete a document from a collection
     */
    async deleteDocument(collectionName: string, documentId: string): Promise<DeleteDocumentResponse> {
        return (await this._fetch(
            "DELETE",
            `/collections/${encodeURIComponent(collectionName)}/documents/${encodeURIComponent(documentId)}`
        )) as DeleteDocumentResponse;
    }

    /**
     * Query a collection for similar documents
     */
    async query(name: string, params: QueryParams): Promise<QueryResponse> {
        return (await this._fetch(
            "POST",
            `/collections/${encodeURIComponent(name)}/query`,
            params
        )) as QueryResponse;
    }
}

/**
 * Memory resource for managing vector memory stores
 */
export class Memory {
    readonly collections: Collections;

    /** @internal */
    private readonly _options: MemoryOptions;

    constructor(options: MemoryOptions) {
        this._options = options;
        this.collections = new Collections(this._fetch.bind(this));
    }

    /** @internal */
    private async _fetch(method: string, path: string, body?: unknown): Promise<unknown> {
        const url = `${this._options.baseUrl}/v1/memory${path}`;

        const headers: Record<string, string> = {
            "x-evs-api-key": this._options.apiKey,
        };
        if (this._options.tenantId) {
            headers["x-evs-tenant-id"] = this._options.tenantId;
        }
        if (body !== undefined) {
            headers["Content-Type"] = "application/json";
        }
        if (this._options.headers) {
            Object.assign(headers, this._options.headers);
        }

        let response: Response;
        try {
            response = await fetch(url, {
                method,
                headers,
                body: body !== undefined ? JSON.stringify(body) : undefined,
            });
        } catch (err) {
            throw new ConnectionError(
                err instanceof Error ? err.message : "Failed to connect to Everstack gateway"
            );
        }

        const contentType = response.headers.get("content-type") ?? "";
        const isJSON = contentType.includes("application/json");
        const parsed = isJSON ? await response.json() : await response.text();

        if (!response.ok) {
            throw errorFromStatus(response.status, parsed);
        }

        return parsed;
    }
}
