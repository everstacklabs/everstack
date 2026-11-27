package retrypolicy

import (
	"errors"
	"testing"
)

// A Cohere 400 or 429 is not an authentication failure. Classifying it as
// one sent every Cohere error through up to 10 key rotations, marking good
// keys failed. Untyped text is classified only on explicit 401/403 markers.
func TestAuthClassificationIgnoresProviderPrefixAndBareNumbers(t *testing.T) {
	cases := []struct {
		err  error
		auth bool
	}{
		// The Cohere adapter's untyped error text.
		{errors.New(`cohere error: {"message":"You are using a Trial key, which is limited to 10 API calls / minute"}`), false},
		{errors.New(`cohere error: {"message":"invalid request: message must be at least 1 token long"}`), false},
		// A typed Cohere 429 and 400.
		{statusErr{429, `cohere error: too many requests`}, false},
		{statusErr{400, `cohere error: bad request`}, false},
		// Numbers that merely contain 401/403.
		{errors.New("prompt is 14013 tokens, limit 4010"), false},
		{errors.New("request 4031 failed upstream with a 500"), false},
		// Explicit markers still classify untyped errors.
		{errors.New(`cohere error: {"message":"invalid api token"}`), true},
		{errors.New("anthropic returned 401: invalid x-api-key"), true},
		{errors.New("upstream status code: 403"), true},
		{errors.New("HTTP 401 Unauthorized"), true},
		{errors.New("403 Forbidden"), true},
	}
	for _, tc := range cases {
		if got := IsAuthenticationError(tc.err); got != tc.auth {
			t.Errorf("IsAuthenticationError(%q) = %v, want %v", tc.err, got, tc.auth)
		}
	}
}

// The untyped adapters put only the upstream body in the error text, so the
// body must carry the classification. These are the providers' documented
// invalid-key bodies as the adapters wrap them.
func TestAuthClassificationOnProviderBodies(t *testing.T) {
	cases := []struct {
		err  error
		auth bool
	}{
		{errors.New(`azure-openai chat error: {"error":{"code":"401","message":"Access denied due to invalid subscription key or wrong API endpoint."}}`), true},
		{errors.New(`anthropic chat error: {"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`), true},
		{errors.New(`anthropic chat error: {"type":"error","error":{"type":"permission_error","message":"Your API key does not have permission to use the specified resource."}}`), true},
		{errors.New(`google error: {"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT","details":[{"reason":"API_KEY_INVALID"}]}}`), true},
		{errors.New(`vertex-ai error: {"error":{"code":401,"message":"Request had invalid authentication credentials.","status":"UNAUTHENTICATED"}}`), true},
		{errors.New(`deepseek error: {"error":{"message":"Authentication Fails, Your api key: ****abcd is invalid","type":"authentication_error"}}`), true},
		{errors.New(`qwen error: {"code":"InvalidApiKey","message":"Invalid API-key provided."}`), true},
		{errors.New(`mistral error: {"message":"Unauthorized","request_id":"x"}`), true},
		// Not auth: rate limits and bad requests from the same adapters.
		{errors.New(`anthropic chat error: {"type":"error","error":{"type":"rate_limit_error","message":"Number of request tokens has exceeded your per-minute rate limit"}}`), false},
		{errors.New(`google error: {"error":{"code":400,"message":"Invalid JSON payload received.","status":"INVALID_ARGUMENT"}}`), false},
		{errors.New(`azure-openai chat error: {"error":{"code":"429","message":"Requests to the ChatCompletions_Create Operation have exceeded call rate limit."}}`), false},
	}
	for _, tc := range cases {
		if got := IsAuthenticationError(tc.err); got != tc.auth {
			t.Errorf("IsAuthenticationError(%q) = %v, want %v", tc.err, got, tc.auth)
		}
	}
}
