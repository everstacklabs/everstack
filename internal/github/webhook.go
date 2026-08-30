package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Errors for webhook handling.
var (
	ErrInstallationNotFound = errors.New("github: installation not found")
	ErrInvalidSignature     = errors.New("github: invalid webhook signature")
	ErrDuplicateDelivery    = errors.New("github: duplicate webhook delivery")
	ErrUnsupportedEvent     = errors.New("github: unsupported event type")
)

// WebhookHandler processes incoming GitHub webhook events.
type WebhookHandler struct {
	app   *App
	store *Store
}

// NewWebhookHandler creates a new webhook handler.
func NewWebhookHandler(app *App, store *Store) *WebhookHandler {
	return &WebhookHandler{app: app, store: store}
}

// webhookEvent holds common fields from GitHub webhook payloads.
type webhookEvent struct {
	Action       string          `json:"action"`
	Installation json.RawMessage `json:"installation"`
}

// installationPayload is the full installation object from webhook events.
type installationPayload struct {
	ID      int64 `json:"id"`
	Account struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"account"`
	AppID               int64           `json:"app_id"`
	Permissions         json.RawMessage `json:"permissions"`
	RepositorySelection string          `json:"repository_selection"`
	Sender              struct {
		Login string `json:"login"`
	} `json:"sender"`
}

// installationEvent wraps the full installation webhook event.
type installationEvent struct {
	Action       string              `json:"action"`
	Installation installationPayload `json:"installation"`
	Sender       struct {
		Login string `json:"login"`
	} `json:"sender"`
}

// maxWebhookBodySize is the maximum allowed body size for webhook payloads (256KB).
const maxWebhookBodySize = 256 * 1024

// ServeHTTP handles incoming GitHub webhook HTTP requests.
// Enforces: body size limit, content-type, signature verification, delivery deduplication.
func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Content-Type validation
	ct := r.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		http.Error(w, "Unsupported content type", http.StatusUnsupportedMediaType)
		return
	}

	// Body size limit
	r.Body = http.MaxBytesReader(w, r.Body, maxWebhookBodySize)

	// Read body for signature verification (must verify before parsing)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		if strings.Contains(err.Error(), "http: request body too large") {
			http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	var (
		webhookKey string
		tenantID   string
		appID      int64
		appClient  *App
	)
	if vars := mux.Vars(r); vars != nil {
		webhookKey = strings.TrimSpace(vars["webhook_key"])
	}

	// Resolve webhook secret:
	// - Dynamic mode: /webhooks/github/{webhook_key} with per-tenant stored app.
	// - Legacy mode: static env-configured GitHub App.
	signature := r.Header.Get("X-Hub-Signature-256")
	if webhookKey != "" {
		if h.store == nil {
			http.Error(w, "GitHub store unavailable", http.StatusInternalServerError)
			return
		}
		var appRec *AppRecord
		appClient, appRec, err = h.store.LoadAppClientForWebhookKey(r.Context(), webhookKey)
		if err != nil {
			http.Error(w, "Unknown webhook key", http.StatusNotFound)
			return
		}
		tenantID = appRec.TenantID
		appID = appRec.AppID
		if !h.verifySignatureWithSecret(body, signature, appClient.WebhookSecret()) {
			logger.Warn("github: webhook signature verification failed")
			http.Error(w, "Invalid signature", http.StatusUnauthorized)
			return
		}
	} else if !h.verifySignature(body, signature) {
		logger.Warn("github: webhook signature verification failed")
		http.Error(w, "Invalid signature", http.StatusUnauthorized)
		return
	}

	// Delivery ID deduplication
	deliveryID := r.Header.Get("X-GitHub-Delivery")
	if deliveryID == "" {
		http.Error(w, "Missing delivery ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var isNew bool
	if webhookKey != "" {
		isNew, err = h.store.CheckDeliveryID(ctx, webhookKey+":"+deliveryID)
	} else {
		isNew, err = h.store.CheckDeliveryID(ctx, deliveryID)
	}
	if err != nil {
		logger.WithFields("delivery_id", deliveryID, "error", err.Error()).
			Warn("github: failed to check delivery ID")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if !isNew {
		logger.WithFields("delivery_id", deliveryID).
			Debug("github: duplicate webhook delivery, skipping")
		w.WriteHeader(http.StatusOK)
		return
	}

	// Route by event type
	eventType := r.Header.Get("X-GitHub-Event")
	logger.WithFields("event", eventType, "delivery_id", deliveryID).
		Debug("github: received webhook event")

	switch eventType {
	case "installation":
		h.handleInstallation(ctx, w, body, tenantID, appID, appClient)
	case "installation_repositories":
		// Repository added/removed — log for now, no action needed in v1
		logger.WithFields("delivery_id", deliveryID).
			Debug("github: installation_repositories event received")
		w.WriteHeader(http.StatusOK)
	case "ping":
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"message":"pong"}`)
	default:
		logger.WithFields("event", eventType).
			Debug("github: unhandled webhook event type")
		w.WriteHeader(http.StatusOK)
	}
}

