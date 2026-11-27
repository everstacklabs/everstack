package v1

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	providerHandler "github.com/everstacklabs/everstack/internal/commands/handlers/provider"
	"github.com/everstacklabs/everstack/internal/domain/provider_api_keys"
	"github.com/everstacklabs/everstack/internal/domain/provider_config"
	"github.com/everstacklabs/everstack/internal/enterprise"
	providerEvents "github.com/everstacklabs/everstack/internal/events/provider"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/services/provider_catalog"
	providerspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/providers/v1"
	"github.com/everstacklabs/everstack/pkg/tenant"
)

const (
	testOrg    = "11111111-1111-1111-1111-11111111111a"
	testUser   = "user_01TESTUSER"
	testCfgID  = "22222222-2222-2222-2222-22222222222b"
	testKeyID  = "33333333-3333-3333-3333-33333333333b"
	notAUUIDID = "not-a-uuid"
)

type captureBus struct {
	mu     sync.Mutex
	events []interface{}
}

func (b *captureBus) Publish(_ context.Context, event interface{}) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, event)
	return nil
}

func (b *captureBus) Subscribe(string, func(ctx context.Context, event interface{}) error) {}

// newKeyTestServer wires the real handler and Postgres repositories to a
// sqlmock database, so the tests see the SQL the RPCs actually run.
func newKeyTestServer(t *testing.T) (*Server, sqlmock.Sqlmock, *captureBus) {
	t.Helper()
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
		_ = rawDB.Close()
	})
	db := sqlx.NewDb(rawDB, "postgres")
	bus := &captureBus{}
	h := providerHandler.NewHandler(provider_config.NewRepository(db), provider_api_keys.NewPostgresRepository(db), bus)
	return &Server{db: db, providerHandler: h, configPath: filepath.Join(t.TempDir(), "missing.yaml"), eventBus: bus}, mock, bus
}

func tenantCtx() context.Context {
	return contextkeys.WithUserID(contextkeys.WithTenantID(context.Background(), testOrg), testUser)
}

type keyRPC func(s *Server, ctx context.Context, configID, keyID string) error

var keyRPCs = map[string]keyRPC{
	"AddProviderAPIKey": func(s *Server, ctx context.Context, configID, _ string) error {
		_, err := s.AddProviderAPIKey(ctx, connect.NewRequest(&providerspb.AddProviderAPIKeyRequest{
			ProviderConfigId: configID, KeyName: "stolen", ApiKey: "sk-attacker-0123456789", Weight: 1,
		}))
		return err
	},
	"UpdateAPIKeyWeight": func(s *Server, ctx context.Context, _, keyID string) error {
		_, err := s.UpdateAPIKeyWeight(ctx, connect.NewRequest(&providerspb.UpdateAPIKeyWeightRequest{KeyId: keyID, Weight: 9}))
		return err
	},
	"ToggleAPIKey": func(s *Server, ctx context.Context, _, keyID string) error {
		_, err := s.ToggleAPIKey(ctx, connect.NewRequest(&providerspb.ToggleAPIKeyRequest{KeyId: keyID, IsActive: false}))
		return err
	},
	"DeleteProviderAPIKey": func(s *Server, ctx context.Context, _, keyID string) error {
		_, err := s.DeleteProviderAPIKey(ctx, connect.NewRequest(&providerspb.DeleteProviderAPIKeyRequest{KeyId: keyID}))
		return err
	},
	"ListProviderAPIKeys": func(s *Server, ctx context.Context, configID, _ string) error {
		_, err := s.ListProviderAPIKeys(ctx, connect.NewRequest(&providerspb.ListProviderAPIKeysRequest{ProviderConfigId: configID}))
		return err
	},
}

