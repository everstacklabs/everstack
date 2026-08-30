package features

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
)

// VerifyManifest verifies the signature on a global manifest and returns it.
// publicKeys is a map of key ID -> public key for key rotation support.
func VerifyManifest(publicKeys map[string]ed25519.PublicKey, data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("unmarshal manifest: %w", err)
	}

	pub, ok := publicKeys[m.PublicKeyID]
	if !ok {
		return nil, fmt.Errorf("unknown public key ID: %s", m.PublicKeyID)
	}

	payload := signedPayload{
		Version:       m.Version,
		SchemaVersion: m.SchemaVersion,
		GeneratedAt:   m.GeneratedAt,
		Features:      m.Features,
	}

	canonical, err := canonicalJSON(payload)
	if err != nil {
		return nil, fmt.Errorf("canonical json: %w", err)
	}

	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}

	if !ed25519.Verify(pub, canonical, sig) {
		return nil, fmt.Errorf("invalid manifest signature")
	}

	return &m, nil
}

// VerifyTenantOverlay verifies the signature on a tenant overlay and returns it.
func VerifyTenantOverlay(publicKeys map[string]ed25519.PublicKey, data []byte) (*TenantOverlay, error) {
	var overlay TenantOverlay
	if err := json.Unmarshal(data, &overlay); err != nil {
		return nil, fmt.Errorf("unmarshal overlay: %w", err)
	}

	pub, ok := publicKeys[overlay.PublicKeyID]
	if !ok {
		return nil, fmt.Errorf("unknown public key ID: %s", overlay.PublicKeyID)
	}

	payload := signedPayload{
		GeneratedAt: overlay.GeneratedAt,
		TenantID:    overlay.TenantID,
		Overrides:   overlay.Overrides,
	}

	canonical, err := canonicalJSON(payload)
	if err != nil {
		return nil, fmt.Errorf("canonical json: %w", err)
	}

	sig, err := base64.StdEncoding.DecodeString(overlay.Signature)
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}

	if !ed25519.Verify(pub, canonical, sig) {
		return nil, fmt.Errorf("invalid overlay signature")
	}

	return &overlay, nil
}

// canonicalJSON produces deterministic JSON with sorted keys
func canonicalJSON(v interface{}) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	var normalized interface{}
	if err := json.Unmarshal(data, &normalized); err != nil {
		return nil, err
	}

	return marshalSorted(normalized)
}

func marshalSorted(v interface{}) ([]byte, error) {
	switch val := v.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		buf := []byte("{")
		for i, k := range keys {
			if i > 0 {
				buf = append(buf, ',')
			}
			keyJSON, err := json.Marshal(k)
			if err != nil {
				return nil, err
			}
			valJSON, err := marshalSorted(val[k])
			if err != nil {
				return nil, err
			}
			buf = append(buf, keyJSON...)
			buf = append(buf, ':')
			buf = append(buf, valJSON...)
		}
		buf = append(buf, '}')
		return buf, nil

	case []interface{}:
		buf := []byte("[")
		for i, item := range val {
			if i > 0 {
				buf = append(buf, ',')
			}
			itemJSON, err := marshalSorted(item)
			if err != nil {
				return nil, err
			}
			buf = append(buf, itemJSON...)
		}
		buf = append(buf, ']')
		return buf, nil

	default:
		return json.Marshal(v)
	}
}
