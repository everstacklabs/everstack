package executors

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	storagesvc "github.com/everstacklabs/everstack/internal/api/grpc/storage/v1"
	"github.com/everstacklabs/everstack/internal/domain/voice_clone"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/workflows/engine"
)

// TTSExecutor handles text-to-speech nodes.
type TTSExecutor struct {
	Registry       *gw.Registry
	VoiceCloneRepo voice_clone.Repository
	StorageServer  *storagesvc.Server
}

func (e *TTSExecutor) NodeType() string { return "tts" }

func (e *TTSExecutor) Execute(ctx context.Context, node *engine.GraphNode, ec *engine.ExecutionContext) engine.NodeResult {
	// Route to the configured provider (default: qwen).
	providerName := node.GetConfigString("provider")
	if providerName == "" {
		providerName = "qwen"
	}
	provider, ok := e.Registry.GetAudioProvider(providerName)
	if !ok {
		// Fall back to scanning all providers.
		provider, providerName, ok = e.Registry.FindAudioProvider()
		if !ok {
			return engine.NodeResult{Error: fmt.Errorf("tts: no audio provider available")}
		}
	}

	// Get input text: explicit config > template interpolation > previous node output
	input := node.GetConfigString("inputText")
	if input != "" && ec.Ledger != nil {
		input = ec.Ledger.InterpolateTemplate(input, ec)
	}
	if input == "" && ec.Ledger != nil {
		input = ec.Ledger.InterpolateTemplate("{{$prev.content}}", ec)
	}
	if input == "" {
		if v, ok := ec.Variables["input"]; ok {
			if s, ok := v.(string); ok {
				input = s
			}
		}
	}
	if input == "" {
		return engine.NodeResult{Error: fmt.Errorf("tts: no input text available")}
	}

	model := node.GetConfigString("model")
	if model == "" {
		model = "qwen3-tts-flash"
	}

	voice := node.GetConfigString("voice")
	if voice == "" {
		voice = "Cherry"
	}

	responseFormat := node.GetConfigString("responseFormat")
	if responseFormat == "" {
		responseFormat = "mp3"
	}

	speed := node.GetConfigFloat("speed")
	if speed == 0 {
		speed = 1.0
	}

	voiceCloneProfileID := node.GetConfigString("voiceCloneProfileId")

	// Resolve voice clone profile ID → actual provider voice ID. Scoped
	// to the workflow's tenant so a workflow in tenant A cannot resolve
	// a profile owned by tenant B even if the node config carries a
	// foreign profile id.
	resolvedProfileID := voiceCloneProfileID
	if voiceCloneProfileID != "" && e.VoiceCloneRepo != nil {
		profileTenant := contextkeys.GetTenantID(ctx)
		profile, err := e.VoiceCloneRepo.GetByID(ctx, voiceCloneProfileID, profileTenant)
		if err != nil {
			logger.WithFields("error", err.Error(), "profile_id", voiceCloneProfileID).
				Warn("tts executor: failed to resolve voice clone profile")
		} else if profile != nil && profile.ProviderVoiceID != "" {
			resolvedProfileID = profile.ProviderVoiceID
			// Cloned voices require the voice clone model — auto-switch if needed.
			if !strings.Contains(model, "vc") {
				model = "qwen3-tts-vc-2026-01-22"
				logger.WithFields("profile_id", voiceCloneProfileID, "model", model).
					Debug("tts executor: auto-switched to voice clone model")
			}
		}
	}

	logger.WithFields("provider", providerName, "model", model, "voice", voice).
		Debug("tts executor: synthesizing speech")

	req := gw.SpeechRequest{
		Model:               model,
		Input:               input,
		Voice:               voice,
		ResponseFormat:      responseFormat,
		Speed:               speed,
		VoiceCloneProfileID: resolvedProfileID,
		Instructions:        node.GetConfigString("instructions"),
		Temperature:         node.GetConfigFloat("temperature"),
		TopP:                node.GetConfigFloat("topP"),
		Stability:           node.GetConfigFloat("stability"),
		Similarity:          node.GetConfigFloat("similarity"),
		Style:               node.GetConfigFloat("style"),
		Enhancement:         node.GetConfigBool("enhancement"),
		SpeakerBoost:        node.GetConfigFloat("speakerBoost"),
	}

	resp, err := provider.Speech(ctx, req)
	if err != nil {
		return engine.NodeResult{Error: fmt.Errorf("tts: speech generation failed: %w", err)}
	}

	// Store audio as base64 in the execution context
	audioBase64 := base64.StdEncoding.EncodeToString(resp.Audio)
	ec.SetVariable("tts_audio", audioBase64)
	ec.SetVariable("tts_format", resp.Format)
	ec.SetVariable("tts_content_type", resp.ContentType)

	ec.SetNodeData("model", model)
	ec.SetNodeData("voice", voice)
	ec.SetNodeData("format", resp.Format)
	ec.SetNodeData("input_length", fmt.Sprintf("%d", len(input)))

	output := map[string]interface{}{
		"audio":        audioBase64,
		"format":       resp.Format,
		"content_type": resp.ContentType,
		"duration":     resp.DurationSeconds,
	}

	// Persist audio to object storage if available.
	if e.StorageServer != nil {
		tenantID := contextkeys.GetTenantID(ctx)
		if tenantID != "" {
			filename := fmt.Sprintf("output.%s", resp.Format)
			objectID, uploadErr := e.StorageServer.UploadObject(
				ctx, tenantID, "voice_audio", filename, resp.ContentType,
				bytes.NewReader(resp.Audio), int64(len(resp.Audio)),
				"workflow_execution", ec.ExecutionID,
			)
			if uploadErr != nil {
				logger.WithError(uploadErr).Warn("tts executor: failed to persist audio to storage")
			} else {
				ec.SetNodeData("audio_object_id", objectID)
				output["audio_object_id"] = objectID
			}
		}
	}

	return engine.NodeResult{NextHandle: "out", Output: output}
}
