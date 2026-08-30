package http

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// M2M authentication headers (must match server-side constants). Canonical form
// is x-evs-*; we also emit the legacy x-mf-* names during the migration so a
// rolling deploy where the verifier hasn't updated yet still authenticates.
// The HMAC payload excludes header names, so emitting both is signature-safe.
const (
	headerServiceToken = "x-evs-service-token"
	headerInstanceID   = "x-evs-instance-id"
	headerSignature    = "x-evs-signature"
	headerTimestamp    = "x-evs-timestamp"
	headerNonce        = "x-evs-nonce"

	legacyHeaderServiceToken = "x-mf-service-token"
	legacyHeaderInstanceID   = "x-mf-instance-id"
	legacyHeaderSignature    = "x-mf-signature"
	legacyHeaderTimestamp    = "x-mf-timestamp"
	legacyHeaderNonce        = "x-mf-nonce"
)

// M2MTransport wraps an http.RoundTripper to add M2M authentication headers.
// It signs each request with HMAC-SHA256 using the provided credentials.
type M2MTransport struct {
	// Base is the underlying transport. If nil, http.DefaultTransport is used.
	Base http.RoundTripper

	// ClientType identifies the type of client ("gateway", "portal", "internal")
	ClientType string

	// InstanceID is the gateway instance identifier (for gateway clients)
	InstanceID string

	// ServiceToken is the service token (for portal/internal clients)
	ServiceToken string

	// SigningKey is the HMAC signing key
	SigningKey []byte
}

// RoundTrip implements http.RoundTripper and adds M2M authentication headers.
func (t *M2MTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request to avoid mutating the original
	reqClone := req.Clone(req.Context())

	// Generate timestamp and nonce
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := uuid.New().String()

	// Add identity header based on client type (emit canonical + legacy names)
	switch t.ClientType {
	case "gateway":
		reqClone.Header.Set(headerInstanceID, t.InstanceID)
		reqClone.Header.Set(legacyHeaderInstanceID, t.InstanceID)
	case "portal", "internal", "billing":
		reqClone.Header.Set(headerServiceToken, t.ServiceToken)
		reqClone.Header.Set(legacyHeaderServiceToken, t.ServiceToken)
	default:
		// For any other service type, use service token if available
		if t.ServiceToken != "" {
			reqClone.Header.Set(headerServiceToken, t.ServiceToken)
			reqClone.Header.Set(legacyHeaderServiceToken, t.ServiceToken)
		}
	}

	// Add timestamp and nonce (canonical + legacy)
	reqClone.Header.Set(headerTimestamp, timestamp)
	reqClone.Header.Set(legacyHeaderTimestamp, timestamp)
	reqClone.Header.Set(headerNonce, nonce)
	reqClone.Header.Set(legacyHeaderNonce, nonce)

	// Compute and add signature
	// NOTE: We use empty body for signature computation because the Connect/gRPC
	// server interceptor doesn't have access to the request body at middleware level.
	// Both client and server must use empty body hash for consistency.
	signature := computeSignature(
		reqClone.Method,
		reqClone.URL.Path,
		nil, // Empty body - server interceptor also uses nil
		timestamp,
		nonce,
		t.SigningKey,
	)
	reqClone.Header.Set(headerSignature, signature)
	reqClone.Header.Set(legacyHeaderSignature, signature)

	// Use base transport or default
	transport := t.Base
	if transport == nil {
		transport = http.DefaultTransport
	}

	return transport.RoundTrip(reqClone)
}

// computeSignature computes the HMAC-SHA256 signature for a request.
// The canonical format is: METHOD\nPATH\nTIMESTAMP\nNONCE\nSHA256(BODY)
func computeSignature(method, path string, body []byte, timestamp, nonce string, key []byte) string {
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

// NewGatewayM2MTransport creates an M2M transport for gateway instances.
func NewGatewayM2MTransport(base http.RoundTripper, instanceID string, signingKey []byte) *M2MTransport {
	return &M2MTransport{
		Base:       base,
		ClientType: "gateway",
		InstanceID: instanceID,
		SigningKey: signingKey,
	}
}

// NewServiceM2MTransport creates an M2M transport for internal services.
func NewServiceM2MTransport(base http.RoundTripper, clientType, serviceToken string, signingKey []byte) *M2MTransport {
	return &M2MTransport{
		Base:         base,
		ClientType:   clientType,
		ServiceToken: serviceToken,
		SigningKey:   signingKey,
	}
}

// M2MHTTPClient creates an HTTP client with M2M authentication for gateway instances.
func M2MHTTPClient(instanceID string, signingKey []byte, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: NewGatewayM2MTransport(
			http.DefaultTransport,
			instanceID,
			signingKey,
		),
	}
}

// ServiceM2MHTTPClient creates an HTTP client with M2M authentication for services.
func ServiceM2MHTTPClient(clientType, serviceToken string, signingKey []byte, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: NewServiceM2MTransport(
			http.DefaultTransport,
			clientType,
			serviceToken,
			signingKey,
		),
	}
}
