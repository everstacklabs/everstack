package cohere

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// Rerank reorders documents by relevance to a query.
func (p *Provider) Rerank(ctx context.Context, req gw.RerankRequest) (gw.RerankResponse, error) {
	if p.cfg.APIKey == "" {
		return gw.RerankResponse{}, fmt.Errorf("cohere api key not provided")
	}

	// Build request payload
	payload := map[string]interface{}{
		"model": req.Model,
		"query": req.Query,
	}

	// Use documents or document objects
	if len(req.Documents) > 0 {
		payload["documents"] = req.Documents
	} else if len(req.DocumentObjects) > 0 {
		docs := make([]string, len(req.DocumentObjects))
		for i, doc := range req.DocumentObjects {
			docs[i] = doc.Text
		}
		payload["documents"] = docs
	}

	if req.TopN > 0 {
		payload["top_n"] = req.TopN
	}
	if req.ReturnDocuments {
		payload["return_documents"] = req.ReturnDocuments
	}
	if req.MaxTokensPerDoc > 0 {
		payload["max_tokens_per_doc"] = req.MaxTokensPerDoc
	}

	buf, _ := json.Marshal(payload)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.BaseURL+"/v2/rerank", bytes.NewReader(buf))
	if err != nil {
		return gw.RerankResponse{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return gw.RerankResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return gw.RerankResponse{}, fmt.Errorf("cohere rerank error: %s", string(b))
	}

	var parsed struct {
		ID      string `json:"id"`
		Results []struct {
			Index          int     `json:"index"`
			RelevanceScore float64 `json:"relevance_score"`
			Document       *struct {
				Text string `json:"text"`
			} `json:"document,omitempty"`
		} `json:"results"`
		Meta struct {
			APIVersion struct {
				Version    string `json:"version"`
				IsBillable bool   `json:"is_billable"`
			} `json:"api_version"`
			BilledUnits struct {
				SearchUnits  int `json:"search_units"`
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"billed_units"`
			Tokens struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"tokens"`
		} `json:"meta"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return gw.RerankResponse{}, err
	}

	result := gw.RerankResponse{
		ID:    parsed.ID,
		Model: req.Model,
	}

	for _, r := range parsed.Results {
		rerankResult := gw.RerankResult{
			Index:          r.Index,
			RelevanceScore: r.RelevanceScore,
		}
		if r.Document != nil {
			rerankResult.Document = r.Document.Text
		}
		result.Results = append(result.Results, rerankResult)
	}

	result.Meta = &gw.RerankMeta{
		Version:    parsed.Meta.APIVersion.Version,
		IsBillable: parsed.Meta.APIVersion.IsBillable,
		BilledUnits: &gw.RerankBilledUnits{
			SearchUnits:  parsed.Meta.BilledUnits.SearchUnits,
			InputTokens:  parsed.Meta.BilledUnits.InputTokens,
			OutputTokens: parsed.Meta.BilledUnits.OutputTokens,
		},
		Tokens: &gw.RerankTokens{
			InputTokens:  parsed.Meta.Tokens.InputTokens,
			OutputTokens: parsed.Meta.Tokens.OutputTokens,
		},
	}

	return result, nil
}
