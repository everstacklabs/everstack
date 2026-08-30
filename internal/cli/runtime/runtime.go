package runtime

import (
	"errors"
	"os"
	"strings"

	cliclient "github.com/everstacklabs/everstack/internal/cli/client"
	clicfg "github.com/everstacklabs/everstack/internal/cli/config"
	"github.com/everstacklabs/everstack/internal/cli/credentials"
)

var (
	ErrNotLoggedIn = errors.New("not logged in; run `evs login` or provide EVS_API_KEY")
	ErrNoAPIURL    = errors.New("no API endpoint is configured; run `evs login --api-url <instance-url>` or provide EVS_API_URL")
)

// Overrides are per-invocation connection settings supplied by a command.
// Empty values defer to environment variables, then the active CLI context.
type Overrides struct {
	APIURL   string
	APIKey   string
	TenantID string
}

// Session is the resolved connection and authentication state for one CLI
// invocation.
type Session struct {
	ContextName       string
	APIURL            string
	AccessToken       string
	AccessTokenSource cliclient.AccessTokenSource
	APIKey            string
	OrgID             string
	TenantID          string
}

// Resolve combines command overrides, environment variables, the active
// context, and its stored login credential.
func Resolve(overrides Overrides) (Session, error) {
	overrideAPIURL := firstNonEmpty(overrides.APIURL, os.Getenv("EVS_API_URL"))
	overrideAPIKey := firstNonEmpty(overrides.APIKey, os.Getenv("EVS_API_KEY"))
	overrideTenantID := firstNonEmpty(overrides.TenantID, os.Getenv("EVS_TENANT_ID"))

	// A complete command/environment override is self-contained. Do not make
	// CI depend on the health of an unrelated local profile file.
	if overrideAPIURL != "" && overrideAPIKey != "" {
		return Session{
			ContextName: "override",
			APIURL:      strings.TrimRight(overrideAPIURL, "/"),
			APIKey:      overrideAPIKey,
			TenantID:    overrideTenantID,
		}, nil
	}

	cfg, err := clicfg.Load()
	if err != nil {
		return Session{}, err
	}

	active := cfg.ActiveCtx()
	resolved := Session{
		ContextName: cfg.ActiveContext,
		APIURL: firstNonEmpty(
			overrideAPIURL,
			active.APIURL,
		),
		APIKey:   overrideAPIKey,
		TenantID: overrideTenantID,
	}

	refreshable := false
	if resolved.APIKey == "" {
		token, err := credentials.Load(cfg.ActiveContext)
		if err != nil {
			return Session{}, err
		}
		resolved.AccessToken = token.AccessToken
		refreshable = token.RefreshToken != ""
		resolved.APIKey = token.APIKey
		resolved.OrgID = token.OrgID
	}
	if resolved.AccessToken == "" && resolved.APIKey == "" {
		return Session{}, ErrNotLoggedIn
	}
	if resolved.APIURL == "" {
		return Session{}, ErrNoAPIURL
	}

	resolved.APIURL = strings.TrimRight(resolved.APIURL, "/")
	if resolved.AccessToken != "" && refreshable {
		resolved.AccessTokenSource = credentials.NewSource(cfg.ActiveContext, resolved.APIURL, nil)
	}
	return resolved, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
