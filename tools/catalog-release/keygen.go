package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/everstacklabs/everstack/internal/catalogdistribution"
)

func generateSigningKey(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("catalog-release keygen", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	privateKeyFile := flags.String("private-key-file", "", "new private key output file")
	publicKeyFile := flags.String("public-key-file", "", "new public key output file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *privateKeyFile == "" || *publicKeyFile == "" {
		return fmt.Errorf("--private-key-file and --public-key-file are required")
	}
	if *privateKeyFile == *publicKeyFile {
		return fmt.Errorf("private and public key paths must differ")
	}
	if err := ensureFileDoesNotExist(*privateKeyFile); err != nil {
		return err
	}
	if err := ensureFileDoesNotExist(*publicKeyFile); err != nil {
		return err
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate Ed25519 catalog signing key: %w", err)
	}
	privateData := []byte(base64.StdEncoding.EncodeToString(privateKey) + "\n")
	publicData := []byte(base64.StdEncoding.EncodeToString(publicKey) + "\n")
	if err := writeNewSecretFile(*privateKeyFile, privateData); err != nil {
		return fmt.Errorf("write private signing key: %w", err)
	}
	if err := writeNewPublicFile(*publicKeyFile, publicData); err != nil {
		_ = os.Remove(*privateKeyFile)
		return fmt.Errorf("write public verification key: %w", err)
	}

	_, _ = fmt.Fprintf(
		output,
		"catalog signing key generated\nprivate key file: %s\npublic key file: %s\npublic key ID: %s\n",
		*privateKeyFile,
		*publicKeyFile,
		catalogdistribution.PublicKeyID(publicKey),
	)
	return nil
}

func ensureFileDoesNotExist(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("refusing to overwrite existing key file %q", path)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect key file %q: %w", path, err)
	}
	return nil
}

func writeNewSecretFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func writeNewPublicFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}
