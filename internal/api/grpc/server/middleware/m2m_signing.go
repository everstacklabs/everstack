package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/api/common"
)

// M2M authentication headers. Canonical form is x-evs-*; the legacy x-mf-* names
// are still accepted (see legacyHeader* below). The HMAC payload is
// METHOD\nPATH\nTIMESTAMP\nNONCE\nSHA256(BODY) — header *names* are not signed —
// so reading either name is signature-safe.
const (
	// HeaderServiceToken identifies a service client (portal, internal services)
	HeaderServiceToken = "x-evs-service-token"
	// HeaderInstanceID identifies a gateway instance
	HeaderInstanceID = "x-evs-instance-id"
	// HeaderSignature contains the HMAC-SHA256 signature of the request
	HeaderSignature = "x-evs-signature"
	// HeaderTimestamp contains the Unix timestamp (seconds) when request was signed
	HeaderTimestamp = "x-evs-timestamp"
	// HeaderNonce contains a unique identifier to prevent replay attacks
	HeaderNonce = "x-evs-nonce"

	// Legacy x-mf-* M2M header names, accepted for backward compatibility during
	// rolling deploys where a not-yet-updated signer still emits them.
	legacyHeaderServiceToken = "x-mf-service-token"
	legacyHeaderInstanceID   = "x-mf-instance-id"
	legacyHeaderSignature    = "x-mf-signature"
	legacyHeaderTimestamp    = "x-mf-timestamp"
	legacyHeaderNonce        = "x-mf-nonce"
)

// M2M signature errors
var (
	ErrMissingSignature = errors.New("missing signature header")
	ErrMissingTimestamp = errors.New("missing timestamp header")
	ErrMissingNonce     = errors.New("missing nonce header")
	ErrInvalidTimestamp = errors.New("invalid timestamp format")
	ErrTimestampExpired = errors.New("request timestamp expired")
	ErrTimestampFuture  = errors.New("request timestamp is in the future")
	ErrInvalidSignature = errors.New("invalid signature")
	ErrMissingIdentity  = errors.New("missing identity header (service token or instance ID)")
	ErrNonceReused      = errors.New("nonce has already been used (replay attack detected)")
	ErrClientNotAllowed = errors.New("client type not allowed for this procedure")
	ErrUnknownClient    = errors.New("unknown client identity")
)

// ComputeSignature computes the HMAC-SHA256 signature for a request.
// The canonical format is: METHOD\nPATH\nTIMESTAMP\nNONCE\nSHA256(BODY)
func ComputeSignature(method, path string, body []byte, timestamp, nonce string, key []byte) string {
	// Compute SHA256 hash of body
	bodyHash := sha256.Sum256(body)
	bodyHashHex := fmt.Sprintf("%x", bodyHash)

	// Build canonical request string
	canonical := strings.Join([]string{
		strings.ToUpper(method),
		path,
		timestamp,
		nonce,
		bodyHashHex,
	}, "\n")

	// Compute HMAC-SHA256
	h := hmac.New(sha256.New, key)
	h.Write([]byte(canonical))
	signature := h.Sum(nil)

	return base64.StdEncoding.EncodeToString(signature)
}

// VerifySignature verifies the HMAC signature of a request.
// It checks:
// 1. All required headers are present
// 2. Timestamp is within the allowed window
// 3. HMAC signature matches
func VerifySignature(method, path string, body []byte, headers http.Header, key []byte, window time.Duration) error {
	// Extract headers (canonical x-evs-*, falling back to legacy x-mf-*)
	signature := common.GetHeader(headers.Get, HeaderSignature, legacyHeaderSignature)
	timestampStr := common.GetHeader(headers.Get, HeaderTimestamp, legacyHeaderTimestamp)
	nonce := common.GetHeader(headers.Get, HeaderNonce, legacyHeaderNonce)

	if signature == "" {
		return ErrMissingSignature
	}
	if timestampStr == "" {
		return ErrMissingTimestamp
	}
	if nonce == "" {
		return ErrMissingNonce
	}

	// Parse and validate timestamp
	timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
	if err != nil {
		return ErrInvalidTimestamp
	}

	requestTime := time.Unix(timestamp, 0)
	now := time.Now()

	// Check timestamp is not too old
	if now.Sub(requestTime) > window {
		return ErrTimestampExpired
	}

	// Check timestamp is not in the future (with small tolerance for clock skew)
	if requestTime.Sub(now) > 30*time.Second {
		return ErrTimestampFuture
	}

	// Compute expected signature
	expectedSignature := ComputeSignature(method, path, body, timestampStr, nonce, key)

	// Compare signatures (constant time to prevent timing attacks)
	expectedBytes, err := base64.StdEncoding.DecodeString(expectedSignature)
	if err != nil {
		return ErrInvalidSignature
	}

	actualBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return ErrInvalidSignature
	}

	if !hmac.Equal(expectedBytes, actualBytes) {
		return ErrInvalidSignature
	}

	return nil
}

// ExtractClientIdentity extracts the client identity from request headers.
// Returns (clientType, clientID, error)
// - For gateway: ("gateway", instanceID, nil)
// - For services: ("service", serviceToken, nil)
func ExtractClientIdentity(headers http.Header) (clientType, clientID string, err error) {
	// Check for instance ID first (gateway client)
	if instanceID := common.GetHeader(headers.Get, HeaderInstanceID, legacyHeaderInstanceID); instanceID != "" {
		return "gateway", instanceID, nil
	}

	// Check for service token (portal, internal services)
	if serviceToken := common.GetHeader(headers.Get, HeaderServiceToken, legacyHeaderServiceToken); serviceToken != "" {
		return "service", serviceToken, nil
	}

	return "", "", ErrMissingIdentity
}

// GetNonce extracts the nonce from request headers
func GetNonce(headers http.Header) string {
	return common.GetHeader(headers.Get, HeaderNonce, legacyHeaderNonce)
}

// GetTimestamp extracts the timestamp from request headers
func GetTimestamp(headers http.Header) string {
	return common.GetHeader(headers.Get, HeaderTimestamp, legacyHeaderTimestamp)
}

// ServiceCredential holds credentials for a pre-registered service
type ServiceCredential struct {
	Name       string // Service name (e.g., "portal", "internal")
	TokenHash  string // SHA256 hash of the service token
	SigningKey []byte // HMAC signing key
}

// HashToken computes the SHA256 hash of a token for storage/comparison
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", h)
}

// ValidateTokenHash validates a token against its stored hash
func ValidateTokenHash(token, storedHash string) bool {
	computedHash := HashToken(token)
	return hmac.Equal([]byte(computedHash), []byte(storedHash))
}
