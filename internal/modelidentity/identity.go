// Package modelidentity separates a serving provider from the publisher and
// canonical model identity. That distinction lets one model aggregate across
// direct, cloud-platform, and meta-provider routes without conflating the
// companies that built and served it.
package modelidentity

import "strings"

type Identity struct {
	Publisher        string
	CanonicalModelID string
}

func Resolve(provider, model string) Identity {
	return ResolveWithOverrides(provider, model, "", "")
}

func ResolveWithOverrides(provider, model, publisherOverride, canonicalOverride string) Identity {
	provider = normalize(provider)
	model = normalizeModel(model)

	publisher, canonicalModel := infer(provider, model)
	if override := normalize(publisherOverride); override != "" {
		publisher = override
	}
	if override := normalizeCanonical(canonicalOverride); override != "" {
		return Identity{Publisher: publisher, CanonicalModelID: override}
	}
	if publisher == "" {
		publisher = provider
	}
	if canonicalModel == "" {
		canonicalModel = model
	}
	return Identity{
		Publisher:        publisher,
		CanonicalModelID: publisher + "/" + canonicalModel,
	}
}

func infer(provider, model string) (publisher, canonicalModel string) {
	switch provider {
	case "azure-openai":
		return "openai", model
	case "aws-bedrock":
		switch {
		case strings.HasPrefix(model, "anthropic."):
			return "anthropic", strings.TrimPrefix(model, "anthropic.")
		case strings.HasPrefix(model, "amazon."):
			return "amazon", strings.TrimPrefix(model, "amazon.")
		case strings.HasPrefix(model, "cohere."):
			return "cohere", strings.TrimPrefix(model, "cohere.")
		case strings.HasPrefix(model, "meta."):
			return "meta", strings.TrimPrefix(model, "meta.")
		}
	case "vertex-ai":
		switch {
		case strings.HasPrefix(model, "claude-"):
			return "anthropic", strings.TrimSuffix(model, "@default")
		case strings.HasPrefix(model, "gemini-"):
			return "google", strings.TrimSuffix(model, "@default")
		}
	case "fireworks":
		return inferFireworks(model)
	case "openrouter", "huggingface", "together", "nvidia-nim":
		if owner, name, ok := splitRepositoryModel(model); ok {
			return normalizePublisher(owner), name
		}
	}

	return normalizePublisher(provider), model
}

func splitRepositoryModel(model string) (string, string, bool) {
	for _, separator := range []string{"/", "__"} {
		if before, after, ok := strings.Cut(model, separator); ok &&
			strings.TrimSpace(before) != "" && strings.TrimSpace(after) != "" {
			return before, after, true
		}
	}
	return "", "", false
}

func inferFireworks(model string) (string, string) {
	canonicalModel := strings.TrimPrefix(model, "accounts/fireworks/models/")
	canonicalModel = strings.TrimPrefix(canonicalModel, "accounts/fireworks/routers/")
	canonicalModel = normalizeFireworksVersion(canonicalModel)

	switch {
	case strings.HasPrefix(canonicalModel, "deepseek-"):
		return "deepseek", canonicalModel
	case strings.HasPrefix(canonicalModel, "glm-"):
		return "zai", canonicalModel
	case strings.HasPrefix(canonicalModel, "gpt-oss-"):
		return "openai", canonicalModel
	case strings.HasPrefix(canonicalModel, "kimi-"):
		return "moonshot", canonicalModel
	case strings.HasPrefix(canonicalModel, "llama-"):
		return "meta", strings.Replace(canonicalModel, "llama-v", "llama-", 1)
	case strings.HasPrefix(canonicalModel, "minimax-"):
		return "minimax", canonicalModel
	case strings.HasPrefix(canonicalModel, "nomic-"):
		return "nomic", canonicalModel
	case strings.HasPrefix(canonicalModel, "qwen"):
		return "qwen", canonicalModel
	default:
		return "fireworks", canonicalModel
	}
}

func normalizeFireworksVersion(value string) string {
	characters := []rune(value)
	for index := 1; index < len(characters)-1; index++ {
		if characters[index] == 'p' &&
			characters[index-1] >= '0' && characters[index-1] <= '9' &&
			characters[index+1] >= '0' && characters[index+1] <= '9' {
			characters[index] = '.'
		}
	}
	return string(characters)
}

func normalizePublisher(value string) string {
	value = normalize(value)
	switch value {
	case "alibaba":
		return "qwen"
	case "deepseek-ai":
		return "deepseek"
	case "meta-llama":
		return "meta"
	case "minimaxai":
		return "minimax"
	case "moonshotai":
		return "moonshot"
	case "stepfun-ai":
		return "stepfun"
	case "x-ai":
		return "xai"
	case "z-ai", "zai-org":
		return "zai"
	case "togetherai":
		return "together"
	case "amazon-bedrock":
		return "amazon"
	default:
		return value
	}
}

func normalizeCanonical(value string) string {
	value = strings.Trim(strings.ToLower(strings.TrimSpace(value)), "/")
	if value == "" || !strings.Contains(value, "/") {
		return ""
	}
	return value
}

func normalizeModel(value string) string {
	return strings.Trim(strings.ToLower(strings.TrimSpace(value)), "/")
}

func normalize(value string) string {
	return strings.Trim(strings.ToLower(strings.TrimSpace(value)), "/")
}
