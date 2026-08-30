package gateway

import "context"

// BatchChatRequest allows batching multiple chat requests for efficiency.
type BatchChatRequest struct {
	Requests []ChatCompletionRequest `json:"requests"`
}

// BatchChatResponse returns a one-to-one list of results.
type BatchChatResponse struct {
	Responses []ChatCompletionResponse `json:"responses"`
}

// HandleBatchChat processes requests sequentially for now. Can be optimized with concurrency.
func HandleBatchChat(ctx context.Context, router *Router, req BatchChatRequest) (BatchChatResponse, error) {
	results := make([]ChatCompletionResponse, 0, len(req.Requests))
	for _, r := range req.Requests {
		resp, err := HandleChat(ctx, router, r)
		if err != nil {
			return BatchChatResponse{}, err
		}
		results = append(results, resp)
	}
	return BatchChatResponse{Responses: results}, nil
}
