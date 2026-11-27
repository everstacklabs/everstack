package v1

// Key rotation marks the key that actually produced the authentication
// error, failed marks expire, and rebuilt bundles skip failed keys.

import (
	"context"
	"database/sql/driver"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/domain/provider_api_keys"
	"github.com/everstacklabs/everstack/internal/domain/provider_config"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/tracing"
)

// keyedModel answers with 401 when built on the key "bad".
type keyedModel struct {
	name, key string
}

func (p *keyedModel) Name() string                { return p.name }
func (p *keyedModel) SupportsModel(m string) bool { return m == "m-keyed" }
func (p *keyedModel) Chat(context.Context, gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	if p.key == "bad" {
		return gw.ChatCompletionResponse{}, gw.NewUpstreamError(p.name, 401, []byte(`{"error":{"message":"invalid api key"}}`))
	}
	return gw.ChatCompletionResponse{ID: "x", Model: "m-keyed", Choices: []gw.Choice{{Message: gw.NewMessage(gw.RoleAssistant, gw.Text("served by "+p.key))}}}, nil
}
func (p *keyedModel) ChatStream(ctx context.Context, r gw.ChatCompletionRequest, on func(gw.ChatResponseChunk) error) error {
	resp, err := p.Chat(ctx, r)
	if err != nil {
		return err
	}
	return on(gw.ChatResponseChunk{ID: "x", Model: resp.Model, Choices: []gw.ChoiceDelta{{Delta: resp.Choices[0].Message}}})
}
func (p *keyedModel) Embed(context.Context, gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	return gw.EmbeddingsResponse{}, errors.New("not used")
}

// keyedProvider builds a provider the way the real factories do: the key is
// chosen at construction (here: the first key given) and stamped on the
// tracing middleware. runtimeName is the provider's Name(), which can differ
// from the factory's registered name (google registers "google", its
// provider is "gemini").
func keyedProvider(runtimeName string, keys []factory.APIKeyInput) gw.Provider {
	k := keys[0]
	return tracing.NewMiddlewareWithKey(&keyedModel{name: runtimeName, key: k.KeyEncrypted}, nil, k.ID, k.KeyName, k.Source)
}

var registerKeyedFactories sync.Once

func keyedFactories() {
	registerKeyedFactories.Do(func() {
		factory.Default.Register("keyprov", func(in factory.AggregatedInput) (gw.Provider, error) {
			return keyedProvider("keyprov", in.APIKeys), nil
		})
		factory.Default.Register("keyprov-registered", func(in factory.AggregatedInput) (gw.Provider, error) {
			return keyedProvider("keyprov-runtime", in.APIKeys), nil
		})
	})
}

type fakeKeyRepo struct {
	provider_api_keys.Repository
	keys []*provider_api_keys.ProviderAPIKey
}

func (r fakeKeyRepo) ListByProviderConfig(context.Context, string) ([]*provider_api_keys.ProviderAPIKey, error) {
	return r.keys, nil
}

func keyRows(providerName string) *sqlmock.Rows {
	cols := []string{"id", "organization_id", "provider_name", "api_key_encrypted", "api_key_source",
		"enabled_models", "custom_base_url", "custom_settings", "is_active", "created_at", "updated_at", "synced_to_yaml_at",
		"catalog_status", "is_from_catalog", "catalog_synced_at", "deprecated_at", "provider_type",
		"supports_model_discovery", "discovery_api_endpoint", "last_used_at"}
	now := time.Now()
	vals := []driver.Value{"cfg-1", "tenant-1", providerName, "", "ui",
		[]byte(`["m-keyed"]`), nil, []byte(`{}`), true, now, now, nil,
		"", false, nil, nil, "static", false, nil, nil}
	return sqlmock.NewRows(cols).AddRow(vals...)
}

