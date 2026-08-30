package auth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/cli/client"
	clicfg "github.com/everstacklabs/everstack/internal/cli/config"
	"github.com/everstacklabs/everstack/internal/cli/credentials"
	"github.com/everstacklabs/everstack/internal/cli/oauthflow"
	authv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/auth/v1"
	"github.com/spf13/cobra"
)

const (
	deviceAuthClientID = "evs-cli"
	deviceAuthScope    = "cli:full"
)

func newLoginCmd() *cobra.Command {
	var apiKey string
	var apiURL string
	var forceDevice bool

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with Everstack",
		Long: `Authenticate with Everstack.

Without flags, opens a browser and uses OAuth Authorization Code + PKCE.
On a headless machine, or with --device, it uses the Device Authorization
Grant and displays a short code instead.

For CI/automation, pass --api-key to skip the browser flow.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := clicfg.Load()
			if err != nil {
				return err
			}

			if apiURL == "" {
				apiURL = os.Getenv("EVS_API_URL")
			}
			if apiURL == "" {
				resolved := clicfg.Resolve(cfg, "", "", "", "", "")
				apiURL = resolved.APIURL
			}
			if apiKey == "" {
				apiKey = os.Getenv("EVS_API_KEY")
			}
			apiURL = strings.TrimRight(strings.TrimSpace(apiURL), "/")
			if apiURL == "" {
				return fmt.Errorf("API URL is required for first login; pass --api-url or set EVS_API_URL")
			}

			if apiKey != "" {
				return loginWithAPIKey(cmd.Context(), cfg, apiURL, apiKey)
			}
			if !forceDevice && browserAuthAvailable() {
				err := loginWithBrowserPKCE(cmd.Context(), cfg, apiURL)
				if err == nil {
					return nil
				}
				if !errors.Is(err, oauthflow.ErrUnavailable) {
					return err
				}
				fmt.Fprintln(os.Stderr, "Browser login is unavailable on this server; using device authorization.")
			}
			return loginWithDeviceAuth(cmd.Context(), cfg, apiURL)
		},
	}

	cmd.Flags().StringVar(&apiKey, "api-key", "", "authenticate with an API key instead of browser (env: EVS_API_KEY)")
	cmd.Flags().StringVar(&apiURL, "api-url", "", "Everstack API URL (env: EVS_API_URL)")
	cmd.Flags().BoolVar(&forceDevice, "device", false, "use the device authorization flow (for headless environments)")
	return cmd
}

func loginWithAPIKey(ctx context.Context, cfg *clicfg.Config, apiURL, apiKey string) error {
	f := client.New(client.Options{APIURL: apiURL, APIKey: apiKey})
	who, err := f.Whoami(ctx)
	if err != nil {
		return fmt.Errorf("could not validate API key: %w", err)
	}

	tok := credentials.Token{
		APIKey:  apiKey,
		OrgID:   who.OrgID,
		OrgSlug: who.OrgSlug,
		UserID:  who.UserID,
		Email:   who.Email,
	}
	if err := saveLoginContext(cfg, apiURL, who.OrgSlug); err != nil {
		return fmt.Errorf("save login context: %w", err)
	}
	if err := credentials.Save(cfg.ActiveContext, tok); err != nil {
		return fmt.Errorf("save credentials: %w", err)
	}

	fmt.Fprintf(os.Stdout, "Logged in as %s (%s)\n", who.Email, who.OrgSlug)
	return nil
}

func loginWithBrowserPKCE(ctx context.Context, cfg *clicfg.Config, apiURL string) error {
	tokens, err := oauthflow.Login(ctx, oauthflow.Options{
		APIURL: apiURL,
		OpenBrowser: func(target string) error {
			fmt.Fprintln(os.Stdout, "Opening Everstack in your browser...")
			return openBrowser(target)
		},
	})
	if err != nil {
		return fmt.Errorf("browser login: %w", err)
	}
	identity, err := client.New(client.Options{
		APIURL:      apiURL,
		AccessToken: tokens.AccessToken,
	}).Whoami(ctx)
	if err != nil {
		return fmt.Errorf("validate browser authorization: %w", err)
	}
	token := credentials.Token{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		ExpiresAt:    tokens.ExpiresAt,
		OrgID:        identity.OrgID,
		OrgSlug:      identity.OrgSlug,
		UserID:       identity.UserID,
		Email:        identity.Email,
	}
	if err := saveLoginContext(cfg, apiURL, identity.OrgSlug); err != nil {
		return fmt.Errorf("save login context: %w", err)
	}
	if err := credentials.Save(cfg.ActiveContext, token); err != nil {
		return fmt.Errorf("save credentials: %w", err)
	}
	fmt.Fprintf(os.Stdout, "Logged in as %s (%s)\n", identity.Email, identity.OrgSlug)
	return nil
}

func loginWithDeviceAuth(ctx context.Context, cfg *clicfg.Config, apiURL string) error {
	// Use an unauthenticated factory just to reach the auth service.
	f := client.New(client.Options{APIURL: apiURL})
	authClient := f.Auth()

	initResp, err := authClient.CreateDeviceAuthorization(ctx, connect.NewRequest(&authv1.CreateDeviceAuthorizationRequest{
		ClientId: deviceAuthClientID,
		Scope:    deviceAuthScope,
	}))
	if err != nil {
		return fmt.Errorf("start device auth: %w", err)
	}
	msg := initResp.Msg

	verificationURI := msg.GetVerificationUri()
	userCode := msg.GetUserCode()
	deviceCode := msg.GetDeviceCode()
	if verificationURI == "" || userCode == "" || deviceCode == "" {
		return fmt.Errorf("start device auth: server returned an incomplete authorization response")
	}
	verificationURI, err = resolveDeviceVerificationURI(apiURL, verificationURI)
	if err != nil {
		return fmt.Errorf("start device auth: invalid verification URL: %w", err)
	}
	verificationURL, err := buildDeviceVerificationURL(verificationURI, userCode)
	if err != nil {
		return fmt.Errorf("start device auth: invalid verification URL: %w", err)
	}
	interval := int(msg.GetInterval())
	if interval <= 0 {
		interval = 5
	}

	fmt.Fprintf(os.Stdout, "\nOpen this URL in your browser:\n\n  %s\n\nAuthorization code: %s\n\nWaiting for authorization...\n", verificationURL, userCode)
	openBrowser(verificationURL)

	// Poll until approved, expired, or context cancelled.
	pollInterval := time.Duration(interval) * time.Second
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			pollResp, err := authClient.ExchangeDeviceCode(ctx, connect.NewRequest(&authv1.ExchangeDeviceCodeRequest{
				DeviceCode: deviceCode,
				ClientId:   deviceAuthClientID,
			}))
			if err != nil {
				// authorization_pending is expected while user hasn't approved yet.
				if isAuthPending(err) {
					continue
				}
				return fmt.Errorf("device code exchange: %w", err)
			}

			result := pollResp.Msg
			outcome := result.GetOauthError()
			if outcome == "" {
				outcome = result.GetStatus()
			}
			nextInterval := nextDevicePollInterval(pollInterval, outcome)
			if nextInterval != pollInterval {
				pollInterval = nextInterval
				ticker.Reset(pollInterval)
			}
			if isDeviceAuthPendingStatus(outcome) {
				continue
			}
			switch outcome {
			case "denied", "access_denied":
				return fmt.Errorf("device authorization was denied")
			case "expired", "expired_token":
				return fmt.Errorf("device authorization expired; run evs login again")
			}
			if result.GetAccessToken() == "" {
				return fmt.Errorf("login failed: no access token returned (status: %s)", outcome)
			}
			identity, err := client.New(client.Options{
				APIURL:      apiURL,
				AccessToken: result.GetAccessToken(),
			}).Whoami(ctx)
			if err != nil {
				return fmt.Errorf("validate device authorization: %w", err)
			}

			tok := credentials.Token{
				AccessToken: result.GetAccessToken(),
				OrgID:       identity.OrgID,
				OrgSlug:     identity.OrgSlug,
				UserID:      identity.UserID,
				Email:       identity.Email,
			}
			if err := saveLoginContext(cfg, apiURL, identity.OrgSlug); err != nil {
				return fmt.Errorf("save login context: %w", err)
			}
			if err := credentials.Save(cfg.ActiveContext, tok); err != nil {
				return fmt.Errorf("save credentials: %w", err)
			}

			fmt.Fprintf(os.Stdout, "Logged in as %s (%s)\n", identity.Email, identity.OrgSlug)
			return nil
		}
	}
}

func saveLoginContext(cfg *clicfg.Config, apiURL, orgSlug string) error {
	active := cfg.ActiveCtx()
	active.APIURL = strings.TrimRight(apiURL, "/")
	if orgSlug != "" {
		active.OrgSlug = orgSlug
	}
	cfg.SetContext(cfg.ActiveContext, active)
	return clicfg.Save(cfg)
}

func resolveDeviceVerificationURI(apiURL, verificationURI string) (string, error) {
	reference, err := url.Parse(verificationURI)
	if err != nil {
		return "", err
	}
	if reference.IsAbs() {
		return reference.String(), nil
	}
	base, err := url.Parse(apiURL)
	if err != nil {
		return "", err
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return "", fmt.Errorf("unsupported API URL scheme %q", base.Scheme)
	}
	if base.Host == "" {
		return "", fmt.Errorf("API URL has no host")
	}
	return base.ResolveReference(reference).String(), nil
}

func buildDeviceVerificationURL(verificationURI, userCode string) (string, error) {
	parsed, err := url.Parse(verificationURI)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("unsupported verification URL scheme %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("verification URL has no host")
	}
	query := parsed.Query()
	query.Set("code", userCode)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func isDeviceAuthPendingStatus(status string) bool {
	switch status {
	case "authorization_pending", "pending", "slow_down":
		return true
	default:
		return false
	}
}

func nextDevicePollInterval(current time.Duration, status string) time.Duration {
	if status == "slow_down" {
		return current + 5*time.Second
	}
	return current
}

func browserAuthAvailable() bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("EVS_NO_BROWSER")), "1") ||
		strings.EqualFold(strings.TrimSpace(os.Getenv("EVS_NO_BROWSER")), "true") ||
		strings.EqualFold(strings.TrimSpace(os.Getenv("CI")), "true") {
		return false
	}
	if runtime.GOOS == "linux" && os.Getenv("SSH_CONNECTION") != "" &&
		os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return false
	}
	return true
}

func openBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "linux":
		cmd = "xdg-open"
		args = []string{url}
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", url}
	default:
		return fmt.Errorf("unsupported platform %q", runtime.GOOS)
	}
	return exec.Command(cmd, args...).Start()
}

func isAuthPending(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "authorization_pending") || strings.Contains(msg, "slow_down")
}
