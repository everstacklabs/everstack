package executors

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"

	storagesvc "github.com/everstacklabs/everstack/internal/api/grpc/storage/v1"
	"github.com/everstacklabs/everstack/internal/domain/voice_clone"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/workflows/engine"
)

// VoiceCloneExecutor handles voice cloning nodes — clones a voice from reference
// audio and synthesizes speech with the cloned voice.
type VoiceCloneExecutor struct {
	Registry       *gw.Registry
	VoiceCloneRepo voice_clone.Repository
	StorageServer  *storagesvc.Server
}

func (e *VoiceCloneExecutor) NodeType() string { return "voiceClone" }

func (e *VoiceCloneExecutor) Execute(ctx context.Context, node *engine.GraphNode, ec *engine.ExecutionContext) engine.NodeResult {
	// Route to the configured provider (default: qwen for voice cloning).
	providerName := node.GetConfigString("provider")
	if providerName == "" {
		providerName = "qwen"
	}
	provider, ok := e.Registry.GetAudioProvider(providerName)
	if !ok {
		// Fall back to scanning all providers.
		provider, providerName, ok = e.Registry.FindAudioProvider()
		if !ok {
			return engine.NodeResult{Error: fmt.Errorf("voiceClone: no audio provider available")}
		}
	}

	// Get text to synthesize: explicit config > template interpolation > previous node output
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
		return engine.NodeResult{Error: fmt.Errorf("voiceClone: no input text available")}
	}

	model := node.GetConfigString("model")
	if model == "" {
		// Voice cloning synthesis must use the same model as the enrollment target_model.
		model = "qwen3-tts-vc-2026-01-22"
	}

	voiceCloneProfileID := node.GetConfigString("voiceCloneProfileId")

	// Resolve voice clone profile ID → actual provider voice ID,
	// scoped to the workflow's tenant. See tts.go for rationale.
	providerVoiceID := voiceCloneProfileID
	if voiceCloneProfileID != "" && e.VoiceCloneRepo != nil {
		profileTenant := contextkeys.GetTenantID(ctx)
		profile, err := e.VoiceCloneRepo.GetByID(ctx, voiceCloneProfileID, profileTenant)
		if err != nil {
			logger.WithFields("error", err.Error(), "profile_id", voiceCloneProfileID).
				Warn("voiceClone executor: failed to resolve voice clone profile")
		} else if profile != nil && profile.ProviderVoiceID != "" {
			providerVoiceID = profile.ProviderVoiceID
			logger.WithFields("profile_id", voiceCloneProfileID, "provider_voice_id", providerVoiceID).
				Debug("voiceClone executor: resolved voice clone profile")
		}
	}

	logger.WithFields("provider", providerName, "model", model, "profile", voiceCloneProfileID).
		Debug("voiceClone executor: synthesizing with cloned voice")

	speed := node.GetConfigFloat("speed")
	if speed == 0 {
		speed = 1.0
	}

	req := gw.SpeechRequest{
		Model:               model,
		Input:               input,
		Voice:               "",
		Speed:               speed,
		VoiceCloneProfileID: providerVoiceID,
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
		return engine.NodeResult{Error: fmt.Errorf("voiceClone: speech generation failed: %w", err)}
	}

	audioBase64 := base64.StdEncoding.EncodeToString(resp.Audio)
	ec.SetVariable("voice_clone_audio", audioBase64)
	ec.SetVariable("voice_clone_format", resp.Format)

	ec.SetNodeData("model", model)
	ec.SetNodeData("format", resp.Format)
	ec.SetNodeData("profile_id", voiceCloneProfileID)

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
				logger.WithError(uploadErr).Warn("voiceClone executor: failed to persist audio to storage")
			} else {
				ec.SetNodeData("audio_object_id", objectID)
				output["audio_object_id"] = objectID
			}
		}
	}

	return engine.NodeResult{NextHandle: "out", Output: output}
}
