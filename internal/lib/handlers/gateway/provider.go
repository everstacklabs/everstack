package gateway

import (
	"context"
	"fmt"
)

// ErrNotSupported is returned when a provider doesn't support an operation.
type ErrNotSupported struct {
	Operation string
	Provider  string
}

func (e ErrNotSupported) Error() string {
	return fmt.Sprintf("%s: operation %s not supported", e.Provider, e.Operation)
}

// ChatProvider defines chat completion capabilities (unary and streaming).
type ChatProvider interface {
	Chat(ctx context.Context, request ChatCompletionRequest) (ChatCompletionResponse, error)
	ChatStream(ctx context.Context, request ChatCompletionRequest, onChunk func(ChatResponseChunk) error) error
}

// EmbeddingsProvider defines embedding generation capability.
type EmbeddingsProvider interface {
	Embed(ctx context.Context, request EmbeddingsRequest) (EmbeddingsResponse, error)
}

// AudioProvider defines audio generation and transcription capabilities.
type AudioProvider interface {
	// Speech generates audio from text (TTS).
	Speech(ctx context.Context, request SpeechRequest) (SpeechResponse, error)
	// Transcribe converts audio to text (STT).
	Transcribe(ctx context.Context, request TranscriptionRequest) (TranscriptionResponse, error)
	// Translate converts audio to English text.
	Translate(ctx context.Context, request TranslationRequest) (TranslationResponse, error)
}

// VoiceEnroller is an optional interface for providers that support voice cloning
// enrollment. The returned string is the provider-specific voice ID.
type VoiceEnroller interface {
	EnrollVoice(ctx context.Context, referenceAudio []byte, referenceText string, targetModel string, preferredName string) (providerVoiceID string, err error)
}

// ImageProvider defines image generation and editing capabilities.
type ImageProvider interface {
	// GenerateImage generates images from text prompts.
	GenerateImage(ctx context.Context, request ImageGenerationRequest) (ImageGenerationResponse, error)
	// EditImage edits an existing image.
	EditImage(ctx context.Context, request ImageEditRequest) (ImageEditResponse, error)
	// CreateImageVariation creates variations of an image.
	CreateImageVariation(ctx context.Context, request ImageVariationRequest) (ImageVariationResponse, error)
}

// ModerationProvider defines content moderation capabilities.
type ModerationProvider interface {
	// Moderate classifies content for policy violations.
	Moderate(ctx context.Context, request ModerationRequest) (ModerationResponse, error)
}

// RerankProvider defines document reranking capabilities.
type RerankProvider interface {
	// Rerank reorders documents by relevance to a query.
	Rerank(ctx context.Context, request RerankRequest) (RerankResponse, error)
}

// ResponsesProvider defines agentic response capabilities.
type ResponsesProvider interface {
	// CreateResponse creates a response with automatic tool calling.
	CreateResponse(ctx context.Context, request CreateResponseRequest) (CreateResponseResponse, error)
	// CreateResponseStream creates a streaming response.
	CreateResponseStream(ctx context.Context, request CreateResponseRequest, onChunk func(CreateResponseResponse) error) error
}

// Provider is a unified interface a connector can implement. Implementers can
// return an error for unsupported methods.
type Provider interface {
	Name() string
	SupportsModel(model string) bool

	ChatProvider
	EmbeddingsProvider
}

// ExtendedProvider extends Provider with additional capabilities.
// Providers can implement any subset of these interfaces.
type ExtendedProvider interface {
	Provider
	AudioProvider
	ImageProvider
	ModerationProvider
	RerankProvider
	ResponsesProvider
}

// SupportsAudio checks if a provider supports audio operations.
func SupportsAudio(p Provider) bool {
	_, ok := p.(AudioProvider)
	return ok
}

// SupportsImages checks if a provider supports image operations.
func SupportsImages(p Provider) bool {
	_, ok := p.(ImageProvider)
	return ok
}

// SupportsModeration checks if a provider supports moderation.
func SupportsModeration(p Provider) bool {
	_, ok := p.(ModerationProvider)
	return ok
}

// SupportsRerank checks if a provider supports reranking.
func SupportsRerank(p Provider) bool {
	_, ok := p.(RerankProvider)
	return ok
}

// SupportsResponses checks if a provider supports the Responses API.
func SupportsResponses(p Provider) bool {
	_, ok := p.(ResponsesProvider)
	return ok
}

// Registry holds available providers keyed by provider name.
type Registry struct {
	byName map[string]Provider
}

func NewRegistry() *Registry { return &Registry{byName: make(map[string]Provider)} }

// Register adds or replaces a provider under its Name().
func (r *Registry) Register(provider Provider) {
	if r.byName == nil {
		r.byName = make(map[string]Provider)
	}
	r.byName[provider.Name()] = provider
}

