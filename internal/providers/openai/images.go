package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// GenerateImage implements image generation from text prompts.
func (p *Provider) GenerateImage(ctx context.Context, req gw.ImageGenerationRequest) (gw.ImageGenerationResponse, error) {
	if p.cfg.APIKey == "" {
		return gw.ImageGenerationResponse{}, fmt.Errorf("openai api key not provided")
	}

	// Build request payload
	payload := map[string]interface{}{
		"prompt": req.Prompt,
	}
	if req.Model != "" {
		payload["model"] = req.Model
	}
	if req.N > 0 {
		payload["n"] = req.N
	}
	if req.Quality != "" {
		payload["quality"] = req.Quality
	}
	if req.ResponseFormat != "" {
		payload["response_format"] = req.ResponseFormat
	}
	if req.Size != "" {
		payload["size"] = req.Size
	}
	if req.Style != "" {
		payload["style"] = req.Style
	}
	if req.User != "" {
		payload["user"] = req.User
	}
	if req.Background != "" {
		payload["background"] = req.Background
	}
	if req.OutputFormat != "" {
		payload["output_format"] = req.OutputFormat
	}
	if req.Moderation != "" {
		payload["moderation"] = req.Moderation
	}

	buf, _ := json.Marshal(payload)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.BaseURL+"/images/generations", bytes.NewReader(buf))
	if err != nil {
		return gw.ImageGenerationResponse{}, err
	}
	p.setHeaders(httpReq.Header)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return gw.ImageGenerationResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return gw.ImageGenerationResponse{}, errors.New("openai image generation error: " + string(b))
	}

	var parsed struct {
		Created int64 `json:"created"`
		Data    []struct {
			B64JSON       string `json:"b64_json,omitempty"`
			URL           string `json:"url,omitempty"`
			RevisedPrompt string `json:"revised_prompt,omitempty"`
		} `json:"data"`
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
			InputTokensDetails struct {
				TextTokens  int `json:"text_tokens"`
				ImageTokens int `json:"image_tokens"`
			} `json:"input_tokens_details"`
			OutputTokensDetails struct {
				TextTokens  int `json:"text_tokens"`
				ImageTokens int `json:"image_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return gw.ImageGenerationResponse{}, err
	}

	result := gw.ImageGenerationResponse{
		Created: parsed.Created,
		Model:   req.Model,
	}

	for _, img := range parsed.Data {
		result.Data = append(result.Data, gw.ImageData{
			B64JSON:       img.B64JSON,
			URL:           img.URL,
			RevisedPrompt: img.RevisedPrompt,
		})
	}

	if parsed.Usage != nil {
		result.Usage = &gw.ImageUsage{
			InputTokens:  parsed.Usage.InputTokens,
			OutputTokens: parsed.Usage.OutputTokens,
			TotalTokens:  parsed.Usage.TotalTokens,
			InputTokensDetails: &gw.ImageTokenDetails{
				TextTokens:  parsed.Usage.InputTokensDetails.TextTokens,
				ImageTokens: parsed.Usage.InputTokensDetails.ImageTokens,
			},
			OutputTokensDetails: &gw.ImageTokenDetails{
				TextTokens:  parsed.Usage.OutputTokensDetails.TextTokens,
				ImageTokens: parsed.Usage.OutputTokensDetails.ImageTokens,
			},
		}
	}

	return result, nil
}

// EditImage implements image editing.
func (p *Provider) EditImage(ctx context.Context, req gw.ImageEditRequest) (gw.ImageEditResponse, error) {
	if p.cfg.APIKey == "" {
		return gw.ImageEditResponse{}, fmt.Errorf("openai api key not provided")
	}

	// Build multipart form
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add image
	imageWriter, err := writer.CreateFormFile("image", "image.png")
	if err != nil {
		return gw.ImageEditResponse{}, err
	}
	if _, err := imageWriter.Write(req.Image); err != nil {
		return gw.ImageEditResponse{}, err
	}

	// Add prompt
	if err := writer.WriteField("prompt", req.Prompt); err != nil {
		return gw.ImageEditResponse{}, err
	}

	// Add mask if provided
	if len(req.Mask) > 0 {
		maskWriter, err := writer.CreateFormFile("mask", "mask.png")
		if err != nil {
			return gw.ImageEditResponse{}, err
		}
		if _, err := maskWriter.Write(req.Mask); err != nil {
			return gw.ImageEditResponse{}, err
		}
	}

	// Add optional fields
	if req.Model != "" {
		if err := writer.WriteField("model", req.Model); err != nil {
			return gw.ImageEditResponse{}, err
		}
	}
	if req.N > 0 {
		if err := writer.WriteField("n", fmt.Sprintf("%d", req.N)); err != nil {
			return gw.ImageEditResponse{}, err
		}
	}
	if req.Size != "" {
		if err := writer.WriteField("size", req.Size); err != nil {
			return gw.ImageEditResponse{}, err
		}
	}
	if req.ResponseFormat != "" {
		if err := writer.WriteField("response_format", req.ResponseFormat); err != nil {
			return gw.ImageEditResponse{}, err
		}
	}
	if req.User != "" {
		if err := writer.WriteField("user", req.User); err != nil {
			return gw.ImageEditResponse{}, err
		}
	}

	writer.Close()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.BaseURL+"/images/edits", body)
	if err != nil {
		return gw.ImageEditResponse{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return gw.ImageEditResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return gw.ImageEditResponse{}, errors.New("openai image edit error: " + string(b))
	}

	var parsed struct {
		Created int64 `json:"created"`
		Data    []struct {
			B64JSON       string `json:"b64_json,omitempty"`
			URL           string `json:"url,omitempty"`
			RevisedPrompt string `json:"revised_prompt,omitempty"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return gw.ImageEditResponse{}, err
	}

	result := gw.ImageEditResponse{
		Created: parsed.Created,
		Model:   req.Model,
	}

	for _, img := range parsed.Data {
		result.Data = append(result.Data, gw.ImageData{
			B64JSON:       img.B64JSON,
			URL:           img.URL,
			RevisedPrompt: img.RevisedPrompt,
		})
	}

	return result, nil
}

