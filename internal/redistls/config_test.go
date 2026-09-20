package redistls

import (
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

func TestLoadDisabled(t *testing.T) {
	config, err := Load(Config{})
	if err != nil || config != nil {
		t.Fatalf("Load() config=%#v err=%v, want nil, nil", config, err)
	}
	if _, err := Load(Config{CAFile: "/tmp/ca.crt"}); err == nil {
		t.Fatal("disabled TLS unexpectedly accepted trust material")
	}
}

func TestLoadVerifiedConfig(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test redis CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	caFile := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}

	config, err := Load(Config{Enabled: true, CAFile: caFile, ServerName: "ha-data-redis"})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if config.MinVersion != tls.VersionTLS12 || config.ServerName != "ha-data-redis" || config.RootCAs == nil {
		t.Fatalf("Load() = %#v", config)
	}
}

func TestLoadRejectsIncompleteOrInvalidTrust(t *testing.T) {
	if _, err := Load(Config{Enabled: true, ServerName: "ha-data-redis"}); err == nil {
		t.Fatal("TLS unexpectedly accepted a missing CA file")
	}
	caFile := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(caFile, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(Config{Enabled: true, CAFile: caFile, ServerName: "ha-data-redis"}); err == nil {
		t.Fatal("TLS unexpectedly accepted an invalid CA file")
	}
}
