package bots

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/interop"
	"github.com/everstacklabs/everstack/internal/lib/apikey"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/gorilla/mux"
)

type fakeStore struct {
	bots                               map[string]Bot
	failInsert, failUpdate, failDelete error
}

func (s *fakeStore) List(_ context.Context, tenant string) ([]Bot, error) {
	out := []Bot{}
	for _, b := range s.bots {
		if b.TenantID == tenant {
			out = append(out, b)
		}
	}
	return out, nil
}
func (s *fakeStore) Get(_ context.Context, tenant, id string) (*Bot, error) {
	b, ok := s.bots[id]
	if !ok || b.TenantID != tenant {
		return nil, ErrNotFound
	}
	return &b, nil
}
func (s *fakeStore) duplicate(b *Bot) bool {
	for _, old := range s.bots {
		if old.ID != b.ID && old.TenantID == b.TenantID && strings.EqualFold(old.Name, b.Name) {
			return true
		}
	}
	return false
}
func (s *fakeStore) Insert(_ context.Context, b *Bot) error {
	if s.failInsert != nil {
		return s.failInsert
	}
	if s.duplicate(b) {
		return ErrNameTaken
	}
	b.ID = fmt.Sprintf("bot-%d", len(s.bots)+1)
	b.CreatedAt = time.Now()
	s.bots[b.ID] = *b
	return nil
}
func (s *fakeStore) Update(ctx context.Context, b *Bot) error {
	if _, err := s.Get(ctx, b.TenantID, b.ID); err != nil {
		return err
	}
	if s.failUpdate != nil {
		return s.failUpdate
	}
	if s.duplicate(b) {
		return ErrNameTaken
	}
	s.bots[b.ID] = *b
	return nil
}
func (s *fakeStore) Delete(ctx context.Context, tenant, id string, _ time.Time) error {
	if _, err := s.Get(ctx, tenant, id); err != nil {
		return err
	}
	if s.failDelete != nil {
		return s.failDelete
	}
	delete(s.bots, id)
	return nil
}
func (*fakeStore) Lock(context.Context, string, string) (func(), error) { return func() {}, nil }

type fakeKeys struct {
	live                   map[string]bool
	events                 []string
	surfaces               [][]string
	failCreate, failRevoke error
}

func (k *fakeKeys) CreateBotKey(ctx context.Context, name string, surfaces []string) (string, string, string, error) {
	if k.failCreate != nil {
		return "", "", "", k.failCreate
	}
	id := fmt.Sprintf("key-%d", len(k.live)+1)
	k.live[id] = true
	k.events = append(k.events, "create:"+id)
	k.surfaces = append(k.surfaces, append([]string{}, surfaces...))
	return id, "sk-once-" + id, "sk-***" + id, nil
}
func (k *fakeKeys) RevokeBotKey(_ context.Context, id string) error {
	k.events = append(k.events, "revoke:"+id)
	if k.failRevoke != nil {
		return k.failRevoke
	}
	k.live[id] = false
	return nil
}

type fakeRemotes struct{}

func (fakeRemotes) ListRemoteAgents(_ context.Context, tenant string) ([]interop.RemoteAgent, error) {
	if tenant == "tenant-a" {
		return []interop.RemoteAgent{{Name: "my-remote"}}, nil
	}
	return nil, nil
}

type botRouter struct{ router *mux.Router }

func (r botRouter) HandleFunc(path string, f http.HandlerFunc) { r.router.HandleFunc(path, f) }

type harness struct {
	store  *fakeStore
	keys   *fakeKeys
	router *mux.Router
}

