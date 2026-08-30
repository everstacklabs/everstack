package github

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

const manifestStateTTL = 10 * time.Minute
const maxGitHubOwnerLength = 39

var githubOwnerPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)

type ManifestHandler struct {
	store      *Store
	httpClient *http.Client
}

func NewManifestHandler(store *Store) *ManifestHandler {
	return &ManifestHandler{
		store: store,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

type manifestConversionResponse struct {
	ID            int64  `json:"id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	PEM           string `json:"pem"`
	WebhookSecret string `json:"webhook_secret"`
	HTMLURL       string `json:"html_url"`
}

func (h *ManifestHandler) Start(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.store == nil {
		http.Error(w, "GitHub integration is not configured", http.StatusServiceUnavailable)
		return
	}

	tenantID := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	if tenantID == "" {
		tenantID = strings.TrimSpace(r.Header.Get("x-tenant-id"))
	}
	if tenantID == "" {
		http.Error(w, "tenant_id is required", http.StatusBadRequest)
		return
	}

	returnTo := normalizeReturnTo(r.URL.Query().Get("return_to"))
	ownerInput := r.URL.Query().Get("owner")
	if strings.TrimSpace(ownerInput) == "" {
		ownerInput = os.Getenv("EVS_GITHUB_DEFAULT_OWNER")
	}
	owner, err := normalizeGitHubOwner(ownerInput)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	baseURL := publicBaseURL(r)
	callbackURL := strings.TrimSuffix(baseURL, "/") + "/integrations/github/callback"
	setupURL := strings.TrimSuffix(baseURL, "/") + returnTo

	state, err := randomHex(24)
	if err != nil {
		http.Error(w, "failed to generate state", http.StatusInternalServerError)
		return
	}
	webhookKey, err := randomHex(16)
	if err != nil {
		http.Error(w, "failed to generate webhook key", http.StatusInternalServerError)
		return
	}

	session := &ManifestSession{
		State:      state,
		TenantID:   tenantID,
		WebhookKey: webhookKey,
		ReturnTo:   returnTo,
		ExpiresAt:  time.Now().Add(manifestStateTTL),
	}
	if err := h.store.CreateManifestSession(r.Context(), session); err != nil {
		logger.WithFields("tenant_id", tenantID, "error", err.Error()).
			Error("github: failed to create manifest session")
		http.Error(w, "failed to create manifest session", http.StatusInternalServerError)
		return
	}

	appName := fmt.Sprintf("everstack-app-%s", webhookKey[:12])
	webhookURL := strings.TrimSuffix(baseURL, "/") + "/webhooks/github/" + webhookKey
	// If no explicit owner is provided, make the app installable on any account
	// (including orgs) by default.
	isPublicApp := owner == ""

	manifest := map[string]interface{}{
		"name":            appName,
		"url":             baseURL,
		"hook_attributes": map[string]interface{}{"url": webhookURL},
		"redirect_url":    callbackURL,
		"setup_url":       setupURL,
		"public":          isPublicApp,
		"default_permissions": map[string]string{
			"contents":      "read",
			"metadata":      "read",
			"pull_requests": "write",
		},
		"default_events": []string{
			"push",
		},
	}

	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		http.Error(w, "failed to build manifest", http.StatusInternalServerError)
		return
	}

	actionURL := manifestCreationURL(state, owner)
	data := struct {
		ActionURL string
		Manifest  string
		BackURL   string
		State     string
	}{
		ActionURL: actionURL,
		Manifest:  string(manifestBytes),
		BackURL:   returnTo,
		State:     state,
	}

	tpl := template.Must(template.New("manifest").Parse(`<!doctype html>
<html>
  <body>
    <form id="manifest" method="post" action="{{.ActionURL}}">
      <input type="hidden" name="manifest" value='{{.Manifest}}' />
    </form>
    <script>
      (function () {
        try {
          const backUrl = {{ printf "%q" .BackURL }};
          const state = {{ printf "%q" .State }};
          const replayKey = "evs_gh_manifest_submitted_" + state;
          if (window.sessionStorage && sessionStorage.getItem(replayKey) === "1") {
            window.location.replace(backUrl || "/settings/integrations");
            return;
          }
          if (window.sessionStorage) {
            sessionStorage.setItem(replayKey, "1");
          }
          window.addEventListener("pageshow", function (evt) {
            if (evt && evt.persisted) {
              window.location.replace(backUrl || "/settings/integrations");
            }
          });
          if (window.history && window.history.replaceState && backUrl) {
            window.history.replaceState({}, "", backUrl);
          }
        } catch (e) {}
        document.getElementById('manifest').submit();
      })();
    </script>
  </body>
</html>`))

	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tpl.Execute(w, data)
}

func (h *ManifestHandler) Callback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.store == nil {
		http.Error(w, "GitHub integration is not configured", http.StatusServiceUnavailable)
		return
	}

	code := strings.TrimSpace(r.URL.Query().Get("code"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if code == "" || state == "" {
		http.Error(w, "missing code or state", http.StatusBadRequest)
		return
	}

	session, err := h.store.ConsumeManifestSession(r.Context(), state)
	if err != nil {
		http.Error(w, "invalid or expired state", http.StatusBadRequest)
		return
	}

	conv, err := h.exchangeManifestCode(r.Context(), code)
	if err != nil {
		logger.WithFields("tenant_id", session.TenantID, "error", err.Error()).
			Error("github: manifest conversion failed")
		http.Error(w, "manifest conversion failed", http.StatusInternalServerError)
		return
	}

	if _, err := h.store.UpsertTenantApp(
		r.Context(),
		session.TenantID,
		conv.ID,
		conv.Slug,
		conv.Name,
		conv.PEM,
		conv.WebhookSecret,
		session.WebhookKey,
		strings.TrimSuffix(publicBaseURL(r), "/")+session.ReturnTo,
		conv.HTMLURL,
	); err != nil {
		logger.WithFields("tenant_id", session.TenantID, "error", err.Error()).
			Error("github: failed to store app credentials")
		http.Error(w, "failed to store app credentials", http.StatusInternalServerError)
		return
	}

	installURL := "https://github.com/apps/" + url.PathEscape(conv.Slug) + "/installations/new"
	http.Redirect(w, r, installURL, http.StatusFound)
}

func (h *ManifestHandler) exchangeManifestCode(ctx context.Context, code string) (*manifestConversionResponse, error) {
	endpoint := "https://api.github.com/app-manifests/" + url.PathEscape(code) + "/conversions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("manifest conversion failed (status %d): %s", resp.StatusCode, string(body))
	}

	var conv manifestConversionResponse
	if err := json.NewDecoder(resp.Body).Decode(&conv); err != nil {
		return nil, err
	}
	if conv.ID <= 0 || conv.PEM == "" || conv.WebhookSecret == "" {
		return nil, fmt.Errorf("manifest conversion response is missing required credentials")
	}
	return &conv, nil
}

func randomHex(nBytes int) (string, error) {
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func normalizeReturnTo(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "/settings/integrations"
	}
	if !strings.HasPrefix(v, "/") {
		return "/settings/integrations"
	}
	return v
}

func normalizeGitHubOwner(v string) (string, error) {
	owner := strings.TrimSpace(v)
	if owner == "" {
		return "", nil
	}
	if len(owner) > maxGitHubOwnerLength || !githubOwnerPattern.MatchString(owner) {
		return "", fmt.Errorf("invalid owner: expected GitHub organization login")
	}
	return owner, nil
}

func manifestCreationURL(state, owner string) string {
	base := "https://github.com/settings/apps/new"
	if owner != "" {
		base = "https://github.com/organizations/" + url.PathEscape(owner) + "/settings/apps/new"
	}
	return base + "?state=" + url.QueryEscape(state)
}

func publicBaseURL(r *http.Request) string {
	if v := strings.TrimSpace(os.Getenv("EVS_GITHUB_PUBLIC_URL")); v != "" {
		return strings.TrimSuffix(v, "/")
	}
	proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	return strings.TrimSuffix(proto+"://"+host, "/")
}
