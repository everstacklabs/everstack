package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/everstacklabs/everstack/internal/auth/sessionlive/sessionlivetest"
	"github.com/everstacklabs/everstack/internal/bots"
	"github.com/everstacklabs/everstack/internal/enterprise"
	"github.com/everstacklabs/everstack/internal/interop"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/gorilla/mux"
	"github.com/jmoiron/sqlx"
)

type rawDashboardRouter struct{ router *mux.Router }

func (r rawDashboardRouter) HandleFunc(path string, f http.HandlerFunc) { r.router.HandleFunc(path, f) }

type rawDashboardBotStore struct{}

func (rawDashboardBotStore) List(context.Context, string) ([]bots.Bot, error) {
	return []bots.Bot{}, nil
}
func (rawDashboardBotStore) Get(context.Context, string, string) (*bots.Bot, error) {
	return nil, bots.ErrNotFound
}
func (rawDashboardBotStore) Insert(context.Context, *bots.Bot) error { return bots.ErrNotFound }
func (rawDashboardBotStore) Update(context.Context, *bots.Bot) error { return bots.ErrNotFound }
func (rawDashboardBotStore) Delete(context.Context, string, string, time.Time) error {
	return bots.ErrNotFound
}
func (rawDashboardBotStore) Lock(context.Context, string, string) (func(), error) {
	return func() {}, nil
}

func TestRawDashboardCookieSessionsStandaloneAndShared(t *testing.T) {
	for _, shared := range []bool{false, true} {
		for _, path := range []string{"/api/bots", "/api/interop/adk/status"} {
			t.Run(path+map[bool]string{true: "/shared", false: "/standalone"}[shared], func(t *testing.T) {
				previous := enterprise.ManagedGateway()
				enterprise.SetManagedGateway(shared)
				defer enterprise.SetManagedGateway(previous)
				raw, mock, err := sqlmock.New()
				if err != nil {
					t.Fatal(err)
				}
				defer raw.Close()
				db := sqlx.NewDb(raw, "sqlmock")
				mock.ExpectQuery(sessionlivetest.QueryPattern).WithArgs("raw-session").WillReturnRows(sessionlivetest.LegacyRows("user-raw", time.Now().Add(time.Hour)))
				tenant := "tenant-raw"
				if shared {
					tenant = "instance-raw"
					mock.ExpectQuery(`SELECT[\s\S]*tc\.instance_id::text[\s\S]*FROM everstack\.tenant_config AS tc`).WithArgs("prod-a1b2c3").WillReturnRows(sqlmock.NewRows([]string{"instance_id", "organization_id", "organization_slug"}).AddRow(tenant, "org-raw", "org"))
					mock.ExpectQuery(`SELECT COALESCE\(role, ''\) FROM everstack\.organization_members`).WithArgs("user-raw", "org-raw").WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("admin"))
				} else {
					mock.ExpectQuery(`SELECT organization_id::text AS organization_id, COALESCE\(role, ''\) AS role[\s\S]*FROM everstack\.organization_members`).WithArgs("user-raw").WillReturnRows(sqlmock.NewRows([]string{"organization_id", "role"}).AddRow(tenant, "admin"))
				}
				router := mux.NewRouter()
				adapter := rawDashboardRouter{router}
				bots.NewHandlers(rawDashboardBotStore{}, nil, nil).Mount(adapter)
				interop.NewHandlers(interop.NewStore(nil), shared, true).Mount(adapter)
				handler := cookieSessionAuthMiddleware(func() *sqlx.DB { return db })(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if !contextkeys.IsDashboardUser(r.Context()) || contextkeys.GetTenantID(r.Context()) != tenant {
						t.Fatal("raw route did not receive verified session identity")
					}
					router.ServeHTTP(w, r)
				}))
				request := httptest.NewRequest("GET", "https://prod-a1b2c3.example.com"+path, nil)
				if !shared {
					request.Host = ""
				}
				request.AddCookie(&http.Cookie{Name: "es_everstack_session", Value: "raw-session"})
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, request)
				if w.Code != 200 {
					t.Fatalf("raw dashboard locked out: %d %s", w.Code, w.Body.String())
				}
				if err = mock.ExpectationsWereMet(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
