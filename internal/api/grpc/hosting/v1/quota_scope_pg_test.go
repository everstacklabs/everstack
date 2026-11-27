package v1

import (
	"context"
	"errors"
	"testing"

	"github.com/everstacklabs/everstack/internal/hosting"
	"github.com/everstacklabs/everstack/internal/lib/pgscratch"
)

// sites / site_versions with the columns the quota reads, typed as on the
// production gateway DB (information_schema, 2026-10-06).
const hostingQuotaSchema = `
CREATE TABLE sites (id UUID PRIMARY KEY DEFAULT gen_random_uuid(), slug TEXT NOT NULL, tenant_id UUID,
	status VARCHAR(50) NOT NULL DEFAULT 'active');
CREATE TABLE site_versions (id UUID PRIMARY KEY DEFAULT gen_random_uuid(), site_id UUID NOT NULL,
	version INTEGER NOT NULL DEFAULT 1, status VARCHAR(50) NOT NULL DEFAULT 'pending', total_bytes BIGINT NOT NULL DEFAULT 0);
`

// On a managed gateway HOSTED_SITES and HOSTING_STORAGE_BYTES count every id
// the organization owns, and never another organization's sites.
func TestPGHostingQuotaCountsTheWholeOrganization(t *testing.T) {
	db := pgscratch.New(t, "hosting_quota")
	const (
		orgA  = "aaaaaaaa-0000-4000-8000-00000000000a"
		instA = "aaaaaaaa-0000-4000-8000-0000000000a1"
		orgB  = "bbbbbbbb-0000-4000-8000-00000000000b"
	)
	pgscratch.MustExec(t, db, hostingQuotaSchema,
		`WITH s AS (INSERT INTO sites (slug, tenant_id) VALUES ('a1', '`+orgA+`'), ('a2', '`+instA+`') RETURNING id)
		 INSERT INTO site_versions (site_id, status, total_bytes) SELECT id, 'finalized', 100 FROM s`,
		`INSERT INTO sites (slug, tenant_id, status) VALUES ('a-gone', '`+instA+`', 'deleted')`,
		`INSERT INTO sites (slug, tenant_id) VALUES ('b1', '`+orgB+`'), ('b2', '`+orgB+`'), ('b3', '`+orgB+`')`,
	)
	quota := hosting.TenantQuota{Tier: "free", MaxSites: 3, MaxStorageBytes: 250,
		ScopeKey: "org:" + orgA, ScopeTenantIDs: []string{orgA, instA}}

	check := func(requested hosting.TenantUsage) error {
		tx, err := db.BeginTxx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		return enforceTenantQuotaTx(context.Background(), tx, instA, quota, requested)
	}

	// A holds 2 live sites over two ids (the deleted one is free) and 200
	// bytes: one more site fits, B's three sites never count.
	if err := check(hosting.TenantUsage{Sites: 1, StorageBytes: 10}); err != nil {
		t.Fatalf("A at 2/3 sites must publish one more: %v", err)
	}
	var exceeded *hosting.QuotaExceededError
	if err := check(hosting.TenantUsage{Sites: 2}); !errors.As(err, &exceeded) || exceeded.Current != 2 {
		t.Fatalf("two more sites: err = %v, want a refusal counting A's 2 sites", err)
	}
	if err := check(hosting.TenantUsage{StorageBytes: 51}); !errors.As(err, &exceeded) ||
		exceeded.Resource != hosting.QuotaResourceStorage || exceeded.Current != 200 {
		t.Fatalf("storage past the cap: err = %v, want a storage refusal at 200 bytes", err)
	}
}