func newHarness() *harness {
	s := &fakeStore{bots: map[string]Bot{}}
	k := &fakeKeys{live: map[string]bool{}}
	r := mux.NewRouter()
	NewHandlers(s, k, fakeRemotes{}).Mount(botRouter{r})
	return &harness{s, k, r}
}
func session(tenant string) context.Context {
	ctx := contextkeys.WithTenantID(context.Background(), tenant)
	ctx = contextkeys.WithUserID(ctx, "session-user")
	return contextkeys.WithTenantAuthenticated(ctx)
}
func (h *harness) request(ctx context.Context, method, path, body, contentType string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, r.WithContext(ctx))
	return w
}
func (h *harness) call(tenant, method, path, body string) *httptest.ResponseRecorder {
	return h.request(session(tenant), method, path, body, "application/json")
}
func checkCode(t *testing.T, r *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if r.Code != status {
		t.Fatalf("status=%d want=%d: %s", r.Code, status, r.Body.String())
	}
	if code != "" {
		var body map[string]string
		if json.Unmarshal(r.Body.Bytes(), &body) != nil || body["error"] != code {
			t.Fatalf("want error %s: %s", code, r.Body.String())
		}
	}
}
func createBot(t *testing.T, h *harness, tenant, name, kind string) Bot {
	t.Helper()
	r := h.call(tenant, "POST", "/api/bots", fmt.Sprintf(`{"name":%q,"kind":%q}`, name, kind))
	checkCode(t, r, 201, "")
	var body struct {
		Bot Bot    `json:"bot"`
		Key string `json:"api_key"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Key == "" || body.Bot.KeyHint == "" {
		t.Fatal("one-time secret and hint required")
	}
	return body.Bot
}

func TestBotAuthFailClosedAndJSONOnly(t *testing.T) {
	h := newHarness()
	for _, ctx := range []context.Context{
		context.Background(), contextkeys.WithTenantID(context.Background(), "tenant-a"), session(""),
		contextkeys.WithAuthenticatedAPIKeyID(context.Background(), "tenant-a", "verified-digest", "key"),
		contextkeys.WithAPIKeySurfaces(contextkeys.WithAuthenticatedAPIKeyID(context.Background(), "tenant-a", "verified-digest", "key"), []string{"inference", "mcp", "telemetry", "a2a"}),
	} {
		for _, method := range []string{"GET", "POST", "PATCH", "DELETE"} {
			checkCode(t, h.request(ctx, method, "/api/bots", `{}`, "application/json"), 401, "")
		}
	}
	for _, method := range []string{"POST", "PATCH", "DELETE"} {
		for _, ct := range []string{"", "text/plain", "application/x-www-form-urlencoded"} {
			checkCode(t, h.request(session("tenant-a"), method, "/api/bots", `{}`, ct), 415, "json_required")
		}
	}
	if len(h.keys.live) != 0 {
		t.Fatal("auth or content type failure minted a key")
	}
}
func TestBotValidationCodes(t *testing.T) {
	h := newHarness()
	for _, tc := range []struct{ body, code string }{
		{`{"name":" ","kind":"custom"}`, "invalid_name"}, {`{"name":"_bot","kind":"custom"}`, "invalid_name"}, {`{"name":"` + strings.Repeat("a", 65) + `","kind":"custom"}`, "invalid_name"},
		{`{"name":"ok","kind":"unknown"}`, "invalid_kind"}, {`{"name":"ok","kind":"custom","surfaces":["admin"]}`, "invalid_surfaces"},
		{`{"name":"ok","kind":"custom","surfaces":null}`, "invalid_surfaces"}, {`{"name":"ok","kind":"custom","surfaces":["mcp","mcp"]}`, "invalid_surfaces"},
		{`{"name":"ok","kind":"grok-bot","surfaces":["inference"]}`, "invalid_surfaces"}, {`{"name":"ok","kind":"custom","remote_agent":"foreign"}`, "remote_not_found"},
		{`{"name":"ok","kind":"custom","tenant_id":"tenant-b"}`, "invalid_body"},
	} {
		checkCode(t, h.call("tenant-a", "POST", "/api/bots", tc.body), 400, tc.code)
	}
	if len(h.keys.live) != 0 {
		t.Fatal("invalid request minted a key")
	}
}
func TestAllKindsAndDefaults(t *testing.T) {
	for _, kind := range []string{"openclaw", "hermes", "nanobot", "zeroclaw", "nanoclaw", "copilotkit-openbot", "nightly-openbot", "custom", "grok-bot", "grok-build", "meta-muse"} {
		h := newHarness()
		b := createBot(t, h, "tenant-a", " My bot ", kind)
		want := []string{"inference", "mcp", "telemetry"}
		if kind == "grok-build" {
			want = []string{"inference", "mcp"}
		} else if kind == "grok-bot" || kind == "meta-muse" {
			want = []string{"mcp"}
		}
		if b.Name != "My bot" || !reflect.DeepEqual([]string(b.Surfaces), want) {
			t.Fatalf("%s: %+v", kind, b)
		}
		list := h.call("tenant-a", "GET", "/api/bots", "")
		if strings.Contains(list.Body.String(), "sk-once") {
			t.Fatal("list exposed key material")
		}
	}
}
func TestBotTenantIsolationAndNameUniqueness(t *testing.T) {
	h := newHarness()
	a := createBot(t, h, "tenant-a", "Same bot", "custom")
	b := createBot(t, h, "tenant-b", "Same bot", "custom")
	checkCode(t, h.call("tenant-a", "POST", "/api/bots", `{"name":"same BOT","kind":"custom"}`), 409, "name_taken")
	before := len(h.keys.events)
	for _, tc := range []struct{ method, suffix, body string }{{"PATCH", "", `{"name":"stolen"}`}, {"POST", "/rotate-key", ""}, {"DELETE", "", ""}} {
		checkCode(t, h.call("tenant-a", tc.method, "/api/bots/"+b.ID+tc.suffix, tc.body), 404, "not_found")
	}
	if len(h.keys.events) != before {
		t.Fatal("foreign key service called")
	}
	list := h.call("tenant-a", "GET", "/api/bots", "")
	if strings.Contains(list.Body.String(), b.ID) || !strings.Contains(list.Body.String(), a.ID) {
		t.Fatal("cross-tenant list")
	}
	checkCode(t, h.call("tenant-b", "PATCH", "/api/bots/"+b.ID, `{"remote_agent":"my-remote"}`), 400, "remote_not_found")
	checkCode(t, h.call("tenant-a", "PATCH", "/api/bots/"+a.ID, `{"remote_agent":"my-remote","surfaces":["a2a"]}`), 200, "")
}
func TestBotRotationDeleteAndCompensation(t *testing.T) {
	h := newHarness()
	b := createBot(t, h, "tenant-a", "test", "custom")
	old := h.store.bots[b.ID].APIKeyID
	r := h.call("tenant-a", "POST", "/api/bots/"+b.ID+"/rotate-key", "")
	checkCode(t, r, 200, "")
	current := h.store.bots[b.ID].APIKeyID
	if old == current || h.keys.live[old] || !h.keys.live[current] || !strings.Contains(r.Body.String(), "sk-once-") {
		t.Fatal("rotation lifecycle")
	}
	if !reflect.DeepEqual(h.keys.surfaces[0], h.keys.surfaces[1]) {
		t.Fatal("rotation changed surfaces")
	}
	h.keys.live[current] = false // An already revoked bot remains deletable.
	checkCode(t, h.call("tenant-a", "DELETE", "/api/bots/"+b.ID, ""), 204, "")
	if len(h.store.bots) != 0 || h.keys.live[current] {
		t.Fatal("delete lifecycle")
	}
	h.store.failInsert = errors.New("insert failed")
	checkCode(t, h.call("tenant-a", "POST", "/api/bots", `{"name":"failed","kind":"custom"}`), 500, "internal_error")
	for _, live := range h.keys.live {
		if live {
			t.Fatal("failed insert left live key")
		}
	}
	h.store.failInsert = nil
	b = createBot(t, h, "tenant-a", "rotate-failure", "custom")
	h.store.failUpdate = errors.New("update failed")
	checkCode(t, h.call("tenant-a", "POST", "/api/bots/"+b.ID+"/rotate-key", ""), 500, "internal_error")
	for _, live := range h.keys.live {
		if live {
			t.Fatal("failed rotation left live key")
		}
	}
	h.store.failUpdate = nil
	h.store.failDelete = errors.New("delete failed")
	checkCode(t, h.call("tenant-a", "DELETE", "/api/bots/"+b.ID, ""), 500, "internal_error")
	h.store.failDelete = nil
	checkCode(t, h.call("tenant-a", "DELETE", "/api/bots/"+b.ID, ""), 204, "")
}

func TestBotPatchValidationAndExplicitRemoteClear(t *testing.T) {
	h := newHarness()
	bot := createBot(t, h, "tenant-a", "test", "grok-bot")
	for _, tc := range []struct{ body, code string }{
		{`{"name":"@bad"}`, "invalid_name"}, {`{"surfaces":["inference"]}`, "invalid_surfaces"}, {`{"surfaces":null}`, "invalid_surfaces"},
		{`{"remote_agent":"absent"}`, "remote_not_found"}, {`{"remote_agent":123}`, "invalid_body"},
	} {
		checkCode(t, h.call("tenant-a", "PATCH", "/api/bots/"+bot.ID, tc.body), 400, tc.code)
	}
	checkCode(t, h.call("tenant-a", "PATCH", "/api/bots/"+bot.ID, `{"remote_agent":"my-remote","surfaces":[]}`), 200, "")
	checkCode(t, h.call("tenant-a", "PATCH", "/api/bots/"+bot.ID, `{"remote_agent":null}`), 200, "")
	stored := h.store.bots[bot.ID]
	if stored.RemoteAgent != nil || stored.Surfaces == nil || len(stored.Surfaces) != 0 {
		t.Fatal("clear changed empty-set semantics")
	}
}

func TestBotLifecycleInvalidatesAuthorizationCaches(t *testing.T) {
	h := newHarness()
	bot := createBot(t, h, "tenant-a", "cache-lifecycle", "custom")
	for _, step := range []struct {
		method, path, body string
		status             int
	}{
		{"PATCH", "/api/bots/" + bot.ID, `{"surfaces":["mcp"]}`, 200},
		{"POST", "/api/bots/" + bot.ID + "/rotate-key", "", 200},
		{"DELETE", "/api/bots/" + bot.ID, "", 204},
	} {
		revision := apikey.CacheRevision()
		checkCode(t, h.call("tenant-a", step.method, step.path, step.body), step.status, "")
		if revision == apikey.CacheRevision() {
			t.Fatalf("%s did not invalidate caches", step.method)
		}
	}
}
func TestGrokBuildAllowsOnlyInferenceAndMCP(t *testing.T) {
	for _, surfaces := range []string{`["inference","mcp"]`, `["inference"]`, `["mcp"]`, `[]`} {
		h := newHarness()
		checkCode(t, h.call("tenant-a", "POST", "/api/bots", `{"name":"build","kind":"grok-build","surfaces":`+surfaces+`}`), 201, "")
	}
	for _, surface := range []string{"telemetry", "a2a"} {
		h := newHarness()
		checkCode(t, h.call("tenant-a", "POST", "/api/bots", `{"name":"build","kind":"grok-build","surfaces":["`+surface+`"]}`), 400, "invalid_surfaces")
	}
}

func TestBotRateLimitValidationAndResponses(t *testing.T) {
	for _, value := range []string{`0`, `-1`, `100001`, `1.5`, `"12"`, `true`, `[]`, `{}`} {
		h := newHarness()
		checkCode(t, h.call("tenant-a", "POST", "/api/bots", `{"name":"bad","kind":"custom","rate_limit_rpm":`+value+`}`), 400, "invalid_rate_limit")
		bot := createBot(t, h, "tenant-a", "good", "custom")
		checkCode(t, h.call("tenant-a", "PATCH", "/api/bots/"+bot.ID, `{"rate_limit_rpm":`+value+`}`), 400, "invalid_rate_limit")
	}
	h := newHarness()
	checkCode(t, h.call("tenant-a", "POST", "/api/bots", `{"name":"bad","kind":"custom","rate_limit_rpm":null}`), 400, "invalid_rate_limit")
	r := h.call("tenant-a", "POST", "/api/bots", `{"name":"limited","kind":"custom","rate_limit_rpm":1}`)
	checkCode(t, r, 201, "")
	var response struct {
		Bot Bot `json:"bot"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	b := response.Bot
	if b.APIKeyID == "" || b.RateLimitRPM == nil || *b.RateLimitRPM != 1 || b.Usage.Calls30d != 0 || len(b.Usage.BySurface) != 4 {
		t.Fatalf("response: %s", r.Body)
	}
	r = h.call("tenant-a", "PATCH", "/api/bots/"+b.ID, `{"rate_limit_rpm":null}`)
	checkCode(t, r, 200, "")
	if !strings.Contains(r.Body.String(), `"rate_limit_rpm":null`) {
		t.Fatal(r.Body)
	}
}