// CreateImageVariation creates variations of an image.
func (p *Provider) CreateImageVariation(ctx context.Context, req gw.ImageVariationRequest) (gw.ImageVariationResponse, error) {
	if p.cfg.APIKey == "" {
		return gw.ImageVariationResponse{}, fmt.Errorf("openai api key not provided")
	}

	// Build multipart form
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add image
	imageWriter, err := writer.CreateFormFile("image", "image.png")
	if err != nil {
		return gw.ImageVariationResponse{}, err
	}
	if _, err := imageWriter.Write(req.Image); err != nil {
		return gw.ImageVariationResponse{}, err
	}

	// Add optional fields
	if req.Model != "" {
		if err := writer.WriteField("model", req.Model); err != nil {
			return gw.ImageVariationResponse{}, err
		}
	}
	if req.N > 0 {
		if err := writer.WriteField("n", fmt.Sprintf("%d", req.N)); err != nil {
			return gw.ImageVariationResponse{}, err
		}
	}
	if req.ResponseFormat != "" {
		if err := writer.WriteField("response_format", req.ResponseFormat); err != nil {
			return gw.ImageVariationResponse{}, err
		}
	}
	if req.Size != "" {
		if err := writer.WriteField("size", req.Size); err != nil {
			return gw.ImageVariationResponse{}, err
		}
	}
	if req.User != "" {
		if err := writer.WriteField("user", req.User); err != nil {
			return gw.ImageVariationResponse{}, err
		}
	}

	writer.Close()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.BaseURL+"/images/variations", body)
	if err != nil {
		return gw.ImageVariationResponse{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return gw.ImageVariationResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return gw.ImageVariationResponse{}, errors.New("openai image variation error: " + string(b))
	}

	var parsed struct {
		Created int64 `json:"created"`
		Data    []struct {
			B64JSON       string `json:"b64_json,omitempty"`
			URL           string `json:"url,omitempty"`
			RevisedPrompt string `json:"revised_prompt,omitempty"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return gw.ImageVariationResponse{}, err
	}

	// Use current time if created is 0
	created := parsed.Created
	if created == 0 {
		created = time.Now().Unix()
	}

	result := gw.ImageVariationResponse{
		Created: created,
		Model:   req.Model,
	}

	for _, img := range parsed.Data {
		result.Data = append(result.Data, gw.ImageData{
			B64JSON:       img.B64JSON,
			URL:           img.URL,
			RevisedPrompt: img.RevisedPrompt,
		})
	}

	return result, nil
}
