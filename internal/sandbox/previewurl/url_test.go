package previewurl

import "testing"

func TestDirectURLUsesPathForLocalhost(t *testing.T) {
	got := DirectURL(Config{BaseDomain: "localhost", ListenPort: "8443"}, "abc-3000", "sbx-a", 3000)
	want := "http://localhost:8443/_sandbox/sbx-a/port/3000/"
	if got != want {
		t.Fatalf("DirectURL = %q, want %q", got, want)
	}
}

func TestDirectURLUsesSubdomainForRealDomain(t *testing.T) {
	got := DirectURL(Config{BaseDomain: "preview.evs.run", TLSEnabled: true}, "abc-3000", "sbx-a", 3000)
	want := "https://abc-3000.preview.evs.run"
	if got != want {
		t.Fatalf("DirectURL = %q, want %q", got, want)
	}
}

func TestSignedURLBaseAlwaysUsesSubdomain(t *testing.T) {
	got := SignedURLBase(Config{BaseDomain: "localhost", ListenPort: "8443"}, "abc-3000")
	want := "http://abc-3000.localhost:8443"
	if got != want {
		t.Fatalf("SignedURLBase = %q, want %q", got, want)
	}
}

func TestPrivateURLUsesPath(t *testing.T) {
	got := PrivateURL(Config{BaseDomain: "preview.evs.run", TLSEnabled: true}, "sbx-a", 3000)
	want := "https://preview.evs.run/_sandbox/sbx-a/port/3000/"
	if got != want {
		t.Fatalf("PrivateURL = %q, want %q", got, want)
	}
}
