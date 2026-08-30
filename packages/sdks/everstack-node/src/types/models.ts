/**
 * Model types
 */

// Re-export generated model types
export type {
    AllModels,
    AnthropicModel,
    CohereModel,
    DeepseekModel,
    GoogleModel,
    HuggingfaceModel,
    MinimaxModel,
    MistralModel,
    ModelMetadata,
    MoonshotModel,
    OpenaiModel,
    OpenrouterModel,
    QwenModel,
    Provider,
} from "../generated/models.js";

export {
    allModels,
    getModelMetadata,
    getModelsByProvider,
    isValidModel,
    modelMetadata,
    parseModelId,
    providers,
} from "../generated/models.js";

/**
 * Model information returned by the API
 */
export interface Model {
    /** Model ID in @provider/model format */
    id: string;
    /** Object type (always "model") */
    object: "model";
    /** Unix timestamp of when the model was created */
    created: number;
    /** Owner of the model */
    owned_by: string;
}

/**
 * Response from the models list endpoint
 */
export interface ModelsListResponse {
    /** Object type (always "list") */
    object: "list";
    /** List of available models */
    data: Model[];
}
