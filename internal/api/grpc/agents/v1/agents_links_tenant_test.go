package v1

import (
	"context"
	"regexp"
	"testing"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/query"
	agentsquery "github.com/everstacklabs/everstack/internal/query/handlers/agents"
	troopersquery "github.com/everstacklabs/everstack/internal/query/handlers/troopers"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	"github.com/jmoiron/sqlx"
)

type linkSweepQueryBus struct {
	listCalls int
}

func (b *linkSweepQueryBus) Execute(_ context.Context, q query.Query) (interface{}, error) {
	var data interface{}
	switch value := q.(type) {
	case *agentsquery.GetAgentByIDQuery:
		if value.TenantID == ownerTenant && (value.ID == "agent-1" || value.ID == "agent-2") {
			data = &agentsquery.AgentDefinitionReadModel{ID: value.ID, TenantID: value.TenantID}
		}
	case *troopersquery.GetTrooperByIDQuery:
		if value.TenantID == ownerTenant && (value.ID == "trooper-1" || value.ID == "trooper-2") {
			data = &troopersquery.TrooperReadModel{ID: value.ID, TenantID: value.TenantID}
		}
	case *agentsquery.ListAgentLinksQuery:
		b.listCalls++
		data = []agentsquery.AgentLinkReadModel{}
	case *agentsquery.ListAgentChannelBindingsQuery:
		b.listCalls++
		data = []agentsquery.AgentChannelBindingReadModel{}
	case *troopersquery.ListTrooperLinksQuery:
		b.listCalls++
		data = []troopersquery.TrooperLinkReadModel{}
	case *troopersquery.ListChannelBindingsQuery:
		b.listCalls++
		data = []troopersquery.TrooperChannelBindingReadModel{}
	}
	return &query.Response{Data: data}, nil
}

func (*linkSweepQueryBus) RegisterHandler(query.QueryHandler) {}

func linkSweepContext(bus query.QueryBus, commands *recordingCommandBus) context.Context {
	return cqrs.WithSystem(sessionPrincipal(ownerTenant), &cqrs.System{QueryBus: bus, CommandBus: commands})
}

func ownedChannelConfig(context.Context, string, string) error { return nil }