// Without a tenant in context no key RPC touches the database.
func TestKeyRPCsRequireTenant(t *testing.T) {
	for name, rpc := range keyRPCs {
		t.Run(name, func(t *testing.T) {
			s, _, _ := newKeyTestServer(t)
			err := rpc(s, context.Background(), testCfgID, testKeyID)
			if connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatalf("err = %v, want PermissionDenied", err)
			}
		})
	}
}

// A configuration or key outside the caller's tenant (another tenant's or
// the platform's) is NotFound, and the lookup carries the tenant predicate.
func TestKeyRPCsReturnNotFoundOutsideTenant(t *testing.T) {
	expectForeign := map[string]func(mock sqlmock.Sqlmock){
		"AddProviderAPIKey": func(mock sqlmock.Sqlmock) {
			mock.ExpectExec(`INSERT INTO provider_api_keys[\s\S]+FROM provider_configurations c\s+WHERE c\.id = \$2::uuid AND c\.organization_id = \$11`).
				WithArgs(sqlmock.AnyArg(), testCfgID, "stolen", "sk-attacker-0123456789", 1, true, "manual", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), testOrg).
				WillReturnResult(sqlmock.NewResult(0, 0))
		},
		"UpdateAPIKeyWeight": expectForeignKeyLookup,
		"ToggleAPIKey":       expectForeignKeyLookup,
		"DeleteProviderAPIKey": func(mock sqlmock.Sqlmock) {
			expectForeignKeyLookup(mock)
		},
		"ListProviderAPIKeys": func(mock sqlmock.Sqlmock) {
			mock.ExpectQuery(`FROM provider_configurations\s+WHERE organization_id = \$1 AND id = \$2`).
				WithArgs(testOrg, testCfgID).
				WillReturnRows(sqlmock.NewRows([]string{"id"}))
		},
	}
	for name, rpc := range keyRPCs {
		t.Run(name, func(t *testing.T) {
			s, mock, bus := newKeyTestServer(t)
			expectForeign[name](mock)
			err := rpc(s, tenantCtx(), testCfgID, testKeyID)
			if connect.CodeOf(err) != connect.CodeNotFound {
				t.Fatalf("err = %v, want NotFound", err)
			}
			if len(bus.events) != 0 {
				t.Fatalf("published %d events for a row outside the tenant", len(bus.events))
			}
		})
		t.Run(name+" malformed id", func(t *testing.T) {
			s, _, _ := newKeyTestServer(t)
			err := rpc(s, tenantCtx(), notAUUIDID, notAUUIDID)
			if connect.CodeOf(err) != connect.CodeNotFound {
				t.Fatalf("err = %v, want NotFound", err)
			}
		})
	}
}

func expectForeignKeyLookup(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`FROM provider_api_keys k\s+JOIN provider_configurations c ON c\.id = k\.provider_config_id\s+WHERE k\.id = \$1 AND c\.organization_id = \$2`).
		WithArgs(testKeyID, testOrg).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
}

var keyColumns = []string{"id", "provider_config_id", "key_name", "key_encrypted", "weight", "is_active", "source", "rate_limit_tracking", "created_at", "updated_at"}

