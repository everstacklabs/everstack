package everstack

import "context"

// DecisionsResource provides typed decisions from Jev, Kev or Laya.
type DecisionsResource struct{ t *Transport }
type DecisionQuestion struct {
	Type         string `json:"type"`
	Instructions any    `json:"instructions,omitempty"`
	Criteria     any    `json:"criteria,omitempty"`
}
type DecisionParams struct {
	Model     string                      `json:"model"`
	State     any                         `json:"state"`
	Questions map[string]DecisionQuestion `json:"questions"`
}
type DecisionAnswer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Legend        map[string]string  `json:"legend,omitempty"`
}
type DecisionResponse struct {
	Model   string                    `json:"model"`
	Answers map[string]DecisionAnswer `json:"answers"`
	Usage   struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
}

func (r *DecisionsResource) Create(ctx context.Context, params *DecisionParams) (*DecisionResponse, error) {
	var resp DecisionResponse
	if err := r.t.Request(ctx, "POST", "/v1/systemone", params, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
