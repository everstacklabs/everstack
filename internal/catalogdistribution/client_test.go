package catalogdistribution

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientFetchesVerifiedRelease(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bundleData, digest, err := BuildBundle(
		"2.4.1",
		completeTestModels("verified-model"),
		completeTestProviders(),
		[]byte("versions: []"),
	)
	if err != nil {
		t.Fatal(err)
	}
	channelData, err := SignChannel(privateKey, Channel{
		Channel:      "stable",
		Version:      "2.4.1",
		BundlePath:   "releases/2.4.1/catalog.bundle.json",
		BundleSHA256: digest,
		BundleSize:   int64(len(bundleData)),
		PublishedAt:  time.Date(2026, 8, 17, 15, 0, 0, 0, time.UTC).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/channels/stable.json":
			_, _ = response.Write(channelData)
		case "/v1/releases/2.4.1/catalog.bundle.json":
			_, _ = response.Write(bundleData)
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(ClientConfig{
		BaseURL:    server.URL + "/v1",
		Channel:    "stable",
		PublicKeys: map[string]ed25519.PublicKey{PublicKeyID(publicKey): publicKey},
	})
	if err != nil {
		t.Fatal(err)
	}

	bundle, err := client.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if bundle.Version != "2.4.1" || !strings.Contains(string(bundle.Models), "verified-model") {
		t.Fatalf("Fetch() = %#v", bundle)
	}

	unsignedClient, err := NewClient(ClientConfig{
		BaseURL: server.URL + "/v1",
		Channel: "stable",
	})
	if unsignedClient != nil || err == nil || !strings.Contains(err.Error(), "verification key") {
		t.Fatalf("unsigned NewClient() = (%v, %v), want missing verification key", unsignedClient, err)
	}
}

func TestClientRejectsBundleTampering(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bundleData, digest, err := BuildBundle("2.4.1", completeTestModels("original-model"), completeTestProviders(), nil)
	if err != nil {
		t.Fatal(err)
	}
	channelData, err := SignChannel(privateKey, Channel{
		Channel:      "stable",
		Version:      "2.4.1",
		BundlePath:   "releases/2.4.1/catalog.bundle.json",
		BundleSHA256: digest,
		BundleSize:   int64(len(bundleData)),
		PublishedAt:  time.Date(2026, 8, 17, 15, 0, 0, 0, time.UTC).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), bundleData...)
	tampered[len(tampered)-1] ^= 1

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if strings.Contains(request.URL.Path, "/channels/") {
			_, _ = response.Write(channelData)
			return
		}
		_, _ = response.Write(tampered)
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(ClientConfig{
		BaseURL:    server.URL + "/v1",
		Channel:    "stable",
		PublicKeys: map[string]ed25519.PublicKey{PublicKeyID(publicKey): publicKey},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Fetch(context.Background()); err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("Fetch() error = %v, want SHA-256 mismatch", err)
	}
}

func completeTestModels(modelName string) []byte {
	return []byte("providers:\n  example:\n    models:\n      - name: " + modelName + "\n")
}

func completeTestProviders() []byte {
	return []byte("providers:\n  example:\n    display_name: Example\n")
}

func TestVerifyChannelRejectsPointerTampering(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	channelData, err := SignChannel(privateKey, Channel{
		Channel:      "stable",
		Version:      "2.4.1",
		BundlePath:   "releases/2.4.1/catalog.bundle.json",
		BundleSHA256: strings.Repeat("a", 64),
		BundleSize:   100,
		PublishedAt:  time.Date(2026, 8, 17, 15, 0, 0, 0, time.UTC).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}

	var channel map[string]any
	if err := json.Unmarshal(channelData, &channel); err != nil {
		t.Fatal(err)
	}
	channel["bundle_size"] = float64(101)
	tampered, err := json.Marshal(channel)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := VerifyChannel(map[string]ed25519.PublicKey{PublicKeyID(publicKey): publicKey}, tampered, "stable"); err == nil || !strings.Contains(err.Error(), "invalid catalog channel signature") {
		t.Fatalf("VerifyChannel() error = %v", err)
	}
}

func TestSignChannelRejectsNonVersionedBundlePath(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, err = SignChannel(privateKey, Channel{
		Channel:      "stable",
		Version:      "2.4.1",
		BundlePath:   "../catalog.bundle.json",
		BundleSHA256: strings.Repeat("a", 64),
		BundleSize:   100,
		PublishedAt:  time.Date(2026, 8, 17, 15, 0, 0, 0, time.UTC).Format(time.RFC3339),
	})
	if err == nil {
		t.Fatal("SignChannel() error = nil")
	}
}
