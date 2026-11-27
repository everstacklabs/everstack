package gateway

import (
	"encoding/json"
)

// Response-cache fingerprints for the normalized request types.
//
// A fingerprint is the canonical JSON of the whole request with only
// transport fields cleared (stream flag, caller metadata), so every field that
// can change the model's output (model, all messages and content parts, tool
// calls, system prompt / instructions, tools, tool choice, response format,
// reasoning, sampling, previous_response_id) is part of the exact-cache key.
// encoding/json sorts map keys, so the encoding is deterministic.
//
// The semantic-context variant additionally blanks the text of the final user
// turn, which is the semantic query itself; everything else must match
// exactly for a semantic hit. A nil result means "do not cache this request".

// ChatCacheFingerprint returns the exact-cache fingerprint of a chat request.
func ChatCacheFingerprint(req ChatCompletionRequest) []byte {
	req.Stream = false
	req.Metadata = nil
	return marshalFingerprint("chat", req)
}

// ChatSemanticContextFingerprint returns the semantic-cache context
// fingerprint of a chat request (the final user turn's text left out).
func ChatSemanticContextFingerprint(req ChatCompletionRequest) []byte {
	req.Stream = false
	req.Metadata = nil
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role != RoleUser {
			continue
		}
		msgs := make([]Message, len(req.Messages))
		copy(msgs, req.Messages)
		msgs[i].Content = blankTextParts(msgs[i].Content)
		req.Messages = msgs
		break
	}
	return marshalFingerprint("chat-semantic", req)
}

// ResponseCacheFingerprint returns the exact-cache fingerprint of a Responses
// API request.
func ResponseCacheFingerprint(req CreateResponseRequest) []byte {
	req.Stream = false
	req.Metadata = nil
	return marshalFingerprint("responses", req)
}

// ResponseSemanticContextFingerprint returns the semantic-cache context
// fingerprint of a Responses API request (the final user message's text left
// out; instructions, earlier input items, tools etc. retained).
func ResponseSemanticContextFingerprint(req CreateResponseRequest) []byte {
	req.Stream = false
	req.Metadata = nil
	for i := len(req.Input) - 1; i >= 0; i-- {
		if req.Input[i].Type != "message" || req.Input[i].Role != RoleUser {
			continue
		}
		items := make([]ResponseInput, len(req.Input))
		copy(items, req.Input)
		items[i].Content = blankTextParts(items[i].Content)
		req.Input = items
		break
	}
	return marshalFingerprint("responses-semantic", req)
}

// cacheFingerprintEnvelope carries the request type plus the fields the JSON
// encoding hides (json:"-") but that still change the output.
type cacheFingerprintEnvelope struct {
	Kind    string      `json:"k"`
	Request interface{} `json:"r"`
	Extra   interface{} `json:"x,omitempty"`
}

func marshalFingerprint(kind string, req interface{}) []byte {
	env := cacheFingerprintEnvelope{Kind: kind, Request: req}
	// The *Configured flags distinguish an explicit zero (e.g. temperature 0)
	// from "provider default", which produce different outputs.
	switch r := req.(type) {
	case ChatCompletionRequest:
		env.Extra = struct {
			UseResponsesAPI            bool
			Temp, TopP, Freq, Presence bool
		}{r.UseResponsesAPI, r.Sampling.TemperatureConfigured, r.Sampling.TopPConfigured,
			r.Sampling.FrequencyConfigured, r.Sampling.PresenceConfigured}
	case CreateResponseRequest:
		env.Extra = struct {
			MaxOutput, Temp, TopP bool
		}{r.MaxOutputConfigured, r.TemperatureConfigured, r.TopPConfigured}
	}
	b, err := json.Marshal(env)
	if err != nil {
		// Unencodable request: refuse to cache rather than risk a key that
		// several different requests share.
		return nil
	}
	return b
}

// blankTextParts drops the text of text parts, keeping every other part
// (images, files, tool results) because those still shape the answer.
func blankTextParts(parts []ContentPart) []ContentPart {
	out := make([]ContentPart, len(parts))
	for i, p := range parts {
		if p.Type == "text" {
			out[i] = ContentPart{Type: "text"}
			continue
		}
		out[i] = p
	}
	return out
}
