package billing

// Spend limits v2 at dispatch (docs/design/spend-limits-v2.md 4.3, slice
// S3): every attempt, platform and BYOK, holds its estimate in P while it
// runs and is evaluated in shadow. Nothing here may refuse, delay or alter
// an attempt.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/everstacklabs/everstack/internal/billingmeter"
	"github.com/everstacklabs/everstack/internal/database/migrations"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// budgetSink is a fakeSink with the v2 capability: it records every
// AdmitSpend and counts the holds still open (P's lifetime at the seam; the
// meter's own P arithmetic is tested in billingmeter).
type budgetSink struct {
	*fakeSink
	mu      sync.Mutex
	admits  []billingmeter.SpendAttempt
	holds   int
	panicky bool
}

func (s *budgetSink) AdmitSpend(_ context.Context, a billingmeter.SpendAttempt) billingmeter.PendingSpend {
	if s.panicky {
		panic("spend evaluator bug")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.admits = append(s.admits, a)
	s.holds++
	return billingmeter.PendingSpend{}
}

func (s *budgetSink) ReleaseSpend(billingmeter.PendingSpend) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.holds--
}

func (s *budgetSink) open() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.holds
}

func TestSpendShadowBYOKHoldsListPriceEstimateAndReleases(t *testing.T) {
	cat := attributionCatalog(t)
	sink := &budgetSink{fakeSink: newSink()}
	inner := &fakeProvider{usage: gw.Usage{PromptTokens: 10, CompletionTokens: 5}}
	p := Wrap(inner, byokCfg(sink, cat))
	req := gw.ChatCompletionRequest{Model: "priced-1", Messages: []gw.Message{{Role: "user", Content: text("hello there")}}}
	req.Sampling.MaxTokens = 200

	resp, err := p.Chat(keyCtx("key-1"), req)
	if err != nil || resp.Model != "priced-1" {
		t.Fatalf("Chat = %+v, %v", resp, err)
	}
	if len(sink.admits) != 1 {
		t.Fatalf("admits = %d, want 1", len(sink.admits))
	}
	a := sink.admits[0]
	want := listPrice(t, cat, "priced-1", estimatePromptTokens(req), 200, 0, 0)
	if a.OrgID != "org-a" || a.APIKeyID != "key-1" || a.EstimateMicros != want || a.Internal {
		t.Fatalf("admit = %+v, want org-a key-1 list-price estimate %d", a, want)
	}
	if open := sink.open(); open != 0 {
		t.Fatalf("holds open after the attempt = %d, want 0", open)
	}
}

// A platform attempt holds what it settles as: the marked-up G1 estimate.
func TestSpendShadowPlatformHoldsMarkedUpEstimate(t *testing.T) {
	sink := &budgetSink{fakeSink: newSink()}
	p := Wrap(&fakeProvider{usage: gw.Usage{PromptTokens: 10, CompletionTokens: 5}}, platformCfg(sink, admissionCatalog(t)))
	req := gw.ChatCompletionRequest{Model: "priced-1", Messages: []gw.Message{{Role: "user", Content: text("hi")}}}
	req.Sampling.MaxTokens = 100
	if _, err := p.Chat(tenantCtx("tenant-a"), req); err != nil {
		t.Fatal(err)
	}
	if len(sink.admits) != 1 || len(sink.fakeSink.admits) != 1 {
		t.Fatalf("admits = %d v2, %d G1", len(sink.admits), len(sink.fakeSink.admits))
	}
	if got, g1 := sink.admits[0].EstimateMicros, sink.fakeSink.admits[0].EstimateMicros; got != g1 || got <= 0 {
		t.Fatalf("platform hold %d, want the G1 estimate %d", got, g1)
	}
}

// P is released however the attempt ends: provider error, client gone
// mid-stream, cancelled request, and a panic in the caller's chunk handler.
func TestSpendShadowReleasesOnErrorsCancelsAndPanics(t *testing.T) {
	cat := attributionCatalog(t)
	req := gw.ChatCompletionRequest{Model: "priced-1", Messages: []gw.Message{{Role: "user", Content: text("x")}}}
	cases := map[string]func(p gw.Provider){
		"provider error": func(p gw.Provider) { _, _ = p.Chat(keyCtx("k"), req) },
		"client gone mid-stream": func(p gw.Provider) {
			_ = p.ChatStream(keyCtx("k"), req, func(gw.ChatResponseChunk) error { return errors.New("write: broken pipe") })
		},
		"cancelled request": func(p gw.Provider) {
			ctx, cancel := context.WithCancel(keyCtx("k"))
			cancel()
			_ = p.ChatStream(ctx, req, func(gw.ChatResponseChunk) error { return ctx.Err() })
		},
		"panic in onChunk": func(p gw.Provider) {
			defer func() { _ = recover() }()
			_ = p.ChatStream(keyCtx("k"), req, func(gw.ChatResponseChunk) error { panic("handler bug") })
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			sink := &budgetSink{fakeSink: newSink()}
			inner := &fakeProvider{usage: gw.Usage{PromptTokens: 3, CompletionTokens: 2}}
			if name == "provider error" {
				inner.err = errors.New("upstream 500")
			}
			run(Wrap(inner, byokCfg(sink, cat)))
			if len(sink.admits) != 1 {
				t.Fatalf("admits = %d, want 1", len(sink.admits))
			}
			if open := sink.open(); open != 0 {
				t.Fatalf("P leaked: %d holds still open", open)
			}
		})
	}
}