func TestAgentLinkAndBindingHandlersRequireOwnedAgent(t *testing.T) {
	tests := []struct {
		name    string
		foreign func(*Server, context.Context) error
		owner   func(*Server, context.Context) error
		lists   bool
	}{
		{
			name: "create link",
			foreign: func(s *Server, ctx context.Context) error {
				_, err := s.CreateAgentLink(ctx, connect.NewRequest(&agentspb.CreateAgentLinkRequest{SourceAgentId: "foreign-agent", TargetType: "agent", TargetId: "agent-2"}))
				return err
			},
			owner: func(s *Server, ctx context.Context) error {
				_, err := s.CreateAgentLink(ctx, connect.NewRequest(&agentspb.CreateAgentLinkRequest{SourceAgentId: "agent-1", TargetType: "agent", TargetId: "agent-2"}))
				return err
			},
		},
		{
			name: "list links",
			foreign: func(s *Server, ctx context.Context) error {
				_, err := s.ListAgentLinks(ctx, connect.NewRequest(&agentspb.ListAgentLinksRequest{AgentId: "foreign-agent"}))
				return err
			},
			owner: func(s *Server, ctx context.Context) error {
				_, err := s.ListAgentLinks(ctx, connect.NewRequest(&agentspb.ListAgentLinksRequest{AgentId: "agent-1"}))
				return err
			},
			lists: true,
		},
		{
			name: "bind channel",
			foreign: func(s *Server, ctx context.Context) error {
				_, err := s.BindAgentChannel(ctx, connect.NewRequest(&agentspb.BindAgentChannelRequest{AgentId: "foreign-agent", ChannelConfigId: "channel-1"}))
				return err
			},
			owner: func(s *Server, ctx context.Context) error {
				_, err := s.BindAgentChannel(ctx, connect.NewRequest(&agentspb.BindAgentChannelRequest{AgentId: "agent-1", ChannelConfigId: "channel-1"}))
				return err
			},
		},
		{
			name: "unbind channel",
			foreign: func(s *Server, ctx context.Context) error {
				_, err := s.UnbindAgentChannel(ctx, connect.NewRequest(&agentspb.UnbindAgentChannelRequest{AgentId: "foreign-agent", ChannelConfigId: "channel-1"}))
				return err
			},
			owner: func(s *Server, ctx context.Context) error {
				_, err := s.UnbindAgentChannel(ctx, connect.NewRequest(&agentspb.UnbindAgentChannelRequest{AgentId: "agent-1", ChannelConfigId: "channel-1"}))
				return err
			},
		},
		{
			name: "list channel bindings",
			foreign: func(s *Server, ctx context.Context) error {
				_, err := s.ListAgentChannelBindings(ctx, connect.NewRequest(&agentspb.ListAgentChannelBindingsRequest{AgentId: "foreign-agent"}))
				return err
			},
			owner: func(s *Server, ctx context.Context) error {
				_, err := s.ListAgentChannelBindings(ctx, connect.NewRequest(&agentspb.ListAgentChannelBindingsRequest{AgentId: "agent-1"}))
				return err
			},
			lists: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+" refuses a foreign agent", func(t *testing.T) {
			bus := &linkSweepQueryBus{}
			commands := &recordingCommandBus{}
			s := &Server{requireChannelConfig: ownedChannelConfig}
			wantNotFound(t, tt.name, tt.foreign(s, linkSweepContext(bus, commands)))
			if bus.listCalls != 0 || commands.dispatched != 0 {
				t.Fatalf("list calls = %d, dispatches = %d", bus.listCalls, commands.dispatched)
			}
		})

		t.Run(tt.name+" works for the owner", func(t *testing.T) {
			bus := &linkSweepQueryBus{}
			commands := &recordingCommandBus{}
			s := &Server{requireChannelConfig: ownedChannelConfig}
			if err := tt.owner(s, linkSweepContext(bus, commands)); err != nil {
				t.Fatalf("owner call: %v", err)
			}
			if tt.lists && bus.listCalls != 1 {
				t.Fatalf("list calls = %d", bus.listCalls)
			}
			if !tt.lists && commands.dispatched != 1 {
				t.Fatalf("dispatches = %d", commands.dispatched)
			}
		})
	}

	t.Run("foreign target agent is not found before link creation", func(t *testing.T) {
		bus := &linkSweepQueryBus{}
		commands := &recordingCommandBus{}
		s := &Server{requireChannelConfig: ownedChannelConfig}
		_, err := s.CreateAgentLink(linkSweepContext(bus, commands), connect.NewRequest(&agentspb.CreateAgentLinkRequest{
			SourceAgentId: "agent-1", TargetType: "agent", TargetId: "foreign-agent",
		}))
		wantNotFound(t, "CreateAgentLink target", err)
		if commands.dispatched != 0 {
			t.Fatalf("dispatches = %d", commands.dispatched)
		}
	})

	t.Run("foreign channel config is not found before binding", func(t *testing.T) {
		bus := &linkSweepQueryBus{}
		commands := &recordingCommandBus{}
		checks := 0
		s := &Server{requireChannelConfig: func(context.Context, string, string) error {
			checks++
			return connect.NewError(connect.CodeNotFound, context.Canceled)
		}}
		_, err := s.BindAgentChannel(linkSweepContext(bus, commands), connect.NewRequest(&agentspb.BindAgentChannelRequest{
			AgentId: "agent-1", ChannelConfigId: "foreign-channel",
		}))
		wantNotFound(t, "BindAgentChannel config", err)
		if checks != 1 || commands.dispatched != 0 {
			t.Fatalf("checks = %d, dispatches = %d", checks, commands.dispatched)
		}
	})
}

