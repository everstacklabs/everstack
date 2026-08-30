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

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// Speech implements text-to-speech generation.
func (p *Provider) Speech(ctx context.Context, req gw.SpeechRequest) (gw.SpeechResponse, error) {
	if p.cfg.APIKey == "" {
		return gw.SpeechResponse{}, fmt.Errorf("openai api key not provided")
	}

	// Build request payload
	payload := map[string]interface{}{
		"model": req.Model,
		"input": req.Input,
		"voice": req.Voice,
	}
	if req.ResponseFormat != "" {
		payload["response_format"] = req.ResponseFormat
	}
	if req.Speed != 0 {
		payload["speed"] = req.Speed
	}

	buf, _ := json.Marshal(payload)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.BaseURL+"/audio/speech", bytes.NewReader(buf))
	if err != nil {
		return gw.SpeechResponse{}, err
	}
	p.setHeaders(httpReq.Header)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return gw.SpeechResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return gw.SpeechResponse{}, errors.New("openai speech error: " + string(b))
	}

	// Read audio data
	audioData, err := io.ReadAll(resp.Body)
	if err != nil {
		return gw.SpeechResponse{}, err
	}

	// Determine content type from response format
	contentType := "audio/mpeg" // Default for mp3
	format := req.ResponseFormat
	if format == "" {
		format = "mp3"
	}
	switch format {
	case "opus":
		contentType = "audio/opus"
	case "aac":
		contentType = "audio/aac"
	case "flac":
		contentType = "audio/flac"
	case "wav":
		contentType = "audio/wav"
	case "pcm":
		contentType = "audio/pcm"
	}

	return gw.SpeechResponse{
		Audio:           audioData,
		Format:          format,
		ContentType:     contentType,
		InputCharacters: len(req.Input),
	}, nil
}

