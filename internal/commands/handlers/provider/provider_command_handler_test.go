package provider

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/everstacklabs/everstack/internal/commands/provider"
	"github.com/everstacklabs/everstack/internal/domain/provider_api_keys"
	"github.com/everstacklabs/everstack/internal/domain/provider_config"
)

// fakeConfigs is an in-memory provider_configurations table. A nil
// OrganizationID is a platform-owned row.
type fakeConfigs struct {
	rows map[string]*provider_config.Configuration // by id
}

func cloneConfig(c *provider_config.Configuration) *provider_config.Configuration {
	cp := *c
	if c.CustomBaseURL != nil {
		v := *c.CustomBaseURL
		cp.CustomBaseURL = &v
	}
	cp.EnabledModels = append([]string(nil), c.EnabledModels...)
	if c.CustomSettings != nil {
		cp.CustomSettings = map[string]string{}
		for k, v := range c.CustomSettings {
			cp.CustomSettings[k] = v
		}
	}
	return &cp
}

func ownedBy(c *provider_config.Configuration, orgID string) bool {
	return orgID != "" && c.OrganizationID != nil && *c.OrganizationID == orgID
}

func (f *fakeConfigs) GetForOrg(_ context.Context, orgID, providerName string) (*provider_config.Configuration, error) {
	for _, c := range f.rows {
		if ownedBy(c, orgID) && c.ProviderName == providerName {
			return cloneConfig(c), nil
		}
	}
	return nil, fmt.Errorf("provider configuration not found: %s", providerName)
}

func (f *fakeConfigs) GetByIDForOrg(_ context.Context, orgID, id string) (*provider_config.Configuration, error) {
	if c, ok := f.rows[id]; ok && ownedBy(c, orgID) {
		return cloneConfig(c), nil
	}
	return nil, provider_config.ErrNotFound
}

func (f *fakeConfigs) ListForOrg(_ context.Context, orgID string) ([]*provider_config.Configuration, error) {
	var out []*provider_config.Configuration
	for _, c := range f.rows {
		if ownedBy(c, orgID) {
			out = append(out, cloneConfig(c))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ProviderName < out[j].ProviderName })
	return out, nil
}

func (f *fakeConfigs) UpsertForOrg(_ context.Context, orgID string, config *provider_config.Configuration) error {
	if orgID == "" {
		return errors.New("org id is required")
	}
	org := orgID
	config.OrganizationID = &org
	for id, c := range f.rows {
		if ownedBy(c, orgID) && c.ProviderName == config.ProviderName {
			config.ID = id
		}
	}
	if config.ID == "" {
		config.ID = uuid.NewString()
	}
	f.rows[config.ID] = cloneConfig(config)
	return nil
}

func (f *fakeConfigs) DeleteForOrg(_ context.Context, orgID, providerName string) error {
	for id, c := range f.rows {
		if ownedBy(c, orgID) && c.ProviderName == providerName {
			delete(f.rows, id)
			return nil
		}
	}
	return fmt.Errorf("provider configuration not found: %s", providerName)
}

// fakeKeys is an in-memory provider_api_keys table. The bare-id methods
// ignore ownership, as the Postgres ones do; the *ForOrg methods join to
// fakeConfigs the way the scoped SQL joins provider_configurations.
type fakeKeys struct {
	configs *fakeConfigs
	rows    map[string]*provider_api_keys.ProviderAPIKey
}

func cloneKey(k *provider_api_keys.ProviderAPIKey) *provider_api_keys.ProviderAPIKey {
	cp := *k
	return &cp
}

func (f *fakeKeys) configOwned(orgID, configID string) bool {
	c, ok := f.configs.rows[configID]
	return ok && ownedBy(c, orgID)
}

func (f *fakeKeys) ListByProviderConfig(_ context.Context, configID string) ([]*provider_api_keys.ProviderAPIKey, error) {
	var out []*provider_api_keys.ProviderAPIKey
	for _, k := range f.rows {
		if k.ProviderConfigID == configID {
			out = append(out, cloneKey(k))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].KeyName < out[j].KeyName })
	return out, nil
}

