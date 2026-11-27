package toolloop

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	functionsquery "github.com/everstacklabs/everstack/internal/query/handlers/functions"
	"github.com/jmoiron/sqlx"
)

type ownershipLookup struct {
	entries map[string]*functionsquery.FunctionReadModel
	tenants []string
}

func (l *ownershipLookup) GetFunctionByName(_ context.Context, name, tenant string) (*functionsquery.FunctionReadModel, error) {
	l.tenants = append(l.tenants, tenant)
	if fn := l.entries[name]; fn != nil {
		return fn, nil
	}
	return nil, sql.ErrNoRows
}
func TestLiveToolOwnershipPreflight(t *testing.T) {
	for _, tc := range []struct {
		name  string
		calls []string
		want  bool
	}{{"client only", []string{"weather"}, false}, {"registered", []string{"owned"}, true}, {"mixed", []string{"owned", "weather"}, false}, {"disabled", []string{"disabled"}, false}, {"another tenant", []string{"foreign"}, false}, {"no calls", nil, false}} {
		t.Run(tc.name, func(t *testing.T) {
			lookup := &ownershipLookup{entries: map[string]*functionsquery.FunctionReadModel{"owned": {Name: "owned", TenantID: "tenant-a", Enabled: true}, "disabled": {TenantID: "tenant-a"}, "foreign": {TenantID: "tenant-b", Enabled: true}}}
			m := &LoopManager{enabled: true, handler: NewHandler(lookup, DefaultConfig())}
			resp := &gw.ChatCompletionResponse{Choices: []gw.Choice{{FinishReason: "tool_calls"}}}
			for _, name := range tc.calls {
				resp.Choices[0].Message.ToolCalls = append(resp.Choices[0].Message.ToolCalls, gw.ToolCall{Type: "function", Function: gw.ToolCallFunction{Name: name}})
			}
			if got := m.CanExecuteToolLoop(context.Background(), "tenant-a", resp); got != tc.want {
				t.Fatal(got, tc.want)
			}
			for _, tenant := range lookup.tenants {
				if tenant != "tenant-a" {
					t.Fatal(tenant)
				}
			}
			if m.CanExecuteToolLoop(context.Background(), "", resp) {
				t.Fatal("missing tenant admitted")
			}
		})
	}
}
func TestLiveOwnershipChecksAllChoices(t *testing.T) {
	lookup := &ownershipLookup{entries: map[string]*functionsquery.FunctionReadModel{"owned": {TenantID: "a", Enabled: true}}}
	m := &LoopManager{enabled: true, handler: NewHandler(lookup, DefaultConfig())}
	resp := &gw.ChatCompletionResponse{Choices: []gw.Choice{{Message: gw.Message{ToolCalls: []gw.ToolCall{{Function: gw.ToolCallFunction{Name: "owned"}}}}}, {Message: gw.Message{ToolCalls: []gw.ToolCall{{Function: gw.ToolCallFunction{Name: "client"}}}}}}}
	if m.CanExecuteToolLoop(context.Background(), "a", resp) {
		t.Fatal("mixed choices admitted")
	}
}
func TestLiveDBFunctionLookupTenantFilter(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	lookup := NewDBFunctionLookup(sqlx.NewDb(db, "sqlmock"))
	const query = `SELECT \* FROM functions\s+WHERE name = \$1 AND tenant_id = \$2 AND enabled = true`
	mock.ExpectQuery(query).WithArgs("weather", "tenant-a").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "tenant_id", "enabled"}).AddRow("fn-a", "weather", "tenant-a", true))
	fn, err := lookup.GetFunctionByName(context.Background(), "weather", "tenant-a")
	if err != nil || fn.TenantID != "tenant-a" {
		t.Fatal(fn, err)
	}
	mock.ExpectQuery(query).WithArgs("weather", "tenant-b").WillReturnError(sql.ErrNoRows)
	if _, err = lookup.GetFunctionByName(context.Background(), "weather", "tenant-b"); err == nil {
		t.Fatal("cross tenant lookup succeeded")
	}
	if _, err = lookup.GetFunctionByName(context.Background(), "weather", ""); err == nil {
		t.Fatal("unscoped lookup succeeded")
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSingleTenantLookupFindsLegacyOwnerButNeverGuesses(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	lookup := NewDBFunctionLookup(sqlx.NewDb(db, "sqlmock"))
	const scoped = `SELECT \* FROM functions\s+WHERE name = \$1 AND tenant_id = \$2 AND enabled = true`
	const byName = `SELECT \* FROM functions WHERE name = \$1 AND enabled = true LIMIT 2`
	cols := []string{"id", "name", "tenant_id", "enabled"}

	// Managed (the default): a miss stays a miss, no second query.
	mock.ExpectQuery(scoped).WithArgs("weather", "request-tenant").WillReturnError(sql.ErrNoRows)
	if _, err = lookup.GetFunctionByName(context.Background(), "weather", "request-tenant"); err == nil {
		t.Fatal("managed gateway resolved a function outside the request tenant")
	}

	SetSingleTenant(true)
	defer SetSingleTenant(false)

	// Self-hosted: one function of that name under an older tenant id is found.
	mock.ExpectQuery(scoped).WithArgs("weather", "request-tenant").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(byName).WithArgs("weather").WillReturnRows(sqlmock.NewRows(cols).AddRow("fn-old", "weather", "license-org", true))
	fn, err := lookup.GetFunctionByName(context.Background(), "weather", "request-tenant")
	if err != nil || fn.ID != "fn-old" {
		t.Fatal(fn, err)
	}

	// Two functions with that name: refused, not guessed.
	mock.ExpectQuery(scoped).WithArgs("weather", "request-tenant").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(byName).WithArgs("weather").WillReturnRows(sqlmock.NewRows(cols).AddRow("fn-1", "weather", "a", true).AddRow("fn-2", "weather", "b", true))
	if _, err = lookup.GetFunctionByName(context.Background(), "weather", "request-tenant"); err == nil {
		t.Fatal("ambiguous name resolved")
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
