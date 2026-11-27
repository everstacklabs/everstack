package v1

import (
	"context"
	"regexp"
	"testing"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/everstacklabs/everstack/internal/commands"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/query"
	troopersquery "github.com/everstacklabs/everstack/internal/query/handlers/troopers"
	"github.com/everstacklabs/everstack/internal/trooper"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	"github.com/jmoiron/sqlx"
)

type ownedTrooperQueryBus struct{}

func (ownedTrooperQueryBus) Execute(_ context.Context, q query.Query) (interface{}, error) {
	lookup, ok := q.(*troopersquery.GetTrooperByIDQuery)
	if !ok || lookup.ID != "trooper-1" || lookup.TenantID != ownerTenant {
		return &query.Response{Data: nil}, nil
	}
	return &query.Response{Data: &troopersquery.TrooperReadModel{ID: lookup.ID, TenantID: lookup.TenantID}}, nil
}

func (ownedTrooperQueryBus) RegisterHandler(query.QueryHandler) {}

type recordingCommandBus struct {
	dispatched int
}

func (b *recordingCommandBus) Dispatch(context.Context, commands.Command) error {
	b.dispatched++
	return nil
}

func (*recordingCommandBus) RegisterHandler(commands.CommandHandler) {}

func trooperContext(tenantID string, commandBus commands.CommandBus) context.Context {
	return cqrs.WithSystem(sessionPrincipal(tenantID), &cqrs.System{
		QueryBus:   ownedTrooperQueryBus{},
		CommandBus: commandBus,
	})
}

func TestRequireOwnedTrooperReadsTheTenantScopedResponse(t *testing.T) {
	s := &Server{}

	if err := s.requireOwnedTrooper(trooperContext(otherTenant, nil), "trooper-1"); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("other tenant error = %v, want NotFound", err)
	}
	if err := s.requireOwnedTrooper(trooperContext(ownerTenant, nil), "trooper-1"); err != nil {
		t.Fatalf("owner error = %v", err)
	}
}

func TestUpdateAndDeleteTrooperCheckOwnershipBeforeDispatch(t *testing.T) {
	tests := []struct {
		name string
		call func(*Server, context.Context) error
	}{
		{
			name: "update",
			call: func(s *Server, ctx context.Context) error {
				name := "updated"
				_, err := s.UpdateTrooper(ctx, connect.NewRequest(&agentspb.UpdateTrooperRequest{Id: "trooper-1", Name: &name}))
				return err
			},
		},
		{
			name: "delete",
			call: func(s *Server, ctx context.Context) error {
				_, err := s.DeleteTrooper(ctx, connect.NewRequest(&agentspb.DeleteTrooperRequest{Id: "trooper-1"}))
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+" another tenant gets not found", func(t *testing.T) {
			bus := &recordingCommandBus{}
			err := tt.call(&Server{}, trooperContext(otherTenant, bus))
			wantNotFound(t, tt.name, err)
			if bus.dispatched != 0 {
				t.Fatalf("commands dispatched = %d", bus.dispatched)
			}
		})

		t.Run(tt.name+" owner dispatches", func(t *testing.T) {
			bus := &recordingCommandBus{}
			if err := tt.call(&Server{}, trooperContext(ownerTenant, bus)); err != nil {
				t.Fatalf("owner call: %v", err)
			}
			if bus.dispatched != 1 {
				t.Fatalf("commands dispatched = %d", bus.dispatched)
			}
		})
	}
}

func TestProvisionTrooperChecksOwnershipBeforeLoadingOrProvisioning(t *testing.T) {
	newServer := func(t *testing.T) (*Server, sqlmock.Sqlmock, *int) {
		t.Helper()
		rawDB, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = rawDB.Close() })
		provisions := 0
		s := &Server{
			db:         sqlx.NewDb(rawDB, "sqlmock"),
			trooperMgr: &trooper.Manager{},
			provisionTrooper: func(_ context.Context, cfg trooper.ProvisionConfig) (string, error) {
				if cfg.TrooperID != "trooper-1" || cfg.TenantID != ownerTenant {
					t.Fatalf("provision config = %+v", cfg)
				}
				provisions++
				return "sandbox-1", nil
			},
		}
		return s, mock, &provisions
	}

	t.Run("another tenant gets not found before a database read", func(t *testing.T) {
		s, mock, provisions := newServer(t)
		mock.ExpectQuery("FROM troopers WHERE id = \\$1").
			WillReturnRows(sqlmock.NewRows([]string{
				"name", "sandbox_image", "sandbox_cpu_limit", "sandbox_memory_mb", "sandbox_disk_mb",
				"sandbox_network_mode", "sandbox_ssh_enabled", "sandbox_git_repo_url", "sandbox_git_branch",
				"soul_md", "identity_md", "user_md", "role_md", "db_sqlite_path", "db_lancedb_path", "db_redb_path",
			}).AddRow("other", "image", 1.0, 512, 1024, "restricted", false, nil, nil, "", "", "", "", "", "", ""))
		_, err := s.ProvisionTrooper(trooperContext(otherTenant, nil), connect.NewRequest(&agentspb.ProvisionTrooperRequest{TrooperId: "trooper-1"}))
		wantNotFound(t, "ProvisionTrooper", err)
		if *provisions != 0 {
			t.Fatalf("provisions = %d", *provisions)
		}
	})

	t.Run("owner loads and provisions the trooper", func(t *testing.T) {
		s, mock, provisions := newServer(t)
		query := regexp.QuoteMeta(`
			SELECT name, sandbox_image, sandbox_cpu_limit, sandbox_memory_mb, sandbox_disk_mb,
				sandbox_network_mode, sandbox_ssh_enabled, sandbox_git_repo_url, sandbox_git_branch,
				soul_md, identity_md, user_md, role_md, db_sqlite_path, db_lancedb_path, db_redb_path
			FROM troopers WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
		`)
		mock.ExpectQuery(query).
			WithArgs("trooper-1", ownerTenant).
			WillReturnRows(sqlmock.NewRows([]string{
				"name", "sandbox_image", "sandbox_cpu_limit", "sandbox_memory_mb", "sandbox_disk_mb",
				"sandbox_network_mode", "sandbox_ssh_enabled", "sandbox_git_repo_url", "sandbox_git_branch",
				"soul_md", "identity_md", "user_md", "role_md", "db_sqlite_path", "db_lancedb_path", "db_redb_path",
			}).AddRow("trooper", "image", 1.0, 512, 1024, "restricted", false, nil, nil, "", "", "", "", "", "", ""))

		if _, err := s.ProvisionTrooper(trooperContext(ownerTenant, nil), connect.NewRequest(&agentspb.ProvisionTrooperRequest{TrooperId: "trooper-1"})); err != nil {
			t.Fatalf("owner ProvisionTrooper: %v", err)
		}
		if *provisions != 1 {
			t.Fatalf("provisions = %d", *provisions)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}