func (f *fakeKeys) ListByProviderConfigForOrg(ctx context.Context, orgID, configID string) ([]*provider_api_keys.ProviderAPIKey, error) {
	if !f.configOwned(orgID, configID) {
		return []*provider_api_keys.ProviderAPIKey{}, nil
	}
	return f.ListByProviderConfig(ctx, configID)
}

func (f *fakeKeys) insert(key *provider_api_keys.ProviderAPIKey) error {
	for _, k := range f.rows {
		if k.ProviderConfigID == key.ProviderConfigID && k.KeyName == key.KeyName {
			return errors.New(`duplicate key value violates unique constraint "provider_api_keys_provider_config_id_key_name_key"`)
		}
	}
	if key.Source == "" {
		key.Source = "manual"
	}
	f.rows[key.ID] = cloneKey(key)
	return nil
}

func (f *fakeKeys) Create(_ context.Context, key *provider_api_keys.ProviderAPIKey) error {
	return f.insert(key)
}

func (f *fakeKeys) CreateForOrg(_ context.Context, orgID string, key *provider_api_keys.ProviderAPIKey) error {
	if !f.configOwned(orgID, key.ProviderConfigID) {
		return provider_api_keys.ErrKeyNotFound
	}
	return f.insert(key)
}

func (f *fakeKeys) Update(_ context.Context, key *provider_api_keys.ProviderAPIKey) error {
	if _, ok := f.rows[key.ID]; !ok {
		return fmt.Errorf("provider API key not found: %s", key.ID)
	}
	f.rows[key.ID] = cloneKey(key)
	return nil
}

func (f *fakeKeys) UpdateForOrg(ctx context.Context, orgID string, key *provider_api_keys.ProviderAPIKey) error {
	stored, ok := f.rows[key.ID]
	if !ok || !f.configOwned(orgID, stored.ProviderConfigID) {
		return provider_api_keys.ErrKeyNotFound
	}
	return f.Update(ctx, key)
}

func (f *fakeKeys) Delete(_ context.Context, id string) error {
	if _, ok := f.rows[id]; !ok {
		return fmt.Errorf("provider API key not found: %s", id)
	}
	delete(f.rows, id)
	return nil
}

func (f *fakeKeys) DeleteForOrg(ctx context.Context, orgID, id string) error {
	stored, ok := f.rows[id]
	if !ok || !f.configOwned(orgID, stored.ProviderConfigID) {
		return provider_api_keys.ErrKeyNotFound
	}
	return f.Delete(ctx, id)
}

func (f *fakeKeys) UpdateRateLimitData(context.Context, string, map[string]interface{}) error {
	return nil
}

func (f *fakeKeys) GetByID(_ context.Context, id string) (*provider_api_keys.ProviderAPIKey, error) {
	if k, ok := f.rows[id]; ok {
		return cloneKey(k), nil
	}
	return nil, fmt.Errorf("provider API key not found: %s", id)
}

func (f *fakeKeys) GetByIDForOrg(_ context.Context, orgID, id string) (*provider_api_keys.ProviderAPIKey, error) {
	if k, ok := f.rows[id]; ok && f.configOwned(orgID, k.ProviderConfigID) {
		return cloneKey(k), nil
	}
	return nil, provider_api_keys.ErrKeyNotFound
}

func (f *fakeKeys) UpsertConfigKey(_ context.Context, key *provider_api_keys.ProviderAPIKey) error {
	for id, k := range f.rows {
		if k.ProviderConfigID == key.ProviderConfigID && k.KeyName == key.KeyName {
			key.ID = id
		}
	}
	if key.ID == "" {
		key.ID = uuid.NewString()
	}
	key.Source = "config"
	f.rows[key.ID] = cloneKey(key)
	return nil
}

