package ratelimit

// rateLimitHeaderMap defines header keys used by a provider for rate-limit reporting.
type rateLimitHeaderMap struct {
	ReqLimit       []string
	ReqRemaining   []string
	ReqReset       []string
	TokenLimit     []string
	TokenRemaining []string
	TokenReset     []string
	RetryAfter     []string
}

// Standard rate-limit header names (package-private, only used in header maps below).
const (
	hdrXRateLimitLimit           = "x-ratelimit-limit"
	hdrXRateLimitRemaining       = "x-ratelimit-remaining"
	hdrXRateLimitReset           = "x-ratelimit-reset"
	hdrXRateLimitLimitTokens     = "x-ratelimit-limit-tokens"
	hdrXRateLimitRemainingTokens = "x-ratelimit-remaining-tokens"
	hdrXRateLimitResetTokens     = "x-ratelimit-reset-tokens"
	hdrRetryAfter                = "retry-after"
	hdrRateLimitLimit            = "rate-limit-limit"
	hdrRateLimitRemaining        = "rate-limit-remaining"
	hdrRateLimitReset            = "rate-limit-reset"
	hdrRateLimitLimitTokens      = "rate-limit-limit-tokens"
	hdrRateLimitRemainingTokens  = "rate-limit-remaining-tokens"
	hdrRateLimitResetTokens      = "rate-limit-reset-tokens"

	// OpenAI-specific headers (per docs)
	hdrOpenAIReqLimit       = "x-ratelimit-limit-requests"
	hdrOpenAIReqRemaining   = "x-ratelimit-remaining-requests"
	hdrOpenAIReqReset       = "x-ratelimit-reset-requests"
	hdrOpenAITokenLimit     = "x-ratelimit-limit-tokens"
	hdrOpenAITokenRemaining = "x-ratelimit-remaining-tokens"
	hdrOpenAITokenReset     = "x-ratelimit-reset-tokens"

	// Anthropic-specific headers (per docs)
	hdrAnthReqLimit       = "anthropic-ratelimit-requests-limit"
	hdrAnthReqRemaining   = "anthropic-ratelimit-requests-remaining"
	hdrAnthReqReset       = "anthropic-ratelimit-requests-reset"
	hdrAnthTokenLimit     = "anthropic-ratelimit-tokens-limit"
	hdrAnthTokenRemaining = "anthropic-ratelimit-tokens-remaining"
	hdrAnthTokenReset     = "anthropic-ratelimit-tokens-reset"
)

// stdHeaderMap captures common/standard header names used by several providers.
// Providers without a specific map (cohere, google, mistral, etc.) fall through to this.
var stdHeaderMap = rateLimitHeaderMap{
	ReqLimit:       []string{hdrXRateLimitLimit, hdrRateLimitLimit},
	ReqRemaining:   []string{hdrXRateLimitRemaining, hdrRateLimitRemaining},
	ReqReset:       []string{hdrXRateLimitReset, hdrRateLimitReset},
	TokenLimit:     []string{hdrXRateLimitLimitTokens, hdrRateLimitLimitTokens},
	TokenRemaining: []string{hdrXRateLimitRemainingTokens, hdrRateLimitRemainingTokens},
	TokenReset:     []string{hdrXRateLimitResetTokens, hdrRateLimitResetTokens},
	RetryAfter:     []string{hdrRetryAfter},
}

var openaiHeaderMap = rateLimitHeaderMap{
	// Prefer OpenAI-specific, include standard as fallback
	ReqLimit:       []string{hdrOpenAIReqLimit, hdrXRateLimitLimit, hdrRateLimitLimit},
	ReqRemaining:   []string{hdrOpenAIReqRemaining, hdrXRateLimitRemaining, hdrRateLimitRemaining},
	ReqReset:       []string{hdrOpenAIReqReset, hdrXRateLimitReset, hdrRateLimitReset},
	TokenLimit:     []string{hdrOpenAITokenLimit, hdrXRateLimitLimitTokens, hdrRateLimitLimitTokens},
	TokenRemaining: []string{hdrOpenAITokenRemaining, hdrXRateLimitRemainingTokens, hdrRateLimitRemainingTokens},
	TokenReset:     []string{hdrOpenAITokenReset, hdrXRateLimitResetTokens, hdrRateLimitResetTokens},
	RetryAfter:     []string{hdrRetryAfter},
}

var anthropicHeaderMap = rateLimitHeaderMap{
	// Prefer Anthropic-specific, include standard as fallback
	ReqLimit:       []string{hdrAnthReqLimit, hdrXRateLimitLimit, hdrRateLimitLimit},
	ReqRemaining:   []string{hdrAnthReqRemaining, hdrXRateLimitRemaining, hdrRateLimitRemaining},
	ReqReset:       []string{hdrAnthReqReset, hdrXRateLimitReset, hdrRateLimitReset},
	TokenLimit:     []string{hdrAnthTokenLimit, hdrXRateLimitLimitTokens, hdrRateLimitLimitTokens},
	TokenRemaining: []string{hdrAnthTokenRemaining, hdrXRateLimitRemainingTokens, hdrRateLimitRemainingTokens},
	TokenReset:     []string{hdrAnthTokenReset, hdrXRateLimitResetTokens, hdrRateLimitResetTokens},
	RetryAfter:     []string{hdrRetryAfter},
}

var providerHeaderMaps = map[string]rateLimitHeaderMap{
	"openai":    openaiHeaderMap,
	"anthropic": anthropicHeaderMap,
}