// Reset replaces all registered providers with the provided map.
// Keeps the same Registry pointer so existing references remain valid.
func (r *Registry) Reset(providers map[string]Provider) {
	if r.byName == nil {
		r.byName = make(map[string]Provider, len(providers))
	} else {
		for k := range r.byName {
			delete(r.byName, k)
		}
	}
	for name, provider := range providers {
		r.byName[name] = provider
	}
}

// Get returns a provider by its canonical name.
func (r *Registry) Get(name string) (Provider, bool) {
	p, ok := r.byName[name]
	return p, ok
}

// All returns all registered providers.
func (r *Registry) All() map[string]Provider { return r.byName }

// GetAudioProvider returns an AudioProvider if the named provider supports audio.
func (r *Registry) GetAudioProvider(name string) (AudioProvider, bool) {
	p, ok := r.byName[name]
	if !ok {
		return nil, false
	}
	return UnwrapTo[AudioProvider](p)
}

// GetImageProvider returns an ImageProvider if the named provider supports images.
func (r *Registry) GetImageProvider(name string) (ImageProvider, bool) {
	p, ok := r.byName[name]
	if !ok {
		return nil, false
	}
	ip, ok := p.(ImageProvider)
	return ip, ok
}

// GetModerationProvider returns a ModerationProvider if the named provider supports moderation.
func (r *Registry) GetModerationProvider(name string) (ModerationProvider, bool) {
	p, ok := r.byName[name]
	if !ok {
		return nil, false
	}
	mp, ok := p.(ModerationProvider)
	return mp, ok
}

// GetRerankProvider returns a RerankProvider if the named provider supports reranking.
func (r *Registry) GetRerankProvider(name string) (RerankProvider, bool) {
	p, ok := r.byName[name]
	if !ok {
		return nil, false
	}
	rp, ok := p.(RerankProvider)
	return rp, ok
}

// GetResponsesProvider returns a ResponsesProvider if the named provider supports the Responses API.
func (r *Registry) GetResponsesProvider(name string) (ResponsesProvider, bool) {
	p, ok := r.byName[name]
	if !ok {
		return nil, false
	}
	rp, ok := p.(ResponsesProvider)
	return rp, ok
}

// Unwrapper is implemented by middleware wrappers to expose the inner provider.
type Unwrapper interface {
	Unwrap() Provider
}

// UnwrapTo extracts a capability interface from a provider, unwrapping middleware layers.
func UnwrapTo[T any](p Provider) (T, bool) {
	current := p
	for current != nil {
		if t, ok := current.(T); ok {
			return t, true
		}
		if u, ok := current.(Unwrapper); ok {
			current = u.Unwrap()
		} else {
			break
		}
	}
	var zero T
	return zero, false
}

// UnwrapAudioProvider checks if a provider implements AudioProvider,
// unwrapping middleware layers if necessary.
func UnwrapAudioProvider(p Provider) (AudioProvider, bool) {
	return UnwrapTo[AudioProvider](p)
}

// FindAudioProvider finds a provider that supports audio operations,
// unwrapping middleware layers if necessary.
func (r *Registry) FindAudioProvider() (AudioProvider, string, bool) {
	for name, p := range r.byName {
		if ap, ok := UnwrapTo[AudioProvider](p); ok {
			return ap, name, true
		}
	}
	return nil, "", false
}

// FindAudioProviderForModel finds an audio-capable provider that supports the
// given model. This is used for model-based routing of audio requests when the
// model is not in the standard chat router (e.g. DashScope-native TTS models).
func (r *Registry) FindAudioProviderForModel(model string) (AudioProvider, string, bool) {
	for name, p := range r.byName {
		if !p.SupportsModel(model) {
			continue
		}
		if ap, ok := UnwrapTo[AudioProvider](p); ok {
			return ap, name, true
		}
	}
	return nil, "", false
}

// FindImageProvider finds a provider that supports image operations.
func (r *Registry) FindImageProvider() (ImageProvider, string, bool) {
	for name, p := range r.byName {
		if ip, ok := p.(ImageProvider); ok {
			return ip, name, true
		}
	}
	return nil, "", false
}

// FindModerationProvider finds a provider that supports moderation.
func (r *Registry) FindModerationProvider() (ModerationProvider, string, bool) {
	for name, p := range r.byName {
		if mp, ok := p.(ModerationProvider); ok {
			return mp, name, true
		}
	}
	return nil, "", false
}

// FindRerankProvider finds a provider that supports reranking.
func (r *Registry) FindRerankProvider() (RerankProvider, string, bool) {
	for name, p := range r.byName {
		if rp, ok := p.(RerankProvider); ok {
			return rp, name, true
		}
	}
	return nil, "", false
}

// FindResponsesProvider finds a provider that supports the Responses API.
func (r *Registry) FindResponsesProvider() (ResponsesProvider, string, bool) {
	for name, p := range r.byName {
		if rp, ok := p.(ResponsesProvider); ok {
			return rp, name, true
		}
	}
	return nil, "", false
}