func (f *fakeKeys) DeactivateConfigKeys(context.Context, string) error { return nil }

const (
	orgA = "11111111-1111-1111-1111-11111111111a"
	orgB = "11111111-1111-1111-1111-11111111111b"

	cfgA    = "22222222-2222-2222-2222-22222222222a"
	cfgB    = "22222222-2222-2222-2222-22222222222b"
	cfgPlat = "22222222-2222-2222-2222-2222222222ff"

	keyA        = "33333333-3333-3333-3333-33333333333a"
	keyB        = "33333333-3333-3333-3333-33333333333b"
	keyBConfig  = "33333333-3333-3333-3333-3333333333bc"
	keyPlatform = "33333333-3333-3333-3333-3333333333ff"
)

func strPtr(s string) *string { return &s }

// newOwnershipFixture seeds tenant A, tenant B and a platform-owned
// (organization_id NULL) openai configuration, each with key rows.
func newOwnershipFixture() (*Handler, *fakeConfigs, *fakeKeys) {
	configs := &fakeConfigs{rows: map[string]*provider_config.Configuration{
		cfgA:    {ID: cfgA, OrganizationID: strPtr(orgA), ProviderName: "openai", APIKeyEncrypted: "sk-a", EnabledModels: []string{"gpt-4o"}, IsActive: true},
		cfgB:    {ID: cfgB, OrganizationID: strPtr(orgB), ProviderName: "openai", APIKeyEncrypted: "sk-b", EnabledModels: []string{"gpt-4o"}, IsActive: true},
		cfgPlat: {ID: cfgPlat, OrganizationID: nil, ProviderName: "openai", APIKeyEncrypted: "sk-platform", EnabledModels: []string{"gpt-4o"}, IsActive: true},
	}}
	keys := &fakeKeys{configs: configs, rows: map[string]*provider_api_keys.ProviderAPIKey{
		keyA:        {ID: keyA, ProviderConfigID: cfgA, KeyName: "a-primary", KeyEncrypted: "sk-a-primary", Weight: 1, IsActive: true, Source: "manual"},
		keyB:        {ID: keyB, ProviderConfigID: cfgB, KeyName: "b-primary", KeyEncrypted: "sk-b-primary", Weight: 1, IsActive: true, Source: "manual"},
		keyBConfig:  {ID: keyBConfig, ProviderConfigID: cfgB, KeyName: "Config API Key", KeyEncrypted: "sk-b", Weight: 1, IsActive: true, Source: "config"},
		keyPlatform: {ID: keyPlatform, ProviderConfigID: cfgPlat, KeyName: "platform", KeyEncrypted: "sk-platform", Weight: 1, IsActive: true, Source: "manual"},
	}}
	return NewHandler(configs, keys, nil), configs, keys
}

func keySnapshot(keys *fakeKeys) map[string]provider_api_keys.ProviderAPIKey {
	out := make(map[string]provider_api_keys.ProviderAPIKey, len(keys.rows))
	for id, k := range keys.rows {
		out[id] = *k
	}
	return out
}

func sameKeys(a, b map[string]provider_api_keys.ProviderAPIKey) bool {
	if len(a) != len(b) {
		return false
	}
	for id, k := range a {
		other, ok := b[id]
		if !ok || other.Weight != k.Weight || other.IsActive != k.IsActive ||
			other.KeyEncrypted != k.KeyEncrypted || other.ProviderConfigID != k.ProviderConfigID {
			return false
		}
	}
	return true
}

