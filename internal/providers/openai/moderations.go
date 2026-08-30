package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// Moderate classifies content for policy violations.
func (p *Provider) Moderate(ctx context.Context, req gw.ModerationRequest) (gw.ModerationResponse, error) {
	if p.cfg.APIKey == "" {
		return gw.ModerationResponse{}, fmt.Errorf("openai api key not provided")
	}

	// Build request payload
	payload := make(map[string]interface{})

	// Use input string or inputs array
	if req.Input != "" {
		payload["input"] = req.Input
	} else if len(req.Inputs) > 0 {
		// Convert to OpenAI format
		inputs := make([]map[string]interface{}, len(req.Inputs))
		for i, inp := range req.Inputs {
			item := map[string]interface{}{
				"type": inp.Type,
			}
			if inp.Type == "text" {
				item["text"] = inp.Text
			} else if inp.Type == "image_url" && inp.ImageURL != nil {
				item["image_url"] = map[string]string{
					"url": inp.ImageURL.URL,
				}
			}
			inputs[i] = item
		}
		payload["input"] = inputs
	}

	if req.Model != "" {
		payload["model"] = req.Model
	}

	buf, _ := json.Marshal(payload)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.BaseURL+"/moderations", bytes.NewReader(buf))
	if err != nil {
		return gw.ModerationResponse{}, err
	}
	p.setHeaders(httpReq.Header)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return gw.ModerationResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return gw.ModerationResponse{}, errors.New("openai moderation error: " + string(b))
	}

	var parsed struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Results []struct {
			Flagged    bool `json:"flagged"`
			Categories struct {
				Hate                  bool `json:"hate"`
				HateThreatening       bool `json:"hate/threatening"`
				Harassment            bool `json:"harassment"`
				HarassmentThreatening bool `json:"harassment/threatening"`
				Illicit               bool `json:"illicit"`
				IllicitViolent        bool `json:"illicit/violent"`
				SelfHarm              bool `json:"self-harm"`
				SelfHarmIntent        bool `json:"self-harm/intent"`
				SelfHarmInstructions  bool `json:"self-harm/instructions"`
				Sexual                bool `json:"sexual"`
				SexualMinors          bool `json:"sexual/minors"`
				Violence              bool `json:"violence"`
				ViolenceGraphic       bool `json:"violence/graphic"`
			} `json:"categories"`
			CategoryScores struct {
				Hate                  float64 `json:"hate"`
				HateThreatening       float64 `json:"hate/threatening"`
				Harassment            float64 `json:"harassment"`
				HarassmentThreatening float64 `json:"harassment/threatening"`
				Illicit               float64 `json:"illicit"`
				IllicitViolent        float64 `json:"illicit/violent"`
				SelfHarm              float64 `json:"self-harm"`
				SelfHarmIntent        float64 `json:"self-harm/intent"`
				SelfHarmInstructions  float64 `json:"self-harm/instructions"`
				Sexual                float64 `json:"sexual"`
				SexualMinors          float64 `json:"sexual/minors"`
				Violence              float64 `json:"violence"`
				ViolenceGraphic       float64 `json:"violence/graphic"`
			} `json:"category_scores"`
		} `json:"results"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return gw.ModerationResponse{}, err
	}

	result := gw.ModerationResponse{
		ID:    parsed.ID,
		Model: parsed.Model,
	}

	for _, r := range parsed.Results {
		result.Results = append(result.Results, gw.ModerationResult{
			Flagged: r.Flagged,
			Categories: gw.ModerationCategories{
				Hate:                  r.Categories.Hate,
				HateThreatening:       r.Categories.HateThreatening,
				Harassment:            r.Categories.Harassment,
				HarassmentThreatening: r.Categories.HarassmentThreatening,
				Illicit:               r.Categories.Illicit,
				IllicitViolent:        r.Categories.IllicitViolent,
				SelfHarm:              r.Categories.SelfHarm,
				SelfHarmIntent:        r.Categories.SelfHarmIntent,
				SelfHarmInstructions:  r.Categories.SelfHarmInstructions,
				Sexual:                r.Categories.Sexual,
				SexualMinors:          r.Categories.SexualMinors,
				Violence:              r.Categories.Violence,
				ViolenceGraphic:       r.Categories.ViolenceGraphic,
			},
			CategoryScores: gw.ModerationCategoryScores{
				Hate:                  r.CategoryScores.Hate,
				HateThreatening:       r.CategoryScores.HateThreatening,
				Harassment:            r.CategoryScores.Harassment,
				HarassmentThreatening: r.CategoryScores.HarassmentThreatening,
				Illicit:               r.CategoryScores.Illicit,
				IllicitViolent:        r.CategoryScores.IllicitViolent,
				SelfHarm:              r.CategoryScores.SelfHarm,
				SelfHarmIntent:        r.CategoryScores.SelfHarmIntent,
				SelfHarmInstructions:  r.CategoryScores.SelfHarmInstructions,
				Sexual:                r.CategoryScores.Sexual,
				SexualMinors:          r.CategoryScores.SexualMinors,
				Violence:              r.CategoryScores.Violence,
				ViolenceGraphic:       r.CategoryScores.ViolenceGraphic,
			},
		})
	}

	return result, nil
}
