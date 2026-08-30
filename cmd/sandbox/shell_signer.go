package sandbox

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// shellAuthParams holds the query parameters for authenticated WebSocket shell connections.
type shellAuthParams struct {
	Timestamp   string // unix timestamp (seconds)
	Signature   string // base64-encoded SSH signature blob
	Fingerprint string // SHA256 fingerprint of the signing key
	Algorithm   string // key algorithm (e.g. "ssh-ed25519")
}

// signShellAuth signs a shell authentication challenge using the user's SSH key.
//
// Key loading priority:
//  1. keyPath (explicit --identity-file)
//  2. SSH agent (SSH_AUTH_SOCK)
//  3. Default key files: ~/.ssh/id_ed25519, ~/.ssh/id_ecdsa, ~/.ssh/id_rsa
//
// The signed message is: "{unix_timestamp}:{sandboxID}"
func signShellAuth(sandboxID, keyPath string) (*shellAuthParams, error) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	message := []byte(ts + ":" + sandboxID)

	// Try explicit key path first
	if keyPath != "" {
		signer, err := loadSignerFromFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load SSH key from %s: %w", keyPath, err)
		}
		return signMessage(signer, message, ts)
	}

	// Try SSH agent
	if params, err := signWithAgent(message, ts); err == nil {
		return params, nil
	}

	// Try default key files
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("no SSH key found: cannot determine home directory: %w", err)
	}

	defaultKeys := []string{
		filepath.Join(home, ".ssh", "id_ed25519"),
		filepath.Join(home, ".ssh", "id_ecdsa"),
		filepath.Join(home, ".ssh", "id_rsa"),
	}

	for _, path := range defaultKeys {
		signer, err := loadSignerFromFile(path)
		if err != nil {
			continue
		}
		return signMessage(signer, message, ts)
	}

	return nil, fmt.Errorf("no SSH key found. Add an SSH key with `mf ssh-key add` or specify `--identity-file`")
}

// loadSignerFromFile reads a private key file and returns an ssh.Signer.
func loadSignerFromFile(path string) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key %s: %w", path, err)
	}
	return signer, nil
}

// signWithAgent tries to sign the message using keys from the SSH agent.
func signWithAgent(message []byte, ts string) (*shellAuthParams, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, fmt.Errorf("SSH_AUTH_SOCK not set")
	}

	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to SSH agent: %w", err)
	}
	defer conn.Close()

	ag := agent.NewClient(conn)
	keys, err := ag.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list agent keys: %w", err)
	}

	if len(keys) == 0 {
		return nil, fmt.Errorf("no keys in SSH agent")
	}

	// Try each agent key until one succeeds
	for _, key := range keys {
		sig, err := ag.Sign(key, message)
		if err != nil {
			continue
		}

		sigBytes := ssh.Marshal(sig)
		return &shellAuthParams{
			Timestamp:   ts,
			Signature:   base64.StdEncoding.EncodeToString(sigBytes),
			Fingerprint: ssh.FingerprintSHA256(key),
			Algorithm:   key.Type(),
		}, nil
	}

	return nil, fmt.Errorf("no agent key could sign the message")
}

// signMessage signs a message with the given signer and returns auth params.
func signMessage(signer ssh.Signer, message []byte, ts string) (*shellAuthParams, error) {
	sig, err := signer.Sign(rand.Reader, message)
	if err != nil {
		return nil, fmt.Errorf("failed to sign shell auth message: %w", err)
	}

	sigBytes := ssh.Marshal(sig)
	pubKey := signer.PublicKey()

	return &shellAuthParams{
		Timestamp:   ts,
		Signature:   base64.StdEncoding.EncodeToString(sigBytes),
		Fingerprint: ssh.FingerprintSHA256(pubKey),
		Algorithm:   pubKey.Type(),
	}, nil
}