// A caller must not reach another tenant's, or the platform's, provider
// keys by id. Every outcome is ErrNotFound (never "already exists" or
// "cannot modify config-sourced key", which would confirm the row exists),
// and nothing is written.
func TestKeyCommandsRefuseRowsTheTenantDoesNotOwn(t *testing.T) {
	type op func(h *Handler, tenant string) error
	add := func(configID, name string) op {
		return func(h *Handler, tenant string) error {
			_, err := h.HandleAddProviderAPIKey(context.Background(), provider.AddProviderAPIKeyCommand{
				TenantID: tenant, ProviderConfigID: configID, KeyName: name, APIKey: "sk-attacker-key", Weight: 1,
			})
			return err
		}
	}
	weight := func(keyID string) op {
		return func(h *Handler, tenant string) error {
			_, err := h.HandleUpdateAPIKeyWeight(context.Background(), provider.UpdateAPIKeyWeightCommand{TenantID: tenant, KeyID: keyID, Weight: 9})
			return err
		}
	}
	toggle := func(keyID string) op {
		return func(h *Handler, tenant string) error {
			_, err := h.HandleToggleAPIKey(context.Background(), provider.ToggleAPIKeyCommand{TenantID: tenant, KeyID: keyID, IsActive: false})
			return err
		}
	}
	del := func(keyID string) op {
		return func(h *Handler, tenant string) error {
			return h.HandleDeleteProviderAPIKey(context.Background(), provider.DeleteProviderAPIKeyCommand{TenantID: tenant, KeyID: keyID})
		}
	}
	list := func(configID string) op {
		return func(h *Handler, tenant string) error {
			keys, err := h.HandleListProviderAPIKeys(context.Background(), provider.ListProviderAPIKeysQuery{TenantID: tenant, ProviderConfigID: configID})
			if err == nil && len(keys) > 0 {
				return fmt.Errorf("listed %d keys of a configuration the tenant does not own", len(keys))
			}
			return err
		}
	}

	cases := []struct {
		name string
		op   op
	}{
		{"add key to another tenant's config", add(cfgB, "new")},
		{"add key reusing another tenant's key name", add(cfgB, "b-primary")},
		{"add key to platform config", add(cfgPlat, "new")},
		{"add key to unknown config", add(uuid.NewString(), "new")},
		{"update weight of another tenant's key", weight(keyB)},
		{"update weight of another tenant's config-sourced key", weight(keyBConfig)},
		{"update weight of platform key", weight(keyPlatform)},
		{"toggle another tenant's key", toggle(keyB)},
		{"toggle another tenant's config-sourced key", toggle(keyBConfig)},
		{"toggle platform key", toggle(keyPlatform)},
		{"delete another tenant's key", del(keyB)},
		{"delete another tenant's config-sourced key", del(keyBConfig)},
		{"delete platform key", del(keyPlatform)},
		{"list another tenant's keys", list(cfgB)},
		{"list platform keys", list(cfgPlat)},
		{"list unknown config", list(uuid.NewString())},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _, keys := newOwnershipFixture()
			before := keySnapshot(keys)
			err := tc.op(h, orgA)
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("err = %v, want ErrNotFound", err)
			}
			if !sameKeys(before, keySnapshot(keys)) {
				t.Fatal("key table changed for a row the tenant does not own")
			}
		})
		t.Run(tc.name+" without tenant", func(t *testing.T) {
			h, _, keys := newOwnershipFixture()
			before := keySnapshot(keys)
			if err := tc.op(h, ""); err == nil {
				t.Fatal("err = nil, want a refusal without a tenant")
			}
			if !sameKeys(before, keySnapshot(keys)) {
				t.Fatal("key table changed without a tenant")
			}
		})
	}
}

// Without a tenant the handler refuses even ids that exist.
func TestKeyCommandsRequireTenantForOwnRows(t *testing.T) {
	h, _, keys := newOwnershipFixture()
	before := keySnapshot(keys)
	if _, err := h.HandleUpdateAPIKeyWeight(context.Background(), provider.UpdateAPIKeyWeightCommand{KeyID: keyA, Weight: 5}); err == nil {
		t.Fatal("weight update without tenant succeeded")
	}
	if _, err := h.HandleListProviderAPIKeys(context.Background(), provider.ListProviderAPIKeysQuery{ProviderConfigID: cfgA}); err == nil {
		t.Fatal("list without tenant succeeded")
	}
	if !sameKeys(before, keySnapshot(keys)) {
		t.Fatal("key table changed without a tenant")
	}
}