// Transcribe implements speech-to-text transcription.
func (p *Provider) Transcribe(ctx context.Context, req gw.TranscriptionRequest) (gw.TranscriptionResponse, error) {
	if p.cfg.APIKey == "" {
		return gw.TranscriptionResponse{}, fmt.Errorf("openai api key not provided")
	}

	// Build multipart form
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add file
	filename := req.Filename
	if filename == "" {
		filename = "audio.mp3"
	}
	fileWriter, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return gw.TranscriptionResponse{}, err
	}
	if _, err := fileWriter.Write(req.File); err != nil {
		return gw.TranscriptionResponse{}, err
	}

	// Add model
	if err := writer.WriteField("model", req.Model); err != nil {
		return gw.TranscriptionResponse{}, err
	}

	// Add optional fields
	if req.Language != "" {
		if err := writer.WriteField("language", req.Language); err != nil {
			return gw.TranscriptionResponse{}, err
		}
	}
	if req.Prompt != "" {
		if err := writer.WriteField("prompt", req.Prompt); err != nil {
			return gw.TranscriptionResponse{}, err
		}
	}
	if req.ResponseFormat != "" {
		if err := writer.WriteField("response_format", req.ResponseFormat); err != nil {
			return gw.TranscriptionResponse{}, err
		}
	}
	if req.Temperature != 0 {
		if err := writer.WriteField("temperature", fmt.Sprintf("%f", req.Temperature)); err != nil {
			return gw.TranscriptionResponse{}, err
		}
	}
	for _, gran := range req.TimestampGranularities {
		if err := writer.WriteField("timestamp_granularities[]", gran); err != nil {
			return gw.TranscriptionResponse{}, err
		}
	}

	writer.Close()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.BaseURL+"/audio/transcriptions", body)
	if err != nil {
		return gw.TranscriptionResponse{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return gw.TranscriptionResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return gw.TranscriptionResponse{}, errors.New("openai transcription error: " + string(b))
	}

	// Parse response based on format
	if req.ResponseFormat == "text" {
		text, _ := io.ReadAll(resp.Body)
		return gw.TranscriptionResponse{Text: string(text)}, nil
	}

	var parsed struct {
		Text     string `json:"text"`
		Task     string `json:"task"`
		Language string `json:"language"`
		Duration float64 `json:"duration"`
		Words    []struct {
			Word  string  `json:"word"`
			Start float64 `json:"start"`
			End   float64 `json:"end"`
		} `json:"words,omitempty"`
		Segments []struct {
			ID               int     `json:"id"`
			Seek             int     `json:"seek"`
			Start            float64 `json:"start"`
			End              float64 `json:"end"`
			Text             string  `json:"text"`
			Tokens           []int   `json:"tokens"`
			Temperature      float64 `json:"temperature"`
			AvgLogprob       float64 `json:"avg_logprob"`
			CompressionRatio float64 `json:"compression_ratio"`
			NoSpeechProb     float64 `json:"no_speech_prob"`
		} `json:"segments,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return gw.TranscriptionResponse{}, err
	}

	result := gw.TranscriptionResponse{
		Text:     parsed.Text,
		Task:     parsed.Task,
		Language: parsed.Language,
		Duration: parsed.Duration,
	}

	// Convert words
	for _, w := range parsed.Words {
		result.Words = append(result.Words, gw.TranscriptionWord{
			Word:  w.Word,
			Start: w.Start,
			End:   w.End,
		})
	}

	// Convert segments
	for _, seg := range parsed.Segments {
		result.Segments = append(result.Segments, gw.TranscriptionSegment{
			ID:               seg.ID,
			Seek:             seg.Seek,
			Start:            seg.Start,
			End:              seg.End,
			Text:             seg.Text,
			Tokens:           seg.Tokens,
			Temperature:      seg.Temperature,
			AvgLogprob:       seg.AvgLogprob,
			CompressionRatio: seg.CompressionRatio,
			NoSpeechProb:     seg.NoSpeechProb,
		})
	}

	return result, nil
}

// Translate implements audio translation to English.
func (p *Provider) Translate(ctx context.Context, req gw.TranslationRequest) (gw.TranslationResponse, error) {
	if p.cfg.APIKey == "" {
		return gw.TranslationResponse{}, fmt.Errorf("openai api key not provided")
	}

	// Build multipart form
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add file
	filename := req.Filename
	if filename == "" {
		filename = "audio.mp3"
	}
	fileWriter, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return gw.TranslationResponse{}, err
	}
	if _, err := fileWriter.Write(req.File); err != nil {
		return gw.TranslationResponse{}, err
	}

	// Add model
	if err := writer.WriteField("model", req.Model); err != nil {
		return gw.TranslationResponse{}, err
	}

	// Add optional fields
	if req.Prompt != "" {
		if err := writer.WriteField("prompt", req.Prompt); err != nil {
			return gw.TranslationResponse{}, err
		}
	}
	if req.ResponseFormat != "" {
		if err := writer.WriteField("response_format", req.ResponseFormat); err != nil {
			return gw.TranslationResponse{}, err
		}
	}
	if req.Temperature != 0 {
		if err := writer.WriteField("temperature", fmt.Sprintf("%f", req.Temperature)); err != nil {
			return gw.TranslationResponse{}, err
		}
	}

	writer.Close()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.BaseURL+"/audio/translations", body)
	if err != nil {
		return gw.TranslationResponse{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return gw.TranslationResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return gw.TranslationResponse{}, errors.New("openai translation error: " + string(b))
	}

	// Parse response
	if req.ResponseFormat == "text" {
		text, _ := io.ReadAll(resp.Body)
		return gw.TranslationResponse{Text: string(text)}, nil
	}

	var parsed struct {
		Text     string  `json:"text"`
		Task     string  `json:"task"`
		Language string  `json:"language"`
		Duration float64 `json:"duration"`
		Segments []struct {
			ID               int     `json:"id"`
			Seek             int     `json:"seek"`
			Start            float64 `json:"start"`
			End              float64 `json:"end"`
			Text             string  `json:"text"`
			Tokens           []int   `json:"tokens"`
			Temperature      float64 `json:"temperature"`
			AvgLogprob       float64 `json:"avg_logprob"`
			CompressionRatio float64 `json:"compression_ratio"`
			NoSpeechProb     float64 `json:"no_speech_prob"`
		} `json:"segments,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return gw.TranslationResponse{}, err
	}

	result := gw.TranslationResponse{
		Text:     parsed.Text,
		Task:     parsed.Task,
		Language: parsed.Language,
		Duration: parsed.Duration,
	}

	// Convert segments
	for _, seg := range parsed.Segments {
		result.Segments = append(result.Segments, gw.TranscriptionSegment{
			ID:               seg.ID,
			Seek:             seg.Seek,
			Start:            seg.Start,
			End:              seg.End,
			Text:             seg.Text,
			Tokens:           seg.Tokens,
			Temperature:      seg.Temperature,
			AvgLogprob:       seg.AvgLogprob,
			CompressionRatio: seg.CompressionRatio,
			NoSpeechProb:     seg.NoSpeechProb,
		})
	}

	return result, nil
}
