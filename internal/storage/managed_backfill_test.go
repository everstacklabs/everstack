package storage

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type managedBackfillTenantSource struct {
	pages map[string][]string
	after []string
	err   error
}

func (s *managedBackfillTenantSource) ListManagedTenantIDs(_ context.Context, after string, _ int) ([]string, error) {
	s.after = append(s.after, after)
	if s.err != nil {
		return nil, s.err
	}
	return append([]string(nil), s.pages[after]...), nil
}

type managedBackfillEnsurer struct {
	tenants []string
	failOn  string
}

func (e *managedBackfillEnsurer) EnsureDefault(_ context.Context, tenantID string) (*ManagedConnection, error) {
	if tenantID == e.failOn {
		return nil, errors.New("write failed")
	}
	e.tenants = append(e.tenants, tenantID)
	return &ManagedConnection{TenantID: tenantID}, nil
}

func TestBackfillManagedDefaultsEnsuresEveryTenantAcrossBoundedPages(t *testing.T) {
	source := &managedBackfillTenantSource{pages: map[string][]string{
		"":           {"instance-1", "instance-2"},
		"instance-2": {"instance-3"},
		"instance-3": {},
	}}
	ensurer := &managedBackfillEnsurer{}

	report, err := BackfillManagedDefaults(context.Background(), source, ensurer, 2)
	if err != nil {
		t.Fatalf("BackfillManagedDefaults() error = %v", err)
	}
	if report.TenantsScanned != 3 || report.DefaultsEnsured != 3 {
		t.Fatalf("report = %#v, want 3 tenants and 3 defaults", report)
	}
	if want := []string{"instance-1", "instance-2", "instance-3"}; !reflect.DeepEqual(ensurer.tenants, want) {
		t.Fatalf("ensured tenants = %#v, want %#v", ensurer.tenants, want)
	}
	if want := []string{"", "instance-2", "instance-3"}; !reflect.DeepEqual(source.after, want) {
		t.Fatalf("page cursors = %#v, want %#v", source.after, want)
	}
}

func TestBackfillManagedDefaultsReturnsProgressAndStopsOnFailure(t *testing.T) {
	source := &managedBackfillTenantSource{pages: map[string][]string{
		"": {"instance-1", "instance-2", "instance-3"},
	}}
	ensurer := &managedBackfillEnsurer{failOn: "instance-2"}

	report, err := BackfillManagedDefaults(context.Background(), source, ensurer, 100)
	if err == nil {
		t.Fatal("BackfillManagedDefaults() error = nil, want failure")
	}
	if report.TenantsScanned != 2 || report.DefaultsEnsured != 1 {
		t.Fatalf("report = %#v, want two scanned and one ensured", report)
	}
	if want := []string{"instance-1"}; !reflect.DeepEqual(ensurer.tenants, want) {
		t.Fatalf("ensured tenants = %#v, want %#v", ensurer.tenants, want)
	}
}

func TestBackfillManagedDefaultsRejectsInvalidDependencies(t *testing.T) {
	source := &managedBackfillTenantSource{}
	ensurer := &managedBackfillEnsurer{}
	tests := []struct {
		name     string
		source   ManagedTenantSource
		defaults ManagedDefaultEnsurer
		batch    int
	}{
		{name: "missing tenant source", defaults: ensurer, batch: 100},
		{name: "missing default ensurer", source: source, batch: 100},
		{name: "invalid batch size", source: source, defaults: ensurer},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := BackfillManagedDefaults(context.Background(), test.source, test.defaults, test.batch); err == nil {
				t.Fatal("BackfillManagedDefaults() error = nil, want validation failure")
			}
		})
	}
}