// The owner keeps full control of their own keys.
func TestKeyCommandsWorkForTheOwningTenant(t *testing.T) {
	ctx := context.Background()
	h, _, keys := newOwnershipFixture()

	added, err := h.HandleAddProviderAPIKey(ctx, provider.AddProviderAPIKeyCommand{TenantID: orgA, ProviderConfigID: cfgA, KeyName: "second", APIKey: "sk-a-second", Weight: 2})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := h.HandleAddProviderAPIKey(ctx, provider.AddProviderAPIKeyCommand{TenantID: orgA, ProviderConfigID: cfgA, KeyName: "second", APIKey: "sk-a-third", Weight: 1}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate name on own config: err = %v, want already exists", err)
	}

	listed, err := h.HandleListProviderAPIKeys(ctx, provider.ListProviderAPIKeysQuery{TenantID: orgA, ProviderConfigID: cfgA})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("listed %d keys, want 2", len(listed))
	}
	for _, k := range listed {
		if k.ProviderConfigID != cfgA {
			t.Fatalf("listed key of config %s", k.ProviderConfigID)
		}
	}

	if got, err := h.HandleUpdateAPIKeyWeight(ctx, provider.UpdateAPIKeyWeightCommand{TenantID: orgA, KeyID: keyA, Weight: 7}); err != nil || got.Weight != 7 {
		t.Fatalf("weight: got %+v err %v", got, err)
	}
	if got, err := h.HandleToggleAPIKey(ctx, provider.ToggleAPIKeyCommand{TenantID: orgA, KeyID: keyA, IsActive: false}); err != nil || got.IsActive {
		t.Fatalf("toggle: got %+v err %v", got, err)
	}
	if err := h.HandleDeleteProviderAPIKey(ctx, provider.DeleteProviderAPIKeyCommand{TenantID: orgA, KeyID: added.ID}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := keys.rows[added.ID]; ok {
		t.Fatal("own key not deleted")
	}
	if keys.rows[keyA].Weight != 7 || keys.rows[keyA].IsActive {
		t.Fatalf("own key not updated: %+v", keys.rows[keyA])
	}
}

const (
	catalogURL  = "https://api.openai.com/v1"
	customURL   = "https://llm-proxy.example.com/v1"
	attackerURL = "https://attacker.example.net/v1"
	storedKey   = "sk-stored-secret-0123456789"
)

type storedKeyRow struct {
	name   string
	source string
}

// newConfigureFixture seeds tenant A's openai configuration with the given
// base URL and key rows. storedURL nil means the column is NULL (the
// gateway then calls the catalog URL).
func newConfigureFixture(storedURL *string, rows []storedKeyRow) (*Handler, *fakeConfigs, *fakeKeys) {
	configs := &fakeConfigs{rows: map[string]*provider_config.Configuration{
		cfgA: {ID: cfgA, OrganizationID: strPtr(orgA), ProviderName: "openai", APIKeyEncrypted: storedKey, APIKeySource: "ui",
			EnabledModels: []string{"gpt-4o"}, CustomBaseURL: storedURL, IsActive: true},
	}}
	keys := &fakeKeys{configs: configs, rows: map[string]*provider_api_keys.ProviderAPIKey{}}
	for _, r := range rows {
		id := uuid.NewString()
		keys.rows[id] = &provider_api_keys.ProviderAPIKey{ID: id, ProviderConfigID: cfgA, KeyName: r.name, KeyEncrypted: storedKey, Weight: 1, IsActive: true, Source: r.source}
	}
	return NewHandler(configs, keys, nil), configs, keys
}