func TestTrooperLinkAndBindingHandlersRequireOwnedTrooper(t *testing.T) {
	tests := []struct {
		name    string
		foreign func(*Server, context.Context) error
		owner   func(*Server, context.Context) error
		lists   bool
	}{
		{"create link", func(s *Server, ctx context.Context) error {
			_, err := s.CreateTrooperLink(ctx, connect.NewRequest(&agentspb.CreateTrooperLinkRequest{SourceTrooperId: "foreign-trooper", TargetType: "trooper", TargetId: "trooper-2"}))
			return err
		}, func(s *Server, ctx context.Context) error {
			_, err := s.CreateTrooperLink(ctx, connect.NewRequest(&agentspb.CreateTrooperLinkRequest{SourceTrooperId: "trooper-1", TargetType: "trooper", TargetId: "trooper-2"}))
			return err
		}, false},
		{"list links", func(s *Server, ctx context.Context) error {
			_, err := s.ListTrooperLinks(ctx, connect.NewRequest(&agentspb.ListTrooperLinksRequest{TrooperId: "foreign-trooper"}))
			return err
		}, func(s *Server, ctx context.Context) error {
			_, err := s.ListTrooperLinks(ctx, connect.NewRequest(&agentspb.ListTrooperLinksRequest{TrooperId: "trooper-1"}))
			return err
		}, true},
		{"bind channel", func(s *Server, ctx context.Context) error {
			_, err := s.BindChannelToTrooper(ctx, connect.NewRequest(&agentspb.BindChannelToTrooperRequest{TrooperId: "foreign-trooper", ChannelConfigId: "channel-1"}))
			return err
		}, func(s *Server, ctx context.Context) error {
			_, err := s.BindChannelToTrooper(ctx, connect.NewRequest(&agentspb.BindChannelToTrooperRequest{TrooperId: "trooper-1", ChannelConfigId: "channel-1"}))
			return err
		}, false},
		{"unbind channel", func(s *Server, ctx context.Context) error {
			_, err := s.UnbindChannelFromTrooper(ctx, connect.NewRequest(&agentspb.UnbindChannelFromTrooperRequest{TrooperId: "foreign-trooper", ChannelConfigId: "channel-1"}))
			return err
		}, func(s *Server, ctx context.Context) error {
			_, err := s.UnbindChannelFromTrooper(ctx, connect.NewRequest(&agentspb.UnbindChannelFromTrooperRequest{TrooperId: "trooper-1", ChannelConfigId: "channel-1"}))
			return err
		}, false},
		{"list channel bindings", func(s *Server, ctx context.Context) error {
			_, err := s.ListTrooperChannelBindings(ctx, connect.NewRequest(&agentspb.ListTrooperChannelBindingsRequest{TrooperId: "foreign-trooper"}))
			return err
		}, func(s *Server, ctx context.Context) error {
			_, err := s.ListTrooperChannelBindings(ctx, connect.NewRequest(&agentspb.ListTrooperChannelBindingsRequest{TrooperId: "trooper-1"}))
			return err
		}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name+" refuses a foreign trooper", func(t *testing.T) {
			bus := &linkSweepQueryBus{}
			commands := &recordingCommandBus{}
			s := &Server{requireChannelConfig: ownedChannelConfig}
			wantNotFound(t, tt.name, tt.foreign(s, linkSweepContext(bus, commands)))
			if bus.listCalls != 0 || commands.dispatched != 0 {
				t.Fatalf("list calls = %d, dispatches = %d", bus.listCalls, commands.dispatched)
			}
		})
		t.Run(tt.name+" works for the owner", func(t *testing.T) {
			bus := &linkSweepQueryBus{}
			commands := &recordingCommandBus{}
			s := &Server{requireChannelConfig: ownedChannelConfig}
			if err := tt.owner(s, linkSweepContext(bus, commands)); err != nil {
				t.Fatalf("owner call: %v", err)
			}
			if tt.lists && bus.listCalls != 1 {
				t.Fatalf("list calls = %d", bus.listCalls)
			}
			if !tt.lists && commands.dispatched != 1 {
				t.Fatalf("dispatches = %d", commands.dispatched)
			}
		})
	}

	t.Run("foreign target trooper is not found before link creation", func(t *testing.T) {
		bus := &linkSweepQueryBus{}
		commands := &recordingCommandBus{}
		s := &Server{requireChannelConfig: ownedChannelConfig}
		_, err := s.CreateTrooperLink(linkSweepContext(bus, commands), connect.NewRequest(&agentspb.CreateTrooperLinkRequest{
			SourceTrooperId: "trooper-1", TargetType: "trooper", TargetId: "foreign-trooper",
		}))
		wantNotFound(t, "CreateTrooperLink target", err)
		if commands.dispatched != 0 {
			t.Fatalf("dispatches = %d", commands.dispatched)
		}
	})

	t.Run("foreign channel config is not found before binding", func(t *testing.T) {
		bus := &linkSweepQueryBus{}
		commands := &recordingCommandBus{}
		checks := 0
		s := &Server{requireChannelConfig: func(context.Context, string, string) error {
			checks++
			return connect.NewError(connect.CodeNotFound, context.Canceled)
		}}
		_, err := s.BindChannelToTrooper(linkSweepContext(bus, commands), connect.NewRequest(&agentspb.BindChannelToTrooperRequest{
			TrooperId: "trooper-1", ChannelConfigId: "foreign-channel",
		}))
		wantNotFound(t, "BindChannelToTrooper config", err)
		if checks != 1 || commands.dispatched != 0 {
			t.Fatalf("checks = %d, dispatches = %d", checks, commands.dispatched)
		}
	})
}

