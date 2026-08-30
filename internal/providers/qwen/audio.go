package qwen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// defaultDashScopeBaseURL is the base URL for DashScope-specific APIs (non-OpenAI-compatible).
// The international endpoint is used by default; users can override via config.
const defaultDashScopeBaseURL = "https://dashscope-intl.aliyuncs.com/api/v1"

// detectAudioMime returns the MIME type for the audio data based on magic bytes.
// Qwen accepts wav, mp3, and m4a only.
func detectAudioMime(data []byte) string {
	if len(data) >= 4 && string(data[0:4]) == "RIFF" {
		return "audio/wav"
	}
	if len(data) >= 4 && string(data[0:4]) == "fLaC" {
		return "audio/flac"
	}
	// MP3: starts with ID3 tag or frame sync 0xFF 0xFB/0xF3/0xF2
	if len(data) >= 3 && string(data[0:3]) == "ID3" {
		return "audio/mpeg"
	}
	if len(data) >= 2 && data[0] == 0xFF && (data[1]&0xE0) == 0xE0 {
		return "audio/mpeg"
	}
	// M4A/MP4: ftyp box at offset 4
	if len(data) >= 8 && string(data[4:8]) == "ftyp" {
		return "audio/mp4"
	}
	// Default to wav since that's what our recorder produces
	return "audio/wav"
}

// dashscopeBaseURL returns the DashScope API base URL, deriving it from the
// configured OpenAI-compatible BaseURL when possible.
func (p *Provider) dashscopeBaseURL() string {
	// If the user configured a custom base URL, derive the DashScope API URL from it.
	// e.g. "https://dashscope-intl.aliyuncs.com/compatible-mode/v1" → "https://dashscope-intl.aliyuncs.com/api/v1"
	if p.cfg.BaseURL != "" {
		base := p.cfg.BaseURL
		// Strip the OpenAI-compatible path suffix.
		for _, suffix := range []string{"/compatible-mode/v1", "/compatible-mode", "/v1"} {
			if idx := len(base) - len(suffix); idx > 0 && base[idx:] == suffix {
				return base[:idx] + "/api/v1"
			}
		}
	}
	return defaultDashScopeBaseURL
}

const qwenTTSChunkLimit = 500 // stay under Qwen's 600-char limit with margin for sentence splits

// Speech implements text-to-speech using DashScope's multimodal-generation endpoint.
// If ReferenceAudio is set on the request, it performs voice cloning first.
// Long text is automatically chunked into segments under 500 characters.
func (p *Provider) Speech(ctx context.Context, req gw.SpeechRequest) (gw.SpeechResponse, error) {
	if p.cfg.APIKey == "" {
		return gw.SpeechResponse{}, fmt.Errorf("qwen api key not provided")
	}

	voice := req.Voice
	if voice == "" {
		voice = "Cherry"
	}

	// If voice cloning reference audio is provided, create a cloned voice first.
	if len(req.ReferenceAudio) > 0 {
		clonedVoiceID, err := p.createClonedVoice(ctx, req)
		if err != nil {
			return gw.SpeechResponse{}, fmt.Errorf("voice cloning failed: %w", err)
		}
		voice = clonedVoiceID
	} else if req.VoiceCloneProfileID != "" {
		voice = req.VoiceCloneProfileID
	}

	model := req.Model
	if model == "" {
		model = "qwen3-tts-flash"
	}

	runes := []rune(req.Input)

	// Build shared params for all chunks.
	sp := speechParams{
		model:        model,
		voice:        voice,
		speed:        req.Speed,
		instructions: req.Instructions,
		temperature:  req.Temperature,
		topP:         req.TopP,
	}

	// Short text — single request.
	if len(runes) <= qwenTTSChunkLimit {
		resp, err := p.speechSingle(ctx, req.Input, sp)
		if err != nil {
			return resp, err
		}
		resp.Audio = postProcessWAV(resp.Audio, req.Enhancement, req.SpeakerBoost)
		return resp, nil
	}

	// Long text — chunk and concatenate.
	chunks := chunkText(req.Input, qwenTTSChunkLimit)
	logger.WithFields("chunks", len(chunks), "total_chars", len(runes)).
		Info("qwen tts: chunking long input")

	var allAudio [][]byte
	var format, contentType string
	for i, chunk := range chunks {
		resp, err := p.speechSingle(ctx, chunk, sp)
		if err != nil {
			return gw.SpeechResponse{}, fmt.Errorf("qwen tts chunk %d/%d failed: %w", i+1, len(chunks), err)
		}
		if i == 0 {
			format = resp.Format
			contentType = resp.ContentType
		}
		allAudio = append(allAudio, resp.Audio)
	}

	combined := concatenateWAV(allAudio)
	combined = postProcessWAV(combined, req.Enhancement, req.SpeakerBoost)

	return gw.SpeechResponse{
		Audio:           combined,
		Format:          format,
		ContentType:     contentType,
		InputCharacters: len(req.Input),
	}, nil
}

