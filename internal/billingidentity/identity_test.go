package billingidentity

import (
	"context"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestIdentityQueriesRetainDeletedManagedInstanceAliases(t *testing.T) {
	if strings.Contains(strings.ToLower(resolveOrganizationQuery), "deleted_at") {
		t.Fatal("organization resolution must retain soft-deleted instance aliases")
	}
	if strings.Contains(strings.ToLower(listOrganizationAliasesQuery), "deleted_at") {
		t.Fatal("alias enumeration must retain soft-deleted instance aliases")
	}
	if !strings.Contains(strings.ToLower(resolveActiveOrganizationQuery), "deleted_at is null") {
		t.Fatal("active organization resolution must reject soft-deleted instances")
	}

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer db.Close()

	const (
		organizationID = "11111111-1111-1111-1111-111111111111"
		deletedID      = "22222222-2222-2222-2222-222222222222"
	)
	mock.ExpectQuery(`SELECT [$]1::text AS tenant_id[\s\S]*FROM everstack[.]managed_instances`).
		WithArgs(organizationID).
		WillReturnRows(sqlmock.NewRows([]string{"tenant_id"}).
			AddRow(organizationID).
			AddRow(deletedID))

	aliases, err := ListOrganizationAliases(context.Background(), sqlx.NewDb(db, "sqlmock"), organizationID)
	if err != nil {
		t.Fatalf("list aliases: %v", err)
	}
	if len(aliases) != 2 || aliases[0] != organizationID || aliases[1] != deletedID {
		t.Fatalf("aliases = %#v", aliases)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestResolveActiveOrganizationUsesActiveInstanceQuery(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer db.Close()

	const (
		organizationID = "11111111-1111-1111-1111-111111111111"
		instanceID     = "22222222-2222-2222-2222-222222222222"
	)
	mock.ExpectQuery(`managed_instances AS mi ON mi[.]organization_id = o[.]id AND mi[.]deleted_at IS NULL`).
		WithArgs(instanceID).
		WillReturnRows(sqlmock.NewRows([]string{"organization_id", "plan_tier"}).
			AddRow(organizationID, "basic"))

	organization, err := ResolveActiveOrganization(context.Background(), sqlx.NewDb(db, "sqlmock"), instanceID)
	if err != nil {
		t.Fatalf("resolve active organization: %v", err)
	}
	if organization.ID != organizationID || organization.Tier != "basic" {
		t.Fatalf("organization = %#v", organization)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
