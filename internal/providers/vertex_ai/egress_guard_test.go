package vertex_ai

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/everstacklabs/everstack/internal/lib/outboundguard"
	"github.com/everstacklabs/everstack/internal/providers/httpclient"
)

func internalServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("INTERNAL-SECRET"))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func externalAccountJSON(extra string, tokenURL string) string {
	return fmt.Sprintf(`{"type":"external_account","audience":"a","subject_token_type":"urn:ietf:params:oauth:token-type:jwt","token_url":%q,"credential_source":{%s}}`, tokenURL, extra)
}

// external_account credentials tell the library to read a URL or file and post
// the contents to a token URL the tenant chose.
func TestManagedGatewayRefusesExternalAccountCredentials(t *testing.T) {
	internal, internalHits := internalServer(t)
	collector, collectorHits := internalServer(t)
	secret := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(secret, []byte("K8S-SA-TOKEN"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer httpclient.SetPublicEndpointsForTest(true)()

	for name, source := range map[string]string{
		"url":  fmt.Sprintf(`"url":%q`, internal.URL),
		"file": fmt.Sprintf(`"file":%q`, secret),
	} {
		_, err := buildTokenSource(externalAccountJSON(source, collector.URL))
		if err == nil {
			t.Fatalf("%s: external_account credential was accepted", name)
		}
	}
	if internalHits.Load() != 0 || collectorHits.Load() != 0 {
		t.Fatalf("requests reached targets: internal=%d collector=%d", internalHits.Load(), collectorHits.Load())
	}
}

func TestManagedGatewayRefusesOtherNonServiceAccountShapes(t *testing.T) {
	defer httpclient.SetPublicEndpointsForTest(true)()

	for name, creds := range map[string]string{
		"authorized_user": `{"type":"authorized_user","client_id":"a","client_secret":"b","refresh_token":"c"}`,
		"impersonated":    `{"type":"impersonated_service_account","service_account_impersonation_url":"http://127.0.0.1/","source_credentials":{}}`,
		"no type":         `{"client_email":"a"}`,
		"duplicate key":   `{"type":"service_account","Type":"external_account","token_url":"http://127.0.0.1/","credential_source":{"file":"/etc/passwd"},"audience":"a","subject_token_type":"b"}`,
		"empty":           ``,
	} {
		if _, err := buildTokenSource(creds); err == nil {
			t.Fatalf("%s: credential was accepted", name)
		}
	}
}

// token_uri in a service account key is tenant-chosen, and its reply is
// reflected in the error text, so it has to go through the guard.
func TestServiceAccountTokenURICannotReachInternalAddress(t *testing.T) {
	internal, hits := internalServer(t)
	defer httpclient.SetPublicEndpointsForTest(true)()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	creds, err := json.Marshal(map[string]string{
		"type": "service_account", "client_email": "a@b.iam.gserviceaccount.com",
		"private_key": string(keyPEM), "private_key_id": "k", "token_uri": internal.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	ts, err := buildTokenSource(string(creds))
	if err != nil {
		t.Fatalf("service account key refused: %v", err)
	}
	_, err = ts.Token()
	// oauth2 flattens the cause into its message, so errors.Is cannot see it.
	if err == nil || !strings.Contains(err.Error(), outboundguard.ErrBlocked.Error()) {
		t.Fatalf("Token = %v, want the guard's refusal", err)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("internal server received %d requests", n)
	}
}

func TestManagedGatewayStillAcceptsStaticAccessToken(t *testing.T) {
	defer httpclient.SetPublicEndpointsForTest(true)()
	ts, err := buildTokenSource("ya29.token")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := ts.Token()
	if err != nil || tok.AccessToken != "ya29.token" {
		t.Fatalf("token = %v, %v", tok, err)
	}
}

// Self-hosted gateways keep every credential type Google supports.
func TestSelfHostedKeepsExternalAccountCredentials(t *testing.T) {
	defer httpclient.SetPublicEndpointsForTest(false)()
	if _, err := buildTokenSource(externalAccountJSON(`"url":"https://sts.example/token"`, "https://sts.example/exchange")); err != nil {
		t.Fatalf("external_account refused on self-hosted: %v", err)
	}
}