// A bug in the spend evaluator never reaches the attempt.
func TestSpendShadowPanicIsContained(t *testing.T) {
	sink := &budgetSink{fakeSink: newSink(), panicky: true}
	inner := &fakeProvider{usage: gw.Usage{PromptTokens: 3, CompletionTokens: 2}}
	before := testCounter(t, spendShadowPanics)
	resp, err := Wrap(inner, byokCfg(sink, attributionCatalog(t))).Chat(keyCtx("k"), gw.ChatCompletionRequest{Model: "priced-1"})
	if err != nil || resp.Model != "priced-1" || inner.calls != 1 {
		t.Fatalf("Chat = %+v, %v (calls %d), want served", resp, err, inner.calls)
	}
	if got := testCounter(t, spendShadowPanics); got != before+1 {
		t.Fatalf("panics = %v, want %v", got, before+1)
	}
}

func testCounter(t *testing.T, c prometheus.Counter) float64 {
	t.Helper()
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetCounter().GetValue()
}

// gathered reads billing_spend_limit_evaluated_total for one label set from
// the default registry (the meter's counter is not exported).
func gathered(t *testing.T, outcome, scope string) float64 {
	t.Helper()
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "billing_spend_limit_evaluated_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			if labels["mode"] == "shadow" && labels["basis"] == "all_inference" && labels["outcome"] == outcome && labels["scope"] == scope {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

// meterSink is the production meter with the organization resolved
// directly (the tenant mapping is not under test).
type meterSink struct {
	*billingmeter.Meter
	org string
}

func (s meterSink) ResolveOrg(context.Context) (string, bool, error) { return s.org, true, nil }

func spendPGDB(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("EVS_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("EVS_TEST_PG_DSN not set; skipping real-Postgres spend shadow test")
	}
	admin, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	var suffix [4]byte
	_, _ = rand.Read(suffix[:])
	name := "spend_shadow_" + hex.EncodeToString(suffix[:])
	if _, err := admin.Exec(`CREATE DATABASE ` + name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(`DROP DATABASE IF EXISTS ` + name + ` WITH (FORCE)`)
		admin.Close()
	})
	scratch := dsn
	if i := strings.LastIndex(dsn, "/"); i > 0 {
		rest, q := dsn[i+1:], ""
		if j := strings.Index(rest, "?"); j >= 0 {
			q = rest[j:]
		}
		scratch = dsn[:i+1] + name + q
	}
	db, err := sqlx.Connect("pgx", scratch)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, file, _, _ := runtime.Caller(0)
	wd, _ := os.Getwd()
	if err := os.Chdir(filepath.Join(filepath.Dir(file), "..", "..", "..")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	for _, svc := range []string{"cloud", "billing"} {
		if err := migrations.EnsureForService(context.Background(), db, "postgres", svc); err != nil {
			t.Fatalf("%s migrations: %v", svc, err)
		}
	}
	return db
}

// Shadow never refuses: an organization already past its all_inference
// block limit keeps being served on BYOK, unary and streaming, while the
// evaluator records would_block for every attempt.
func TestPGSpendShadowNeverRefusesAndRecordsWouldBlock(t *testing.T) {
	db := spendPGDB(t)
	org := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO everstack.organizations (id, slug, name) VALUES ($1::uuid, $2, 'Org')`, org, "ss-"+org[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO billing.spend_limits (organization_id, basis, limit_type, limit_amount, period, action_on_exceed)
		VALUES ($1::uuid, 'all_inference', 'estimated_cost', 1, 'monthly', 'block')`, org); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO billing.spend_rollups (organization_id, api_key_id, bucket_hour, byok_est_micros, attempts)
		VALUES ($1::uuid, 'key-1', date_trunc('hour', NOW()), 5000000, 1)`, org); err != nil {
		t.Fatal(err)
	}
	m := billingmeter.New(db, nil)
	t.Cleanup(func() { m.Shutdown(context.Background()) })
	inner := &fakeProvider{usage: gw.Usage{PromptTokens: 10, CompletionTokens: 5}}
	p := Wrap(inner, byokCfg(meterSink{Meter: m, org: org}, attributionCatalog(t)))
	req := gw.ChatCompletionRequest{Model: "priced-1", Messages: []gw.Message{{Role: "user", Content: text("hi")}}}

	before := gathered(t, billingmeter.SpendOutcomeWouldBlock, billingmeter.SpendScopeOrganization)
	// Cold: served at once; the first evaluation runs in the background.
	if _, err := p.Chat(keyCtx("key-1"), req); err != nil {
		t.Fatalf("cold attempt refused: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for gathered(t, billingmeter.SpendOutcomeWouldBlock, billingmeter.SpendScopeOrganization) < before+1 {
		if time.Now().After(deadline) {
			t.Fatal("would_block never recorded for the cold attempt")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Warm: evaluated inline, still served.
	if _, err := p.Chat(keyCtx("key-1"), req); err != nil {
		t.Fatalf("warm attempt refused: %v", err)
	}
	chunks := 0
	if err := p.ChatStream(keyCtx("key-1"), req, func(gw.ChatResponseChunk) error { chunks++; return nil }); err != nil {
		t.Fatalf("stream refused: %v", err)
	}
	if inner.calls != 3 || chunks != 2 {
		t.Fatalf("provider calls %d, chunks %d: every attempt must reach the provider unaltered", inner.calls, chunks)
	}
	if got := gathered(t, billingmeter.SpendOutcomeWouldBlock, billingmeter.SpendScopeOrganization); got != before+3 {
		t.Fatalf("would_block = %v, want %v", got, before+3)
	}
}
