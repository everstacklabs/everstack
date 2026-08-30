package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/catalogdistribution"
)

const (
	signingKeyEnvironmentVariable     = "EVS_CATALOG_SIGNING_PRIVATE_KEY"
	signingKeyFileEnvironmentVariable = "EVS_CATALOG_SIGNING_PRIVATE_KEY_FILE"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Getenv, time.Now, os.Stdout, publishReleaseToR2); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type publishReleaseFunc func(context.Context, func(string) string, releaseArtifacts) error

func run(
	ctx context.Context,
	args []string,
	getenv func(string) string,
	now func() time.Time,
	output io.Writer,
	publishRelease publishReleaseFunc,
) error {
	if len(args) == 0 {
		return fmt.Errorf("catalog release command is required: validate, build, publish, or keygen")
	}
	command := args[0]
	args = args[1:]
	switch command {
	case "keygen":
		return generateSigningKey(args, output)
	case "validate":
		flags := flag.NewFlagSet("catalog-release validate", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		catalogDir := flags.String("catalog-dir", "model-catalog", "catalog source directory")
		if err := flags.Parse(args); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("unexpected validate arguments: %s", strings.Join(flags.Args(), " "))
		}
		source, err := loadCatalogSource(*catalogDir)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(output, "catalog %s validated\n", source.version)
		return nil
	case "build", "publish":
		// Both commands build the same signed artifacts below.
	default:
		return fmt.Errorf("unknown command %q: expected validate, build, publish, or keygen", command)
	}
	publishCommand := command == "publish"

	flags := flag.NewFlagSet("catalog-release "+command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	catalogDir := flags.String("catalog-dir", "model-catalog", "catalog source directory")
	outDir := flags.String("out", "model-catalog/dist/v1", "release output directory")
	channelName := flags.String("channel", "stable", "channel to promote")
	publicURL := flags.String("public-url", "", "public distribution base URL to verify after promotion")
	verifyTimeout := flags.Duration("verify-timeout", 2*time.Minute, "maximum time to wait for public distribution verification")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected %s arguments: %s", command, strings.Join(flags.Args(), " "))
	}
	if publishCommand {
		if *publicURL == "" {
			return fmt.Errorf("--public-url is required for the publish command")
		}
		if *verifyTimeout <= 0 {
			return fmt.Errorf("--verify-timeout must be positive")
		}
	}

	privateKey, err := loadPrivateKey(
		getenv(signingKeyEnvironmentVariable),
		getenv(signingKeyFileEnvironmentVariable),
	)
	if err != nil {
		return err
	}
	source, err := loadCatalogSource(*catalogDir)
	if err != nil {
		return err
	}

	bundle, digest, err := catalogdistribution.BuildBundle(source.version, source.models, source.providers, source.changelog)
	if err != nil {
		return err
	}
	bundlePath := filepath.ToSlash(filepath.Join("releases", source.version, "catalog.bundle.json"))
	channel, err := catalogdistribution.SignChannel(privateKey, catalogdistribution.Channel{
		Channel:      *channelName,
		Version:      source.version,
		BundlePath:   bundlePath,
		BundleSHA256: digest,
		BundleSize:   int64(len(bundle)),
		PublishedAt:  now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}

	bundleOutput := filepath.Join(*outDir, filepath.FromSlash(bundlePath))
	channelOutput := filepath.Join(*outDir, "channels", *channelName+".json")
	if err := writeFileAtomically(bundleOutput, bundle); err != nil {
		return fmt.Errorf("write catalog bundle: %w", err)
	}
	if err := writeFileAtomically(channelOutput, channel); err != nil {
		return fmt.Errorf("write catalog channel: %w", err)
	}
	if publishCommand {
		if publishRelease == nil {
			return fmt.Errorf("catalog release publisher is not configured")
		}
		if err := publishRelease(ctx, getenv, releaseArtifacts{
			Version:     source.version,
			ChannelName: *channelName,
			BundlePath:  bundlePath,
			Bundle:      bundle,
			Channel:     channel,
		}); err != nil {
			return fmt.Errorf("publish catalog release: %w", err)
		}
		publicKey := privateKey.Public().(ed25519.PublicKey)
		if err := verifyPublicRelease(ctx, *publicURL, *channelName, source.version, publicKey, *verifyTimeout); err != nil {
			return err
		}
	}

	_, _ = fmt.Fprintf(output, "catalog release %s built\nbundle: %s\nchannel: %s\n", source.version, bundleOutput, channelOutput)
	if publishCommand {
		_, _ = fmt.Fprintln(output, "published: R2")
		_, _ = fmt.Fprintf(output, "verified: %s channel %s version %s\n", strings.TrimRight(*publicURL, "/"), *channelName, source.version)
	}
	return nil
}

func loadPrivateKey(encoded, filePath string) (ed25519.PrivateKey, error) {
	encoded = strings.TrimSpace(encoded)
	filePath = strings.TrimSpace(filePath)
	if encoded == "" && filePath == "" {
		return nil, fmt.Errorf(
			"%s or %s is required",
			signingKeyEnvironmentVariable,
			signingKeyFileEnvironmentVariable,
		)
	}
	if encoded == "" {
		info, err := os.Stat(filePath)
		if err != nil {
			return nil, fmt.Errorf("inspect %s: %w", signingKeyFileEnvironmentVariable, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s must reference a regular file", signingKeyFileEnvironmentVariable)
		}
		if info.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("%s must not be accessible by group or others", signingKeyFileEnvironmentVariable)
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", signingKeyFileEnvironmentVariable, err)
		}
		encoded = strings.TrimSpace(string(data))
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode catalog signing private key: %w", err)
	}
	if len(decoded) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("catalog signing private key must decode to %d bytes", ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(decoded), nil
}

func writeFileAtomically(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporaryPath := path + ".tmp"
	if err := os.WriteFile(temporaryPath, data, 0o644); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