// keyServer is a shared-mode gateway whose tenant-1 bundle serves m-keyed on
// the key "bad", with keys [good, bad] in the database.
func keyServer(t *testing.T, factoryName, runtimeName string) *Server {
	t.Helper()
	keyedFactories()
	raw, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	mock.MatchExpectationsInOrder(false)
	for i := 0; i < 20; i++ {
		mock.ExpectQuery(`FROM provider_configurations\s+WHERE organization_id`).WillReturnRows(keyRows(factoryName))
	}
	keys := []*provider_api_keys.ProviderAPIKey{
		{ID: "good", KeyName: "good", KeyEncrypted: "good", IsActive: true, Weight: 1},
		{ID: "bad", KeyName: "bad", KeyEncrypted: "bad", IsActive: true, Weight: 1},
	}
	s := CreateServerWithContext(context.Background(), &validator.GatewayConfig{}, &validator.FeaturesConfig{})
	s.feat.Gateway.EnableStreaming = true
	sctx := context.WithValue(context.Background(), contextkeys.SharedGatewayMode, true)
	sctx = context.WithValue(sctx, contextkeys.ProviderRepo, provider_config.NewRepository(sqlx.NewDb(raw, "postgres")))
	sctx = context.WithValue(sctx, contextkeys.APIKeyRepo, provider_api_keys.Repository(fakeKeyRepo{keys: keys}))
	s.ctx = sctx

	reg := gw.NewRegistry()
	reg.Register(keyedProvider(runtimeName, []factory.APIKeyInput{{ID: "bad", KeyName: "bad", KeyEncrypted: "bad", IsActive: true}}))
	s.tenantBundles.store("tenant-1", &tenantBundle{reg: reg, router: gw.NewRouter(reg, map[string]string{"m-keyed": runtimeName})})
	return s
}

// With keys [good, bad] and the bundle on "bad", a 401 must mark "bad", not
// the first available key, and the retry must be served by "good".
func TestKeyRotationMarksTheKeyThatFailed(t *testing.T) {
	for _, stream := range []bool{false, true} {
		s := keyServer(t, "keyprov", "keyprov")
		var got collected
		ctx := contextkeys.WithTenantID(context.Background(), "tenant-1")
		if err := processChatCompletion(ctx, s, chatReq("m-keyed", stream), got.send); err != nil {
			t.Fatalf("stream=%v: %v", stream, err)
		}
		if got.String() != "served by good" {
			t.Fatalf("stream=%v: served %q", stream, got.String())
		}
		if !s.isKeyFailed("bad") || s.isKeyFailed("good") {
			t.Fatalf("stream=%v: failed marks: bad=%v good=%v, want bad only", stream, s.isKeyFailed("bad"), s.isKeyFailed("good"))
		}
	}
}

// The provider's runtime Name() differing from its factory name (google
// registers "google", its provider is "gemini") must not stop rotation.
func TestKeyRotationWhenProviderNameDiffersFromFactory(t *testing.T) {
	s := keyServer(t, "keyprov-registered", "keyprov-runtime")
	var got collected
	ctx := contextkeys.WithTenantID(context.Background(), "tenant-1")
	if err := processChatCompletion(ctx, s, chatReq("m-keyed", false), got.send); err != nil {
		t.Fatal(err)
	}
	if got.String() != "served by good" {
		t.Fatalf("served %q", got.String())
	}
}
func TestFailedKeyMarksExpireAndRebuildsSkipThem(t *testing.T) {
	s := CreateServerWithContext(context.Background(), &validator.GatewayConfig{}, &validator.FeaturesConfig{})
	s.markFailedKey("k1")
	if !s.isKeyFailed("k1") {
		t.Fatal("a fresh mark must hold")
	}
	s.failedKeyMutex.Lock()
	s.failedKeys["k1"] = time.Now().Add(-time.Second)
	s.failedKeyMutex.Unlock()
	if s.isKeyFailed("k1") {
		t.Fatal("an expired mark must not hold")
	}
	if failedKeyTTL < time.Minute || failedKeyTTL > time.Hour {
		t.Fatalf("failedKeyTTL = %v, want minutes", failedKeyTTL)
	}

	s.markFailedKey("k2")
	keys := []factory.APIKeyInput{{ID: "k2"}, {ID: "k3"}}
	if got := s.skipFailedKeys(keys); len(got) != 1 || got[0].ID != "k3" {
		t.Fatalf("skipFailedKeys = %v, want only k3", got)
	}
	// Every key failed: keep them all rather than leave the provider with
	// none (a mark is a hint, the key may have recovered).
	only := []factory.APIKeyInput{{ID: "k2"}}
	if got := s.skipFailedKeys(only); len(got) != 1 {
		t.Fatalf("skipFailedKeys removed the only key: %v", got)
	}
}