// A request that keeps the stored key must not move it to another host.
// Otherwise anyone able to call ConfigureProvider can point the stored
// secret (and every pooled key on the configuration) at a host they control.
func TestConfigureProviderBaseURLChangeNeedsAFreshKey(t *testing.T) {
	configRow := []storedKeyRow{{"Config API Key", "config"}}
	cases := []struct {
		name      string
		storedURL *string
		rows      []storedKeyRow
		reqURL    *string
		apiKey    string
		keyName   string
		wantErr   error
	}{
		{name: "masked key, same custom URL", storedURL: strPtr(customURL), rows: configRow, reqURL: strPtr(customURL), apiKey: "sk-s*******6789"},
		{name: "empty key, same custom URL", storedURL: strPtr(customURL), rows: configRow, reqURL: strPtr(customURL), apiKey: ""},
		{name: "empty key, unset URL resubmitted as catalog URL", storedURL: nil, rows: configRow, reqURL: strPtr(catalogURL), apiKey: ""},
		{name: "empty key, catalog URL cleared", storedURL: strPtr(catalogURL), rows: configRow, reqURL: nil, apiKey: ""},
		{name: "empty key, URL unset both times", storedURL: nil, rows: configRow, reqURL: nil, apiKey: ""},
		{name: "empty key, custom URL swapped", storedURL: strPtr(customURL), rows: configRow, reqURL: strPtr(attackerURL), apiKey: "", wantErr: ErrBaseURLChangeRequiresKey},
		{name: "masked key, custom URL swapped", storedURL: strPtr(customURL), rows: configRow, reqURL: strPtr(attackerURL), apiKey: "sk-s*******6789", wantErr: ErrBaseURLChangeRequiresKey},
		{name: "masked key, URL set from unset", storedURL: nil, rows: configRow, reqURL: strPtr(attackerURL), apiKey: "sk-s*******6789", wantErr: ErrBaseURLChangeRequiresKey},
		{name: "empty key, custom URL cleared", storedURL: strPtr(customURL), rows: configRow, reqURL: nil, apiKey: "", wantErr: ErrBaseURLChangeRequiresKey},
		{name: "whitespace key, custom URL swapped", storedURL: strPtr(customURL), rows: nil, reqURL: strPtr(attackerURL), apiKey: "   ", wantErr: ErrBaseURLChangeRequiresKey},
		{name: "fresh unnamed key replaces the only stored key", storedURL: strPtr(customURL), rows: configRow, reqURL: strPtr(attackerURL), apiKey: "sk-new-key-0123456789"},
		{name: "fresh named key, no stored key rows", storedURL: strPtr(customURL), rows: nil, reqURL: strPtr(attackerURL), apiKey: "sk-new-key-0123456789", keyName: "new"},
		{name: "fresh unnamed key, a manual key would follow", storedURL: strPtr(customURL), rows: []storedKeyRow{{"Config API Key", "config"}, {"backup", "manual"}}, reqURL: strPtr(attackerURL), apiKey: "sk-new-key-0123456789", wantErr: ErrBaseURLChangeWithStoredKeys},
		{name: "fresh named key, the config key would follow", storedURL: strPtr(customURL), rows: configRow, reqURL: strPtr(attackerURL), apiKey: "sk-new-key-0123456789", keyName: "new", wantErr: ErrBaseURLChangeWithStoredKeys},
		{name: "fresh key, blank name replaces the config key in place", storedURL: strPtr(customURL), rows: configRow, reqURL: strPtr(attackerURL), apiKey: "sk-new-key-0123456789", keyName: "   "},
		{name: "fresh unnamed key, a manual key, URL unchanged", storedURL: strPtr(customURL), rows: []storedKeyRow{{"backup", "manual"}}, reqURL: strPtr(customURL), apiKey: "sk-new-key-0123456789"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, configs, keys := newConfigureFixture(tc.storedURL, tc.rows)
			before := cloneConfig(configs.rows[cfgA])
			_, err := h.HandleConfigureProvider(context.Background(), provider.ConfigureProviderCommand{
				TenantID:       orgA,
				ProviderName:   "openai",
				APIKey:         tc.apiKey,
				APIKeyName:     tc.keyName,
				EnabledModels:  []string{"gpt-4o", "gpt-4o-mini"},
				CustomBaseURL:  tc.reqURL,
				DefaultBaseURL: catalogURL,
			})
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("err = %v, want success", err)
				}
				// Whenever the host moved, no stored secret may be served
				// against it: every key the configuration now carries must
				// be the one this request supplied.
				if provider_config.BaseURLChanged(tc.storedURL, tc.reqURL, catalogURL) {
					if got := configs.rows[cfgA].APIKeyEncrypted; got == storedKey {
						t.Fatal("legacy api_key_encrypted still holds the stored secret after a base URL change")
					}
					for _, k := range keys.rows {
						if k.ProviderConfigID == cfgA && k.KeyEncrypted == storedKey {
							t.Fatalf("key row %q still holds the stored secret after a base URL change", k.KeyName)
						}
					}
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			after := configs.rows[cfgA]
			if after.APIKeyEncrypted != before.APIKeyEncrypted ||
				(after.CustomBaseURL == nil) != (before.CustomBaseURL == nil) ||
				(after.CustomBaseURL != nil && *after.CustomBaseURL != *before.CustomBaseURL) {
				t.Fatalf("rejected request still changed the stored configuration: %+v", after)
			}
		})
	}
}

