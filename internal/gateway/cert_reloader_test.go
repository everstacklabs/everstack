package gateway

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCertReloader_HotReload verifies the reloader picks up a new
// keypair after the on-disk files are rewritten — the exact scenario
// cert-manager triggers on Secret rotation.
func TestCertReloader_HotReload(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")

	writeSelfSignedKeypair(t, certPath, keyPath, "before.example.com")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r, err := newCertReloader(ctx, certPath, keyPath)
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}

	got, err := r.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if name := parsedCN(t, got); name != "before.example.com" {
		t.Fatalf("initial CN = %q, want before.example.com", name)
	}

	// Rotate on disk; reload directly so we don't wait 60s.
	writeSelfSignedKeypair(t, certPath, keyPath, "after.example.com")
	if err := os.Chtimes(certPath, time.Now().Add(time.Second), time.Now().Add(time.Second)); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if err := r.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	got, err = r.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate after reload: %v", err)
	}
	if name := parsedCN(t, got); name != "after.example.com" {
		t.Fatalf("post-reload CN = %q, want after.example.com", name)
	}
}

// TestCertReloader_BadInitial returns an error so the gateway can refuse
// to start instead of accepting connections it can't TLS-terminate.
func TestCertReloader_BadInitial(t *testing.T) {
	dir := t.TempDir()
	if _, err := newCertReloader(context.Background(), filepath.Join(dir, "missing.crt"), filepath.Join(dir, "missing.key")); err == nil {
		t.Fatal("expected error on missing files")
	}
}

func writeSelfSignedKeypair(t *testing.T, certPath, keyPath, cn string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{cn},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey: %v", err)
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
}

func parsedCN(t *testing.T, c *tls.Certificate) string {
	t.Helper()
	parsed, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return parsed.Subject.CommonName
}
