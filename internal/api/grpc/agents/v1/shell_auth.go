package v1

import (
	"context"
	"encoding/base64"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

const (
	// shellAuthTimestampWindow is the maximum age of a signed shell auth request.
	// Prevents replay attacks while allowing reasonable clock skew.
	shellAuthTimestampWindow = 60 * time.Second
)

// authenticateShellRequest checks whether the HTTP request is authorized
// to open a WebSocket shell to the given sandbox.
//
// Two auth paths are supported:
//  1. An already-authenticated caller that owns the sandbox. Upstream auth (the
//     API-key middleware for CLI/SDK callers, the session cookie for the admin
//     UI) puts a tenant on the request context; the sandbox's tenant must match.
//  2. SSH key signature (CLI): query params ts, sig, fp, alg carry a signed
//     message verified against the user's registered SSH public key.
//
// There used to be a third path: any request carrying `Sec-Fetch-Site:
// same-origin`, a matching Origin/Referer, or a WebSocket Origin on the same
// host was authorized outright. The comment claimed those headers "cannot be
// forged by cross-origin JavaScript", which is true and irrelevant: the threat
// is not a browser. Any HTTP client sets them freely.
//
// That made this an unauthenticated cross-tenant remote shell. The handlers
// resolve the sandbox from a caller-supplied id with no tenant scoping
// (GetBySandboxIDOrName is "first match wins" across every tenant in the shared
// gateway process), so one forged header plus a sandbox id opened an
// interactive shell inside another tenant's sandbox.
func (s *Server) authenticateShellRequest(r *http.Request, sandboxID, tenantID string) error {
	// Path 1: authenticated caller who owns this sandbox.
	if callerTenant, err := s.resolveTenantID(r.Context(), ""); err == nil && callerTenant != "" {
		if callerTenant == tenantID {
			return nil
		}
		// Mirror requireSandboxOwnershipHTTP: do not confirm that another
		// tenant's sandbox exists.
		logger.WithFields(
			"sandbox_id", sandboxID,
			"caller_tenant", callerTenant,
			"sandbox_tenant", tenantID,
		).Warn("shell auth: cross-tenant attempt refused")
		return fmt.Errorf("sandbox not found")
	}

	// Path 2: SSH key signature (CLI), which carries its own proof of identity
	// and is verified against the sandbox's tenant.
	return s.verifySSHSignature(r, sandboxID, tenantID)
}

// verifySSHSignature validates the SSH key signature sent as query parameters.
//
// Expected query params:
//   - ts:  unix timestamp (seconds)
//   - sig: base64-encoded SSH signature blob
//   - fp:  SHA256 fingerprint of the signing key (e.g. "SHA256:abc...")
//   - alg: key algorithm (e.g. "ssh-ed25519", "ssh-rsa", "ecdsa-sha2-nistp256")
//
// The signed message is: "{ts}:{sandboxID}"
func (s *Server) verifySSHSignature(r *http.Request, sandboxID, tenantID string) error {
	if s.sshKeyStore == nil {
		return fmt.Errorf("SSH key authentication is not configured; ensure SSH is enabled in server config")
	}

	q := r.URL.Query()
	tsStr := q.Get("ts")
	sigB64 := q.Get("sig")
	fp := q.Get("fp")

	if tsStr == "" || sigB64 == "" || fp == "" {
		return fmt.Errorf("missing shell auth parameters; use --identity-file or add an SSH key with `mf sandbox ssh-keys add`")
	}

	// Validate timestamp is within the allowed window.
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid timestamp: %w", err)
	}
	now := time.Now().Unix()
	if math.Abs(float64(now-ts)) > shellAuthTimestampWindow.Seconds() {
		return fmt.Errorf("shell auth timestamp expired (clock skew > %s)", shellAuthTimestampWindow)
	}

	// Decode signature blob.
	sigBytes, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		// Try URL-safe base64
		sigBytes, err = base64.URLEncoding.DecodeString(sigB64)
		if err != nil {
			return fmt.Errorf("invalid signature encoding: %w", err)
		}
	}

	// Look up the public key by fingerprint within the tenant.
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	sshKey, err := s.sshKeyStore.LookupKeyByFingerprint(ctx, tenantID, fp)
	if err != nil {
		logger.WithFields("fingerprint", fp, "tenant_id", tenantID, "error", err.Error()).
			Warn("shell_auth: key lookup failed")
		return fmt.Errorf("SSH key not registered; add your public key with `mf sandbox ssh-keys add`")
	}

	// Parse the stored public key.
	pubKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(sshKey.PublicKey))
	if err != nil {
		logger.WithFields("key_id", sshKey.ID, "error", err.Error()).
			Warn("shell_auth: failed to parse stored public key")
		return fmt.Errorf("SSH key not found or not authorized")
	}

	// Reconstruct the signed message.
	message := []byte(tsStr + ":" + sandboxID)

	// Parse and verify the SSH signature.
	sig := &ssh.Signature{}
	if err := ssh.Unmarshal(sigBytes, sig); err != nil {
		return fmt.Errorf("invalid SSH signature format: %w", err)
	}

	if err := pubKey.Verify(message, sig); err != nil {
		logger.WithFields("fingerprint", fp, "sandbox_id", sandboxID).
			Warn("shell_auth: signature verification failed")
		return fmt.Errorf("SSH signature verification failed")
	}

	// Auth passed: the user holds a registered key in this tenant and the
	// signature is cryptographically valid. This is sufficient for shell access.
	// The SSH proxy also enforces per-sandbox ACLs for direct SSH, but for
	// WebSocket shells the tenant-scoped key registration is the access control.

	// Update last-used timestamp (background, non-blocking).
	go s.sshKeyStore.TouchKeyLastUsed(context.Background(), sshKey.ID)

	return nil
}