// A first configuration supplies its own key, so any base URL is allowed.
func TestConfigureProviderNewConfigAcceptsCustomBaseURL(t *testing.T) {
	configs := &fakeConfigs{rows: map[string]*provider_config.Configuration{}}
	keys := &fakeKeys{configs: configs, rows: map[string]*provider_api_keys.ProviderAPIKey{}}
	h := NewHandler(configs, keys, nil)
	_, err := h.HandleConfigureProvider(context.Background(), provider.ConfigureProviderCommand{
		TenantID: orgA, ProviderName: "openai", APIKey: "sk-new-key-0123456789",
		EnabledModels: []string{"gpt-4o"}, CustomBaseURL: strPtr(customURL), DefaultBaseURL: catalogURL,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
}

// The gateway merges a tenant's rows by lowercased provider name, so a second
// row spelled differently would lend its base URL to the first row's stored
// keys without ever meeting the base URL check. It must be refused, and
// nothing may be written.
func TestConfigureProviderRefusesCaseVariantOfExistingProvider(t *testing.T) {
	for _, name := range []string{"OpenAI", "OPENAI", " openai", "openai "} {
		t.Run(name, func(t *testing.T) {
			h, configs, keys := newConfigureFixture(nil, []storedKeyRow{{name: "pooled", source: "manual"}})
			before := keySnapshot(keys)
			_, err := h.HandleConfigureProvider(context.Background(), provider.ConfigureProviderCommand{
				TenantID: orgA, ProviderName: name, APIKey: "sk-attacker-key-0123456789", APIKeyName: "x",
				EnabledModels: []string{"gpt-4o"}, CustomBaseURL: strPtr(customURL), DefaultBaseURL: catalogURL,
			})
			if !errors.Is(err, ErrProviderNameCollision) {
				t.Fatalf("err = %v, want ErrProviderNameCollision", err)
			}
			if len(configs.rows) != 1 {
				t.Fatalf("a second configuration was written: %d rows", len(configs.rows))
			}
			if !sameKeys(before, keySnapshot(keys)) {
				t.Fatal("key rows changed on a refused request")
			}
		})
	}
}

// Another tenant's row with a similar name is not a collision.
func TestConfigureProviderCaseVariantOfAnotherTenantIsAllowed(t *testing.T) {
	h, configs, _ := newConfigureFixture(nil, nil)
	_, err := h.HandleConfigureProvider(context.Background(), provider.ConfigureProviderCommand{
		TenantID: orgB, ProviderName: "OpenAI", APIKey: "sk-new-key-0123456789",
		EnabledModels: []string{"gpt-4o"}, DefaultBaseURL: catalogURL,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(configs.rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(configs.rows))
	}
}