func TestCreateLinksRejectUnknownTargetTypes(t *testing.T) {
	tests := []struct {
		name string
		call func(*Server, context.Context) error
	}{
		{
			name: "agent link",
			call: func(s *Server, ctx context.Context) error {
				_, err := s.CreateAgentLink(ctx, connect.NewRequest(&agentspb.CreateAgentLinkRequest{
					SourceAgentId: "agent-1", TargetType: "external", TargetId: "remote",
				}))
				return err
			},
		},
		{
			name: "trooper link",
			call: func(s *Server, ctx context.Context) error {
				_, err := s.CreateTrooperLink(ctx, connect.NewRequest(&agentspb.CreateTrooperLinkRequest{
					SourceTrooperId: "trooper-1", TargetType: "external", TargetId: "remote",
				}))
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commands := &recordingCommandBus{}
			err := tt.call(&Server{}, linkSweepContext(&linkSweepQueryBus{}, commands))
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("error = %v, want InvalidArgument", err)
			}
			if commands.dispatched != 0 {
				t.Fatalf("dispatches = %d", commands.dispatched)
			}
		})
	}
}

func TestCreateTrooperLinkAllowsHumanTarget(t *testing.T) {
	commands := &recordingCommandBus{}
	_, err := (&Server{}).CreateTrooperLink(linkSweepContext(&linkSweepQueryBus{}, commands), connect.NewRequest(&agentspb.CreateTrooperLinkRequest{
		SourceTrooperId: "trooper-1",
		TargetType:      "human",
		TargetId:        "user-1",
	}))
	if err != nil {
		t.Fatalf("CreateTrooperLink: %v", err)
	}
	if commands.dispatched != 1 {
		t.Fatalf("dispatches = %d", commands.dispatched)
	}
}

func TestRequireOwnedChannelConfigQueriesTheTenantScope(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	s := &Server{db: sqlx.NewDb(rawDB, "sqlmock")}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS(SELECT 1 FROM channel_configs WHERE id = $1 AND tenant_id = $2)")).
		WithArgs("channel-1", ownerTenant).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	err = s.requireOwnedChannelConfig(context.Background(), "channel-1", ownerTenant)
	wantNotFound(t, "channel config", err)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetTrooperReadsTheOwnedTrooper(t *testing.T) {
	t.Run("foreign trooper is not found", func(t *testing.T) {
		bus := &linkSweepQueryBus{}
		commands := &recordingCommandBus{}
		s := &Server{}
		_, err := s.GetTrooper(linkSweepContext(bus, commands), connect.NewRequest(&agentspb.GetTrooperRequest{Id: "foreign-trooper"}))
		wantNotFound(t, "GetTrooper", err)
	})

	t.Run("owner reads the trooper", func(t *testing.T) {
		bus := &linkSweepQueryBus{}
		commands := &recordingCommandBus{}
		s := &Server{}
		if _, err := s.GetTrooper(linkSweepContext(bus, commands), connect.NewRequest(&agentspb.GetTrooperRequest{Id: "trooper-1"})); err != nil {
			t.Fatalf("owner GetTrooper: %v", err)
		}
	})
}

func TestDeleteLinksRequiresOwnedLink(t *testing.T) {
	tests := []struct {
		name  string
		query string
		call  func(*Server, context.Context) error
	}{
		{"agent", `SELECT EXISTS(SELECT 1 FROM agent_links WHERE id = $1 AND tenant_id = $2)`, func(s *Server, ctx context.Context) error {
			_, err := s.DeleteAgentLink(ctx, connect.NewRequest(&agentspb.DeleteAgentLinkRequest{LinkId: "link-1"}))
			return err
		}},
		{"trooper", `SELECT EXISTS(SELECT 1 FROM trooper_links WHERE id = $1 AND tenant_id = $2)`, func(s *Server, ctx context.Context) error {
			_, err := s.DeleteTrooperLink(ctx, connect.NewRequest(&agentspb.DeleteTrooperLinkRequest{LinkId: "link-1"}))
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name+" foreign link is not found before dispatch", func(t *testing.T) {
			raw, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			commands := &recordingCommandBus{}
			s := &Server{db: sqlx.NewDb(raw, "sqlmock")}
			mock.ExpectQuery(regexp.QuoteMeta(tt.query)).WithArgs("link-1", ownerTenant).
				WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
			wantNotFound(t, tt.name, tt.call(s, linkSweepContext(&linkSweepQueryBus{}, commands)))
			if commands.dispatched != 0 {
				t.Fatalf("dispatches = %d", commands.dispatched)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})

		t.Run(tt.name+" owner deletes the link", func(t *testing.T) {
			raw, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			commands := &recordingCommandBus{}
			s := &Server{db: sqlx.NewDb(raw, "sqlmock")}
			mock.ExpectQuery(regexp.QuoteMeta(tt.query)).WithArgs("link-1", ownerTenant).
				WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
			if err := tt.call(s, linkSweepContext(&linkSweepQueryBus{}, commands)); err != nil {
				t.Fatalf("owner call: %v", err)
			}
			if commands.dispatched != 1 {
				t.Fatalf("dispatches = %d", commands.dispatched)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
