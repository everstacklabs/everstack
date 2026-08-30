package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	clicfg "github.com/everstacklabs/everstack/internal/cli/config"
	"github.com/everstacklabs/everstack/internal/cli/credentials"
)

func TestLogoutLocalDoesNotContactServer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	requestReceived := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requestReceived = true
	}))
	t.Cleanup(server.Close)

	cfg := &clicfg.Config{
		ActiveContext: "default",
		Contexts: map[string]clicfg.Context{
			"default": {APIURL: server.URL},
		},
	}
	if err := clicfg.Save(cfg); err != nil {
		t.Fatalf("save CLI config: %v", err)
	}
	if err := credentials.Save("default", credentials.Token{
		AccessToken:  "saved-token",
		RefreshToken: "saved-refresh-token",
	}); err != nil {
		t.Fatalf("save CLI credentials: %v", err)
	}

	cmd := New()
	cmd.SetArgs([]string{"logout", "--local"})
	cmd.SilenceUsage = true
	if err := cmd.Execute(); err != nil {
		t.Fatalf("local logout: %v", err)
	}
	if requestReceived {
		t.Fatal("local logout contacted the server")
	}
	token, err := credentials.Load("default")
	if err != nil {
		t.Fatalf("load credentials: %v", err)
	}
	if !token.IsEmpty() {
		t.Fatalf("local credential still present: %#v", token)
	}
}