type speechParams struct {
	model        string
	voice        string
	speed        float64
	instructions string
	temperature  float64
	topP         float64
}

// speechSingle sends a single TTS request for text that fits within the API limit.
func (p *Provider) speechSingle(ctx context.Context, text string, sp speechParams) (gw.SpeechResponse, error) {
	input := map[string]interface{}{
		"text":  text,
		"voice": sp.voice,
	}

	// Qwen has no numeric speed parameter — speed is controlled via natural language
	// instructions on the instruct model only. Build effective instructions from
	// the user's speed setting + explicit instructions.
	if strings.Contains(sp.model, "instruct") {
		effectiveInstructions := sp.instructions
		if sp.speed != 0 && sp.speed != 1.0 {
			var speedHint string
			if sp.speed <= 0.6 {
				speedHint = "Speak very slowly."
			} else if sp.speed <= 0.8 {
				speedHint = "Speak slowly."
			} else if sp.speed >= 1.8 {
				speedHint = "Speak very quickly."
			} else if sp.speed >= 1.4 {
				speedHint = "Speak quickly."
			} else if sp.speed >= 1.2 {
				speedHint = "Speak at a slightly faster pace."
			}
			if speedHint != "" {
				if effectiveInstructions != "" {
					effectiveInstructions = speedHint + " " + effectiveInstructions
				} else {
					effectiveInstructions = speedHint
				}
			}
		}
		if effectiveInstructions != "" {
			input["instructions"] = effectiveInstructions
		}
	}

	payload := map[string]interface{}{
		"model": sp.model,
		"input": input,
	}

	params := map[string]interface{}{}
	if sp.temperature > 0 {
		params["temperature"] = sp.temperature
	}
	if sp.topP > 0 {
		params["top_p"] = sp.topP
	}
	if len(params) > 0 {
		payload["parameters"] = params
	}

	buf, _ := json.Marshal(payload)

	endpoint := p.dashscopeBaseURL() + "/services/aigc/multimodal-generation/generation"
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return gw.SpeechResponse{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	logger.WithFields(
		"provider", "qwen",
		"endpoint", endpoint,
		"model", sp.model,
		"voice", sp.voice,
		"text_len", len(text),
		"speed", sp.speed,
		"has_instructions", sp.instructions != "",
		"temperature", sp.temperature,
		"top_p", sp.topP,
	).Info("qwen tts request issued")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return gw.SpeechResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return gw.SpeechResponse{}, fmt.Errorf("qwen tts error (status %d): %s", resp.StatusCode, string(b))
	}

	var ttsResp struct {
		Output struct {
			Audio json.RawMessage `json:"audio"`
		} `json:"output"`
		Usage struct {
			Characters int `json:"characters"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ttsResp); err != nil {
		return gw.SpeechResponse{}, fmt.Errorf("qwen tts decode error: %w", err)
	}

	var audioData []byte

	var audioObj struct {
		URL  string `json:"url"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal(ttsResp.Output.Audio, &audioObj); err == nil && audioObj.URL != "" {
		audioResp, dlErr := p.client.Get(audioObj.URL)
		if dlErr != nil {
			return gw.SpeechResponse{}, fmt.Errorf("qwen tts audio download error: %w", dlErr)
		}
		defer audioResp.Body.Close()
		audioData, err = io.ReadAll(audioResp.Body)
		if err != nil {
			return gw.SpeechResponse{}, fmt.Errorf("qwen tts audio read error: %w", err)
		}
	} else if err == nil && audioObj.Data != "" {
		audioData, err = base64.StdEncoding.DecodeString(audioObj.Data)
		if err != nil {
			return gw.SpeechResponse{}, fmt.Errorf("qwen tts audio decode error: %w", err)
		}
	} else {
		var audioStr string
		if err := json.Unmarshal(ttsResp.Output.Audio, &audioStr); err != nil {
			return gw.SpeechResponse{}, fmt.Errorf("qwen tts: unexpected audio format: %s", string(ttsResp.Output.Audio))
		}
		audioData, err = base64.StdEncoding.DecodeString(audioStr)
		if err != nil {
			return gw.SpeechResponse{}, fmt.Errorf("qwen tts audio decode error: %w", err)
		}
	}

	format := "wav"
	ct := "audio/wav"
	if len(audioData) >= 3 && (string(audioData[0:3]) == "ID3" || (audioData[0] == 0xFF && (audioData[1]&0xE0) == 0xE0)) {
		format = "mp3"
		ct = "audio/mpeg"
	}

	return gw.SpeechResponse{
		Audio:           audioData,
		Format:          format,
		ContentType:     ct,
		InputCharacters: len(text),
	}, nil
}

// chunkText splits text into segments that fit within maxChars, preferring
// sentence boundaries (. ! ? newline), then clause boundaries (, ; :), then word boundaries.
func chunkText(text string, maxChars int) []string {
	runes := []rune(text)
	if len(runes) <= maxChars {
		return []string{text}
	}

	var chunks []string
	for len(runes) > 0 {
		if len(runes) <= maxChars {
			chunks = append(chunks, string(runes))
			break
		}

		// Find the best split point within maxChars.
		segment := runes[:maxChars]
		splitAt := -1

		// Prefer sentence boundaries.
		for i := len(segment) - 1; i >= len(segment)/2; i-- {
			if segment[i] == '.' || segment[i] == '!' || segment[i] == '?' || segment[i] == '\n' {
				splitAt = i + 1
				break
			}
		}
		// Fall back to clause boundaries.
		if splitAt < 0 {
			for i := len(segment) - 1; i >= len(segment)/2; i-- {
				if segment[i] == ',' || segment[i] == ';' || segment[i] == ':' {
					splitAt = i + 1
					break
				}
			}
		}
		// Fall back to word boundaries.
		if splitAt < 0 {
			for i := len(segment) - 1; i >= len(segment)/2; i-- {
				if segment[i] == ' ' {
					splitAt = i + 1
					break
				}
			}
		}
		// Hard cut if no good boundary found.
		if splitAt < 0 {
			splitAt = maxChars
		}

		chunk := strings.TrimSpace(string(runes[:splitAt]))
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		runes = runes[splitAt:]
	}

	return chunks
}

// concatenateWAV merges multiple WAV byte slices by stripping headers and
// writing a single combined WAV. If any input is not WAV (no RIFF header),
// it falls back to simple byte concatenation.
func concatenateWAV(parts [][]byte) []byte {
	if len(parts) == 1 {
		return parts[0]
	}

	// Collect raw PCM data from each WAV.
	var pcmParts [][]byte
	var sampleRate uint32
	var bitsPerSample, numChannels uint16

	for _, part := range parts {
		if len(part) < 44 || string(part[0:4]) != "RIFF" {
			// Not WAV — fall back to simple concat.
			var combined []byte
			for _, p := range parts {
				combined = append(combined, p...)
			}
			return combined
		}
		// Parse WAV header fields from first part.
		if sampleRate == 0 {
			numChannels = uint16(part[22]) | uint16(part[23])<<8
			sampleRate = uint32(part[24]) | uint32(part[25])<<8 | uint32(part[26])<<16 | uint32(part[27])<<24
			bitsPerSample = uint16(part[34]) | uint16(part[35])<<8
		}
		// Find "data" subchunk.
		dataOffset := 12
		for dataOffset < len(part)-8 {
			chunkID := string(part[dataOffset : dataOffset+4])
			chunkSize := int(part[dataOffset+4]) | int(part[dataOffset+5])<<8 | int(part[dataOffset+6])<<16 | int(part[dataOffset+7])<<24
			if chunkID == "data" {
				start := dataOffset + 8
				end := start + chunkSize
				if end > len(part) {
					end = len(part)
				}
				pcmParts = append(pcmParts, part[start:end])
				break
			}
			dataOffset += 8 + chunkSize
		}
	}

	// Calculate total PCM size.
	totalPCM := 0
	for _, p := range pcmParts {
		totalPCM += len(p)
	}

	// Write combined WAV.
	byteRate := sampleRate * uint32(numChannels) * uint32(bitsPerSample) / 8
	blockAlign := numChannels * bitsPerSample / 8

	out := make([]byte, 44+totalPCM)
	copy(out[0:4], "RIFF")
	writeLE32(out, 4, uint32(36+totalPCM))
	copy(out[8:12], "WAVE")
	copy(out[12:16], "fmt ")
	writeLE32(out, 16, 16) // chunk size
	writeLE16(out, 20, 1)  // PCM
	writeLE16(out, 22, numChannels)
	writeLE32(out, 24, sampleRate)
	writeLE32(out, 28, byteRate)
	writeLE16(out, 32, blockAlign)
	writeLE16(out, 34, bitsPerSample)
	copy(out[36:40], "data")
	writeLE32(out, 40, uint32(totalPCM))

	offset := 44
	for _, p := range pcmParts {
		copy(out[offset:], p)
		offset += len(p)
	}

	return out
}

func writeLE16(buf []byte, offset int, v uint16) {
	buf[offset] = byte(v)
	buf[offset+1] = byte(v >> 8)
}

func writeLE32(buf []byte, offset int, v uint32) {
	buf[offset] = byte(v)
	buf[offset+1] = byte(v >> 8)
	buf[offset+2] = byte(v >> 16)
	buf[offset+3] = byte(v >> 24)
}

// EnrollVoice implements the VoiceEnroller interface — calls DashScope's voice
// customization endpoint to create a cloned voice and returns the provider voice ID.
func (p *Provider) EnrollVoice(ctx context.Context, referenceAudio []byte, referenceText string, targetModel string, preferredName string) (string, error) {
	if p.cfg.APIKey == "" {
		return "", fmt.Errorf("qwen api key not provided")
	}

	audioBase64 := base64.StdEncoding.EncodeToString(referenceAudio)
	audioMime := detectAudioMime(referenceAudio)
	audioDataURI := "data:" + audioMime + ";base64," + audioBase64

	if targetModel == "" {
		targetModel = "qwen3-tts-vc-2026-01-22"
	}
	if preferredName == "" {
		preferredName = "cloned_voice"
	}
	// Qwen requires preferred_name to contain only letters, digits, underscores; max 16 chars.
	sanitized := make([]byte, 0, len(preferredName))
	for _, c := range []byte(preferredName) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			sanitized = append(sanitized, c)
		} else if c == ' ' || c == '-' {
			sanitized = append(sanitized, '_')
		}
	}
	if len(sanitized) == 0 {
		sanitized = []byte("cloned_voice")
	}
	if len(sanitized) > 16 {
		sanitized = sanitized[:16]
	}
	preferredName = string(sanitized)

	payload := map[string]interface{}{
		"model": "qwen-voice-enrollment",
		"input": map[string]interface{}{
			"action":         "create",
			"target_model":   targetModel,
			"preferred_name": preferredName,
			"audio": map[string]interface{}{
				"data": audioDataURI,
			},
		},
	}

	if referenceText != "" {
		payload["input"].(map[string]interface{})["text"] = referenceText
	}

	buf, _ := json.Marshal(payload)
	endpoint := p.dashscopeBaseURL() + "/services/audio/tts/customization"

	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	logger.WithFields(
		"provider", "qwen",
		"endpoint", endpoint,
	).Info("qwen voice enrollment request issued")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("qwen voice enrollment error (status %d): %s", resp.StatusCode, string(b))
	}

	var cloneResp struct {
		Output struct {
			Voice       string `json:"voice"`
			TargetModel string `json:"target_model"`
		} `json:"output"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cloneResp); err != nil {
		return "", fmt.Errorf("qwen voice enrollment decode error: %w", err)
	}

	if cloneResp.Output.Voice == "" {
		return "", fmt.Errorf("qwen voice enrollment returned empty voice")
	}

	logger.WithFields(
		"voice", cloneResp.Output.Voice,
		"target_model", cloneResp.Output.TargetModel,
	).Info("qwen voice enrollment created")

	return cloneResp.Output.Voice, nil
}

// createClonedVoice is a convenience wrapper used by Speech() when inline voice cloning is requested.
func (p *Provider) createClonedVoice(ctx context.Context, req gw.SpeechRequest) (string, error) {
	preferredName := "cloned_voice"
	if req.ReferenceText != "" {
		preferredName = "cloned_" + req.ReferenceText[:min(len(req.ReferenceText), 9)]
	}
	return p.EnrollVoice(ctx, req.ReferenceAudio, req.ReferenceText, "qwen3-tts-vc-2026-01-22", preferredName)
}

// Transcribe returns ErrNotSupported since Qwen ASR has no DashScope API yet.
func (p *Provider) Transcribe(_ context.Context, _ gw.TranscriptionRequest) (gw.TranscriptionResponse, error) {
	return gw.TranscriptionResponse{}, gw.ErrNotSupported{
		Operation: "transcribe",
		Provider:  "qwen",
	}
}

// Translate returns ErrNotSupported since Qwen has no translation API.
func (p *Provider) Translate(_ context.Context, _ gw.TranslationRequest) (gw.TranslationResponse, error) {
	return gw.TranslationResponse{}, gw.ErrNotSupported{
		Operation: "translate",
		Provider:  "qwen",
	}
}

func audioFormatToContentType(format string) string {
	switch format {
	case "mp3":
		return "audio/mpeg"
	case "opus":
		return "audio/opus"
	case "aac":
		return "audio/aac"
	case "flac":
		return "audio/flac"
	case "wav":
		return "audio/wav"
	case "pcm":
		return "audio/pcm"
	default:
		return "audio/mpeg"
	}
}
