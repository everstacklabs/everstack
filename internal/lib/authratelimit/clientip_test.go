package authratelimit

import (
	"net/http"
	"testing"
)

func request(xff, remoteAddr string) *http.Request {
	r := &http.Request{Header: http.Header{}, RemoteAddr: remoteAddr}
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

const (
	client     = "203.0.113.7"
	cloudflare = "173.245.48.9"
	ingress    = "10.42.0.5"
	peer       = "10.42.0.5:54321"
)

// The limiter is only as good as the identity it charges. These tests exist
// because the obvious reading of X-Forwarded-For, taking the left-most entry,
// hands the caller control of that identity: nginx appends rather than
// replaces, so anything the caller sends arrives on the left.
func TestClientIPIgnoresAForgedPrefix(t *testing.T) {
	proxies := parseTrustedProxies("173.245.48.0/20,10.42.0.0/16")

	// A caller trying to look like someone else, or like a new caller on
	// every request, gets charged to their own address regardless.
	got := clientIPFrom(request("1.2.3.4, "+client+", "+cloudflare+", "+ingress, peer), proxies)
	if got != client {
		t.Errorf("clientIP = %q, want %q (the first hop we did not add)", got, client)
	}
}

func TestClientIPWalksPastEveryTrustedHop(t *testing.T) {
	proxies := parseTrustedProxies("173.245.48.0/20,10.42.0.0/16")

	got := clientIPFrom(request(client+", "+cloudflare+", "+ingress, peer), proxies)
	if got != client {
		t.Errorf("clientIP = %q, want %q", got, client)
	}
}

// With nothing declared trustworthy, the header is not evidence of anything,
// so the socket peer is used instead. Coarse, but it cannot be forged, and
// over-limiting is recoverable in a way that a bypass is not.
func TestClientIPFallsBackToPeerWhenNoProxiesAreTrusted(t *testing.T) {
	got := clientIPFrom(request("1.2.3.4", peer), nil)
	if got != ingress {
		t.Errorf("clientIP = %q, want the socket peer %q", got, ingress)
	}
}

func TestClientIPUsesPeerWhenHeaderAbsent(t *testing.T) {
	proxies := parseTrustedProxies("10.42.0.0/16")

	got := clientIPFrom(request("", peer), proxies)
	if got != ingress {
		t.Errorf("clientIP = %q, want %q", got, ingress)
	}
}

func TestParseTrustedProxiesAcceptsBareAddressesAndSkipsJunk(t *testing.T) {
	proxies := parseTrustedProxies(" 10.42.0.0/16 , 198.51.100.4 , not-an-ip , ")
	if len(proxies) != 2 {
		t.Fatalf("parsed %d proxies, want 2", len(proxies))
	}

	// The bare address must match itself and nothing adjacent.
	got := clientIPFrom(request(client+", 198.51.100.4", peer), proxies)
	if got != client {
		t.Errorf("clientIP = %q, want %q", got, client)
	}
	got = clientIPFrom(request(client+", 198.51.100.5", peer), proxies)
	if got != "198.51.100.5" {
		t.Errorf("clientIP = %q, want the untrusted neighbour 198.51.100.5", got)
	}
}
