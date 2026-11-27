package internalauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// GatewayIdentityHeader is emitted only by the in-process REST transcoder.
const GatewayIdentityHeader = "x-evs-gateway-key-identity"

// GatewayIdentity is the API-key identity already verified by HTTP middleware.
// The signed envelope preserves NULL versus empty surfaces across the RPC hop.
type GatewayIdentity struct {
	TenantID     string
	KeyID        string
	Hash         string
	Surfaces     []string
	RateLimitRPM *int
	Processed    bool
	IssuedAt     int64
}

func SignGatewayIdentity(identity GatewayIdentity) string {
	identity.IssuedAt = time.Now().Unix()
	data, _ := json.Marshal(identity)
	payload := base64.RawURLEncoding.EncodeToString(data)
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// GatewayKeyIdentity never trusts an unsigned client header. The signing key
// is the process-local internal-call credential, not a configured client secret.
func GatewayKeyIdentity(h http.Header) (GatewayIdentity, bool) {
	var identity GatewayIdentity
	parts := strings.Split(h.Get(GatewayIdentityHeader), ".")
	if len(parts) != 2 {
		return identity, false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return identity, false
	}
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte(parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return identity, false
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return identity, false
	}
	if json.Unmarshal(data, &identity) != nil {
		return GatewayIdentity{}, false
	}
	age := time.Now().Unix() - identity.IssuedAt
	return identity, age >= 0 && age <= 30 && identity.Hash != "" && identity.TenantID != ""
}

// GatewayTransportHeader identifies the gateway's own loopback RPC hop. This
// credential never grants authentication; it only permits the signed envelope
// through the HTTP edge sanitizer. It is separate from the internal-call token.
const GatewayTransportHeader = "x-evs-gateway-transport"

var gatewayTransportToken = generateToken()

func SetGatewayTransportHeader(h http.Header) { h.Set(GatewayTransportHeader, gatewayTransportToken) }
func IsGatewayTransport(h http.Header) bool {
	values := h.Values(GatewayTransportHeader)
	return len(values) == 1 && hmac.Equal([]byte(values[0]), []byte(gatewayTransportToken))
}
