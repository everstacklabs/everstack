package config_test

import (
	"testing"

	"github.com/everstacklabs/everstack/internal/cli/config"
)

func TestResolveDoesNotInventACloudResourceEndpoint(t *testing.T) {
	t.Setenv("EVS_API_URL", "")

	resolved := config.Resolve(&config.Config{
		ActiveContext: "default",
		Contexts:      map[string]config.Context{},
	}, "", "", "", "", "")

	if resolved.APIURL != "" {
		t.Fatalf("API URL = %q, want no endpoint until the user selects one", resolved.APIURL)
	}
}