// Key events carry the acting user from context, not "".
func TestKeyRPCsRecordTheCallingUser(t *testing.T) {
	ownKeyRow := func(mock sqlmock.Sqlmock) {
		now := time.Now()
		mock.ExpectQuery(`FROM provider_api_keys k\s+JOIN provider_configurations c`).
			WithArgs(testKeyID, testOrg).
			WillReturnRows(sqlmock.NewRows(keyColumns).AddRow(testKeyID, testCfgID, "primary", "sk-own-0123456789", 1, true, "manual", []byte("{}"), now, now))
	}
	scopedUpdate := func(mock sqlmock.Sqlmock) {
		mock.ExpectExec(`UPDATE provider_api_keys AS k[\s\S]+FROM provider_configurations AS c\s+WHERE k\.id = \$1 AND c\.id = k\.provider_config_id AND c\.organization_id = \$8`).
			WillReturnResult(sqlmock.NewResult(0, 1))
	}
	cases := map[string]struct {
		expect func(mock sqlmock.Sqlmock)
		call   func(s *Server) error
	}{
		"AddProviderAPIKey": {
			expect: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(`INSERT INTO provider_api_keys`).WillReturnResult(sqlmock.NewResult(0, 1))
			},
			call: func(s *Server) error {
				_, err := s.AddProviderAPIKey(tenantCtx(), connect.NewRequest(&providerspb.AddProviderAPIKeyRequest{
					ProviderConfigId: testCfgID, KeyName: "second", ApiKey: "sk-own-second-0123456789", Weight: 1,
				}))
				return err
			},
		},
		"UpdateAPIKeyWeight": {
			expect: func(mock sqlmock.Sqlmock) { ownKeyRow(mock); scopedUpdate(mock) },
			call: func(s *Server) error {
				_, err := s.UpdateAPIKeyWeight(tenantCtx(), connect.NewRequest(&providerspb.UpdateAPIKeyWeightRequest{KeyId: testKeyID, Weight: 4}))
				return err
			},
		},
		"ToggleAPIKey": {
			expect: func(mock sqlmock.Sqlmock) { ownKeyRow(mock); scopedUpdate(mock) },
			call: func(s *Server) error {
				_, err := s.ToggleAPIKey(tenantCtx(), connect.NewRequest(&providerspb.ToggleAPIKeyRequest{KeyId: testKeyID, IsActive: false}))
				return err
			},
		},
		"DeleteProviderAPIKey": {
			expect: func(mock sqlmock.Sqlmock) {
				ownKeyRow(mock)
				mock.ExpectExec(`DELETE FROM provider_api_keys AS k\s+USING provider_configurations AS c`).
					WithArgs(testKeyID, testOrg).
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			call: func(s *Server) error {
				_, err := s.DeleteProviderAPIKey(tenantCtx(), connect.NewRequest(&providerspb.DeleteProviderAPIKeyRequest{KeyId: testKeyID}))
				return err
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s, mock, bus := newKeyTestServer(t)
			tc.expect(mock)
			if err := tc.call(s); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if len(bus.events) != 1 {
				t.Fatalf("published %d events, want 1", len(bus.events))
			}
			var userID string
			switch ev := bus.events[0].(type) {
			case providerEvents.ProviderAPIKeyAddedEvent:
				userID = ev.UserID
			case providerEvents.ProviderAPIKeyWeightUpdatedEvent:
				userID = ev.UserID
			case providerEvents.ProviderAPIKeyToggledEvent:
				userID = ev.UserID
			case providerEvents.ProviderAPIKeyDeletedEvent:
				userID = ev.UserID
			default:
				t.Fatalf("event = %T", ev)
			}
			if userID != testUser {
				t.Fatalf("event UserID = %q, want %q", userID, testUser)
			}
		})
	}
}

var configColumns = []string{"id", "organization_id", "provider_name", "api_key_encrypted", "api_key_source", "enabled_models", "custom_base_url",
	"custom_settings", "is_active", "created_at", "updated_at", "synced_to_yaml_at", "catalog_status", "is_from_catalog", "catalog_synced_at",
	"deprecated_at", "provider_type", "supports_model_discovery", "discovery_api_endpoint", "last_used_at"}

