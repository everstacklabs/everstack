package main

import (
	"bytes"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateSigningKeyWritesRawBase64KeyPairWithoutPrintingSecrets(t *testing.T) {
	directory := t.TempDir()
	privatePath := filepath.Join(directory, "private.key")
	publicPath := filepath.Join(directory, "public.key")
	var output bytes.Buffer

	if err := generateSigningKey([]string{
		"--private-key-file", privatePath,
		"--public-key-file", publicPath,
	}, &output); err != nil {
		t.Fatalf("generateSigningKey() error = %v", err)
	}
	privateData, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	publicData, err := os.ReadFile(publicPath)
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(privateData)))
	if err != nil || len(privateKey) != 64 {
		t.Fatalf("private key length = %d, error = %v", len(privateKey), err)
	}
	publicKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(publicData)))
	if err != nil || len(publicKey) != 32 {
		t.Fatalf("public key length = %d, error = %v", len(publicKey), err)
	}
	if bytes.Contains(output.Bytes(), bytes.TrimSpace(privateData)) || bytes.Contains(output.Bytes(), bytes.TrimSpace(publicData)) {
		t.Fatal("key material was printed")
	}
	privateInfo, err := os.Stat(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	if privateInfo.Mode().Perm() != 0o600 {
		t.Fatalf("private key permissions = %o, want 600", privateInfo.Mode().Perm())
	}
}

func TestGenerateSigningKeyRefusesOverwrite(t *testing.T) {
	directory := t.TempDir()
	privatePath := filepath.Join(directory, "private.key")
	publicPath := filepath.Join(directory, "public.key")
	if err := os.WriteFile(privatePath, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := generateSigningKey([]string{
		"--private-key-file", privatePath,
		"--public-key-file", publicPath,
	}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("generateSigningKey() error = %v", err)
	}
	data, readErr := os.ReadFile(privatePath)
	if readErr != nil || string(data) != "keep" {
		t.Fatalf("existing private key changed: data = %q, error = %v", data, readErr)
	}
}

func TestGenerateSigningKeyRequiresExplicitPaths(t *testing.T) {
	if err := generateSigningKey(nil, io.Discard); err == nil || !strings.Contains(err.Error(), "are required") {
		t.Fatalf("generateSigningKey() error = %v", err)
	}
}
