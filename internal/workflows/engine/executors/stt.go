package executors

import (
	"context"
	"encoding/base64"
	"fmt"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/workflows/engine"
)

// STTExecutor handles speech-to-text nodes.
type STTExecutor struct {
	Registry *gw.Registry
}

func (e *STTExecutor) NodeType() string { return "stt" }

func (e *STTExecutor) Execute(ctx context.Context, node *engine.GraphNode, ec *engine.ExecutionContext) engine.NodeResult {
	provider, providerName, ok := e.Registry.FindAudioProvider()
	if !ok {
		return engine.NodeResult{Error: fmt.Errorf("stt: no audio provider available")}
	}

	// Get audio from the previous node's output (expected as base64)
	var audioBytes []byte
	if ec.Ledger != nil {
		audioB64 := ec.Ledger.InterpolateTemplate("{{$prev.audio}}", ec)
		if audioB64 != "" {
			var err error
			audioBytes, err = base64.StdEncoding.DecodeString(audioB64)
			if err != nil {
				return engine.NodeResult{Error: fmt.Errorf("stt: failed to decode audio: %w", err)}
			}
		}
	}
	if len(audioBytes) == 0 {
		if v, ok := ec.Variables["tts_audio"]; ok {
			if s, ok := v.(string); ok {
				var err error
				audioBytes, err = base64.StdEncoding.DecodeString(s)
				if err != nil {
					return engine.NodeResult{Error: fmt.Errorf("stt: failed to decode audio variable: %w", err)}
				}
			}
		}
	}
	if len(audioBytes) == 0 {
		return engine.NodeResult{Error: fmt.Errorf("stt: no audio input available")}
	}

	model := node.GetConfigString("model")
	if model == "" {
		model = "whisper-1"
	}

	language := node.GetConfigString("language")
	responseFormat := node.GetConfigString("responseFormat")
	if responseFormat == "" {
		responseFormat = "json"
	}

	logger.WithFields("provider", providerName, "model", model).
		Debug("stt executor: transcribing audio")

	req := gw.TranscriptionRequest{
		File:           audioBytes,
		Model:          model,
		Language:       language,
		ResponseFormat: responseFormat,
		Filename:       "workflow_audio.webm",
	}

	resp, err := provider.Transcribe(ctx, req)
	if err != nil {
		return engine.NodeResult{Error: fmt.Errorf("stt: transcription failed: %w", err)}
	}

	ec.SetVariable("stt_text", resp.Text)
	ec.SetNodeData("model", model)
	ec.SetNodeData("text_length", fmt.Sprintf("%d", len(resp.Text)))

	output := map[string]interface{}{
		"text":     resp.Text,
		"language": resp.Language,
		"duration": resp.Duration,
	}

	return engine.NodeResult{NextHandle: "out", Output: output}
}