// Keeping the stored key while moving the base URL is InvalidArgument and
// writes nothing.
func TestConfigureProviderRejectsBaseURLSwapWithStoredKey(t *testing.T) {
	for _, apiKey := range []string{"", "sk-s*******6789"} {
		t.Run("api_key="+apiKey, func(t *testing.T) {
			s, mock, _ := newKeyTestServer(t)
			now := time.Now()
			mock.ExpectQuery(`FROM provider_configurations\s+WHERE organization_id = \$1 AND provider_name = \$2`).
				WithArgs(testOrg, "openai").
				WillReturnRows(sqlmock.NewRows(configColumns).AddRow(testCfgID, testOrg, "openai", "sk-stored-secret-0123456789", "ui",
					[]byte(`["gpt-4o"]`), "https://llm-proxy.example.com/v1", []byte(`{}`), true, now, now, nil, "", false, nil, nil, "static", false, nil, nil))

			_, err := s.ConfigureProvider(tenantCtx(), connect.NewRequest(&providerspb.ConfigureProviderRequest{
				ProviderName:  "openai",
				ApiKey:        apiKey,
				EnabledModels: []string{"gpt-4o"},
				CustomBaseUrl: "https://attacker.example.net/v1",
			}))
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("err = %v, want InvalidArgument", err)
			}
		})
	}
}

// ReloadConfig upserts the shared YAML file into platform-owned rows and
// reloads the whole gateway, so like Get/SaveConfigYAML it is an operator
// tool that no tenant principal on a managed gateway may run.
func TestReloadConfigDeniedOnManagedGateway(t *testing.T) {
	cases := map[string]struct {
		managed bool
		ctx     context.Context
	}{
		"managed gateway":       {managed: true, ctx: tenantCtx()},
		"tenant config present": {managed: false, ctx: tenant.WithConfig(tenantCtx(), &tenant.Config{})},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			enterprise.SetManagedGateway(tc.managed)
			t.Cleanup(func() { enterprise.SetManagedGateway(false) })
			s, _, _ := newKeyTestServer(t)
			_, err := s.ReloadConfig(tc.ctx, connect.NewRequest(&providerspb.ReloadConfigRequest{}))
			if connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatalf("err = %v, want PermissionDenied", err)
			}
		})
	}
}

// The admin edit sheet resubmits the catalog base URL when the stored one is
// unset. That is the same upstream host, so a models-only edit (no key) must
// pass the base URL check; any other URL must not.
func TestConfigureProviderComparesAgainstTheCatalogDefault(t *testing.T) {
	// Development catalog loading reads model-catalog/ from the working
	// directory, so load it from the repository root.
	t.Chdir(filepath.Join("..", "..", "..", "..", ".."))
	catalog, err := provider_catalog.New()
	if err != nil {
		t.Fatalf("provider_catalog.New: %v", err)
	}
	entry, ok := catalog.GetProvider("openai")
	if !ok || entry.BaseURL == "" {
		t.Fatalf("catalog has no openai base URL: %+v", entry)
	}

	cases := []struct {
		name    string
		reqURL  string
		wantArg bool
	}{
		{name: "catalog URL resubmitted", reqURL: entry.BaseURL, wantArg: false},
		{name: "other URL", reqURL: "https://attacker.example.net/v1", wantArg: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, mock, _ := newKeyTestServer(t)
			s.catalog = catalog
			now := time.Now()
			mock.ExpectQuery(`FROM provider_configurations\s+WHERE organization_id = \$1 AND provider_name = \$2`).
				WithArgs(testOrg, "openai").
				WillReturnRows(sqlmock.NewRows(configColumns).AddRow(testCfgID, testOrg, "openai", "sk-stored-secret-0123456789", "ui",
					[]byte(`["gpt-4o"]`), nil, []byte(`{}`), true, now, now, nil, "", false, nil, nil, "static", false, nil, nil))
			// The accepted request goes on to list the tenant's providers;
			// that query is left unmocked, so it ends in an internal error
			// after the base URL check has passed.
			_, err := s.ConfigureProvider(tenantCtx(), connect.NewRequest(&providerspb.ConfigureProviderRequest{
				ProviderName:  "openai",
				EnabledModels: []string{"gpt-4o"},
				CustomBaseUrl: tc.reqURL,
			}))
			gotArg := connect.CodeOf(err) == connect.CodeInvalidArgument
			if gotArg != tc.wantArg {
				t.Fatalf("err = %v, want InvalidArgument = %v", err, tc.wantArg)
			}
		})
	}
}
