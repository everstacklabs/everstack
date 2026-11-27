package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// ErrNotImplemented is returned when a provider capability isn't implemented.
type ErrNotImplemented string

func (e ErrNotImplemented) Error() string {
	return fmt.Sprintf("provider capability not implemented: %s", string(e))
}

// RouterError is returned when routing/config selection fails for a request.
type RouterError struct {
	Message string
}

func (e RouterError) Error() string { return e.Message }

// ErrModelNotFound is returned when the requested model is not available or not activated.
// This is a non-retriable error that should NOT trigger fallback to other models.
type ErrModelNotFound struct {
	RequestedModel string
	Message        string
}

func (e *ErrModelNotFound) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("model not found: %s (%s)", e.RequestedModel, e.Message)
	}
	return fmt.Sprintf("model not found or not activated: %s", e.RequestedModel)
}

// IsModelNotFoundError checks if an error is a model not found error.
func IsModelNotFoundError(err error) bool {
	var modelNotFoundErr *ErrModelNotFound
	return errors.As(err, &modelNotFoundErr)
}

// IsNonRetriableError checks if an error should NOT trigger fallback.
// Model not found errors and not implemented errors are configuration/capability issues and should fail fast.
func IsNonRetriableError(err error) bool {
	if IsModelNotFoundError(err) {
		return true
	}

	// A model trial refusal is terminal: a retry is refused again and a
	// fallback target would serve the request on a key the caller did not
	// choose. Matched by method so this package need not import billing.
	var trial interface{ ModelTrialRefusal() string }
	if errors.As(err, &trial) {
		return true
	}

	// Check if error is ErrNotImplemented (e.g., streaming not supported)
	var notImplErr ErrNotImplemented
	if errors.As(err, &notImplErr) {
		return true
	}

	return false
}

// UpstreamError is a non-2xx HTTP response from an upstream model provider,
// decoded from the provider's error envelope. It keeps the upstream status and
// the fields a caller needs to act on (message, type, code, param) so the
// gateway can answer with a matching 4xx and a readable message instead of an
// opaque 500 carrying a raw JSON body.
type UpstreamError struct {
	Provider   string
	StatusCode int
	Message    string
	Type       string
	Code       string
	Param      string
}

func (e *UpstreamError) Error() string {
	provider := e.Provider
	if provider == "" {
		provider = "provider"
	}
	msg := e.Message
	if msg == "" {
		msg = "no error message in upstream response"
	}
	out := fmt.Sprintf("%s returned %d: %s", provider, e.StatusCode, msg)
	var details []string
	if e.Type != "" {
		details = append(details, "type="+e.Type)
	}
	if e.Code != "" {
		details = append(details, "code="+e.Code)
	}
	if e.Param != "" {
		details = append(details, "param="+e.Param)
	}
	if len(details) > 0 {
		out += " (" + strings.Join(details, ", ") + ")"
	}
	return out
}

// HTTPStatusCode reports the upstream HTTP status. retrypolicy classifies
// errors through this method without importing this package.
func (e *UpstreamError) HTTPStatusCode() int { return e.StatusCode }

// NewUpstreamError decodes an upstream error body. It understands the
// OpenAI-style envelope ({"error":{"message","type","code","param"}}), which
// most OpenAI-compatible providers share. A body it cannot decode is kept as
// the message (trimmed and bounded) so the caller never sees an empty error.
func NewUpstreamError(provider string, statusCode int, body []byte) *UpstreamError {
	e := &UpstreamError{Provider: provider, StatusCode: statusCode}
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil && len(envelope.Error) > 0 {
		var detail struct {
			Message interface{} `json:"message"`
			Type    interface{} `json:"type"`
			Code    interface{} `json:"code"`
			Param   interface{} `json:"param"`
		}
		if json.Unmarshal(envelope.Error, &detail) == nil {
			e.Message = scalarString(detail.Message)
			e.Type = scalarString(detail.Type)
			e.Code = scalarString(detail.Code)
			e.Param = scalarString(detail.Param)
		} else {
			// {"error":"plain string"}
			var s string
			if json.Unmarshal(envelope.Error, &s) == nil {
				e.Message = s
			}
		}
	}
	if e.Message == "" {
		e.Message = strings.TrimSpace(string(body))
		const maxRaw = 512
		if len(e.Message) > maxRaw {
			e.Message = e.Message[:maxRaw] + "..."
		}
	}
	if e.Message == "" {
		e.Message = http.StatusText(statusCode)
	}
	return e
}

func scalarString(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return ""
	}
}

// AsUpstreamError returns the first UpstreamError in err's chain.
func AsUpstreamError(err error) (*UpstreamError, bool) {
	var upstream *UpstreamError
	if errors.As(err, &upstream) && upstream != nil {
		return upstream, true
	}
	return nil, false
}

// ProviderConfigError reports that a provider is configured in a way that
// cannot work (for example an API key that is not a valid HTTP header value),
// detected before any upstream call. It is the operator's to fix, so the
// gateway answers with a failed-precondition rather than a 500.
type ProviderConfigError struct {
	Provider string
	Message  string
}

func (e *ProviderConfigError) Error() string {
	return fmt.Sprintf("%s provider misconfigured: %s", e.Provider, e.Message)
}

// ValidateAPIKeyFormat checks that an API key can be sent verbatim in an HTTP
// header: printable ASCII with no whitespace. Provider keys from every vendor
// we support satisfy this; anything else is pasted text, not a key, and the
// upstream answers it with an opaque 400. The returned error never echoes the
// key.
func ValidateAPIKeyFormat(provider, key string) error {
	for i := 0; i < len(key); i++ {
		if c := key[i]; c < 0x21 || c > 0x7e {
			return &ProviderConfigError{
				Provider: provider,
				Message: fmt.Sprintf("the configured API key is malformed: it contains whitespace or non-ASCII characters "+
					"(length %d); re-enter the key in the provider settings", len(key)),
			}
		}
	}
	return nil
}
