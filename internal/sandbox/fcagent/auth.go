package fcagent

import (
	"context"
	"os"
	"strings"
)

// authTokenEnv carries the shared secret the agents expect as
// "authorization: Bearer <token>". Read here, in the one place every
// caller (sandbox backend, functions runtime, ssh proxy) passes through,
// so no caller can ship without it.
const authTokenEnv = "EVS_SANDBOX_FIRECRACKER_AGENT_AUTH_TOKEN"

// resolveAuthToken returns the configured token, falling back to the
// environment. Whitespace is trimmed because the value usually arrives
// from a Secret file that ends in a newline.
func resolveAuthToken(configured string) string {
	if v := strings.TrimSpace(configured); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv(authTokenEnv))
}

// bearerToken attaches the shared secret to every RPC. It does not
// require transport security: the gateway dials agents over plain gRPC
// inside the cluster today, and the token is what authenticates the
// caller either way (mTLS, when configured, stacks on top).
type bearerToken string

func (b bearerToken) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + string(b)}, nil
}

func (bearerToken) RequireTransportSecurity() bool { return false }
