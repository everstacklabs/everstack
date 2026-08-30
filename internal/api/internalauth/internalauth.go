// Package internalauth provides a credential for loopback calls a process
// makes to its own HTTP API.
//
// Several code paths (the eval runner, the scorers, dataset generation) build
// an HTTP request to this gateway's own /v1/chat/completions rather than
// calling the handler directly. Those requests have to get past the API-key
// middleware, and they used to do it by setting
//
//	Sec-Fetch-Site: same-origin
//
// which the middleware treated as proof of a first-party browser call. That is
// not what the header means. Sec-Fetch-Site is forbidden to *scripts*, so a
// page cannot forge it, but it is an ordinary request header that any non
// browser client sets freely. Anyone who could reach the gateway could send it,
// be marked authenticated with no credential at all, and then name any tenant
// they liked through x-tenant-id.
//
// The fix is a real credential with the property the old check only pretended
// to have: it cannot be produced from outside this process. The token is 32
// bytes of crypto/rand generated once at startup and never persisted, logged,
// or sent anywhere except on loopback requests this process makes to itself.
// A caller that is not this process cannot know it.
//
// This is deliberately not a general-purpose service-to-service credential. It
// authenticates "this request came from inside this process" and nothing more.
// Calls that cross a process boundary need a real API key or an M2M token, and
// in particular anything running inside a sandbox must never be handed this
// token, because sandboxes execute untrusted code.
package internalauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
)

// Header carries the internal-call token on loopback requests.
const Header = "x-evs-internal-call"

// token is generated once per process. It is unexported and there is no setter,
// so it cannot be replaced at runtime by configuration or by a test helper that
// might then leak into a production build.
var token = generateToken()

func generateToken() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failing means the platform CSPRNG is unavailable. There is
		// no safe fallback: a predictable token here would be a bypass, and
		// continuing without one would silently break every loopback call. Fail
		// loudly at startup instead.
		panic(fmt.Sprintf("internalauth: cannot read from crypto/rand: %v", err))
	}
	return hex.EncodeToString(buf)
}

// SetHeader stamps the internal-call token onto an outbound loopback request.
// Only use this for requests this process sends to its own API.
func SetHeader(h http.Header) {
	h.Set(Header, token)
}

// Verify reports whether candidate is this process's internal-call token.
// The comparison is constant time so a caller cannot recover the token by
// timing repeated guesses.
func Verify(candidate string) bool {
	if candidate == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(token)) == 1
}

// IsInternalRequest reports whether r carries a valid internal-call token.
func IsInternalRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	return Verify(r.Header.Get(Header))
}

// IsInternalHeader reports whether an arbitrary header set carries a valid
// internal-call token. Used by the Connect/gRPC interceptor, which sees
// http.Header rather than a full *http.Request.
func IsInternalHeader(h http.Header) bool {
	if h == nil {
		return false
	}
	return Verify(h.Get(Header))
}