// verifySignature verifies the X-Hub-Signature-256 HMAC signature.
func (h *WebhookHandler) verifySignature(body []byte, signature string) bool {
	if h.app == nil || h.app.webhookSecret == "" || signature == "" {
		return false
	}
	return h.verifySignatureWithSecret(body, signature, h.app.webhookSecret)
}

func (h *WebhookHandler) verifySignatureWithSecret(body []byte, signature, secret string) bool {
	if secret == "" || signature == "" {
		return false
	}
	// Signature format: "sha256=<hex>"
	parts := strings.SplitN(signature, "=", 2)
	if len(parts) != 2 || parts[0] != "sha256" {
		return false
	}

	expected, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	actual := mac.Sum(nil)

	return hmac.Equal(actual, expected)
}

// handleInstallation processes installation lifecycle events.
func (h *WebhookHandler) handleInstallation(ctx context.Context, w http.ResponseWriter, body []byte, tenantID string, appID int64, appClient *App) {
	var event installationEvent
	if err := json.Unmarshal(body, &event); err != nil {
		logger.WithFields("error", err.Error()).
			Warn("github: failed to parse installation event")
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	inst := event.Installation
	logger.WithFields(
		"action", event.Action,
		"installation_id", inst.ID,
		"account", inst.Account.Login,
	).Info("github: processing installation event")

	switch event.Action {
	case "created":
		perms, _ := json.Marshal(inst.Permissions)
		senderLogin := event.Sender.Login
		status := "pending"
		if tenantID != "" {
			status = "active"
		}
		effectiveAppID := inst.AppID
		if appID > 0 {
			effectiveAppID = appID
		}
		installation := &Installation{
			TenantID:            tenantID,
			InstallationID:      inst.ID,
			AccountLogin:        inst.Account.Login,
			AccountType:         inst.Account.Type,
			AppID:               effectiveAppID,
			Permissions:         perms,
			RepositorySelection: inst.RepositorySelection,
			Status:              status,
			InstalledBy:         &senderLogin,
		}
		if err := h.store.UpsertInstallation(ctx, installation); err != nil {
			logger.WithFields("installation_id", inst.ID, "error", err.Error()).
				Error("github: failed to upsert installation")
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

	case "deleted":
		if err := h.store.DeleteInstallation(ctx, inst.ID); err != nil {
			logger.WithFields("installation_id", inst.ID, "error", err.Error()).
				Error("github: failed to delete installation")
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		// Invalidate cached token
		if appClient != nil {
			appClient.InvalidateToken(inst.ID)
		} else if h.app != nil {
			h.app.InvalidateToken(inst.ID)
		}

	case "suspend":
		if err := h.store.SuspendInstallation(ctx, inst.ID); err != nil {
			logger.WithFields("installation_id", inst.ID, "error", err.Error()).
				Error("github: failed to suspend installation")
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		if appClient != nil {
			appClient.InvalidateToken(inst.ID)
		} else if h.app != nil {
			h.app.InvalidateToken(inst.ID)
		}

	case "unsuspend":
		if err := h.store.UnsuspendInstallation(ctx, inst.ID); err != nil {
			logger.WithFields("installation_id", inst.ID, "error", err.Error()).
				Error("github: failed to unsuspend installation")
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

	default:
		logger.WithFields("action", event.Action, "installation_id", inst.ID).
			Debug("github: unhandled installation action")
	}

	w.WriteHeader(http.StatusOK)
}
