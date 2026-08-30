package everstack

import "context"

// AudioResource provides audio operations.
type AudioResource struct {
	Speech         *SpeechResource
	Transcriptions *TranscriptionsResource
	Translations   *TranslationsResource
}

func newAudioResource(t *Transport) *AudioResource {
	return &AudioResource{
		Speech:         &SpeechResource{t: t},
		Transcriptions: &TranscriptionsResource{t: t},
		Translations:   &TranslationsResource{t: t},
	}
}

// SpeechResource provides text-to-speech operations.
type SpeechResource struct {
	t *Transport
}

// Create generates speech audio from text.
func (r *SpeechResource) Create(ctx context.Context, params *SpeechParams) (*SpeechResponse, error) {
	var resp SpeechResponse
	err := r.t.Request(ctx, "POST", "/v1/audio/speech", params, nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// TranscriptionsResource provides speech-to-text operations.
type TranscriptionsResource struct {
	t *Transport
}

// Create transcribes audio to text.
func (r *TranscriptionsResource) Create(ctx context.Context, params *TranscriptionParams) (*TranscriptionResponse, error) {
	var resp TranscriptionResponse
	err := r.t.Request(ctx, "POST", "/v1/audio/transcriptions", params, nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// TranslationsResource provides audio translation operations.
type TranslationsResource struct {
	t *Transport
}

// Create translates audio to English text.
func (r *TranslationsResource) Create(ctx context.Context, params *TranslationParams) (*TranslationResponse, error) {
	var resp TranslationResponse
	err := r.t.Request(ctx, "POST", "/v1/audio/translations", params, nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}
