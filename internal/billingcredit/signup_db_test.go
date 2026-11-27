package billingcredit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/database/migrations"
	"github.com/everstacklabs/everstack/internal/lib/pgscratch"
)

// Real-Postgres tests for the per-user signup credit. They run the cloud and
// billing migrations into a scratch database (EVS_TEST_PG_DSN) so the SQL is
// exercised against the production schema.

const fiveDollars = int64(5_000_000)

type signupEnv struct {
	t   *testing.T
	db  *sqlx.DB
	ctx context.Context
	// clock is a monotonically increasing timestamp used for created_at /
	// joined_at so "first org" ordering is deterministic.
	clock time.Time
}

func newSignupEnv(t *testing.T) *signupEnv {
	t.Helper()
	db := pgscratch.New(t, "signup_credit")
	ctx := context.Background()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Join(wd, "..", "..")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	for _, svc := range []string{"cloud", "billing"} {
		if err := migrations.EnsureForService(ctx, db, "postgres", svc); err != nil {
			t.Fatalf("%s migrations: %v", svc, err)
		}
	}
	return &signupEnv{t: t, db: db, ctx: ctx, clock: time.Now().Add(-48 * time.Hour)}
}

func (e *signupEnv) tick() time.Time {
	e.clock = e.clock.Add(time.Minute)
	return e.clock
}

func (e *signupEnv) user() uuid.UUID {
	id := uuid.New()
	return e.userWithEmail(id, id.String()+"@example.test")
}

func (e *signupEnv) userWithEmail(id uuid.UUID, email string) uuid.UUID {
	e.db.MustExec(`INSERT INTO everstack.users (id, external_id, email) VALUES ($1, $2, $3)`,
		id, "ext-"+id.String(), email)
	return id
}

// legacyGrant writes a signup grant the way the old per-org rule did: keyed
// by org, no expiry, no claims.
func (e *signupEnv) legacyGrant(org uuid.UUID) {
	e.db.MustExec(`
		INSERT INTO billing.credit_ledger (organization_id, entry_type, amount_micros, resource_type, idempotency_key)
		VALUES ($1, 'grant', $2, 'signup', $3)`, org, fiveDollars, SignupGrantIdempotencyKey(org.String()))
	e.db.MustExec(`INSERT INTO billing.credit_balances (organization_id, balance_micros) VALUES ($1, $2)
		ON CONFLICT (organization_id) DO UPDATE SET balance_micros = billing.credit_balances.balance_micros + EXCLUDED.balance_micros`, org, fiveDollars)
}

// runClaimsMigration re-runs the claims migration (idempotent by design) so
// its backfill sees ledger rows seeded after the scratch DB was migrated.
func (e *signupEnv) runClaimsMigration() {
	e.t.Helper()
	matches, err := filepath.Glob("services/billing/internal/database/migrations/postgres/signup_credit_claims_*/up.sql")
	if err != nil || len(matches) != 1 {
		e.t.Fatalf("claims migration not found: %v %v", matches, err)
	}
	body, err := os.ReadFile(matches[0])
	if err != nil {
		e.t.Fatal(err)
	}
	e.db.MustExec(string(body))
}

// org creates an organization with userID as its owner.
func (e *signupEnv) org(owner uuid.UUID) uuid.UUID {
	id := uuid.New()
	at := e.tick()
	e.db.MustExec(`INSERT INTO everstack.organizations (id, slug, name, created_at, updated_at) VALUES ($1, $2, 'Org', $3, $3)`,
		id, "o-"+id.String()[:8], at)
	e.member(id, owner, "owner")
	return id
}

func (e *signupEnv) member(org, user uuid.UUID, role string) {
	e.db.MustExec(`INSERT INTO everstack.organization_members (organization_id, user_id, role, joined_at) VALUES ($1, $2, $3, $4)`,
		org, user, role, e.tick())
}

func (e *signupEnv) sync(org uuid.UUID, now time.Time) bool {
	e.t.Helper()
	granted, err := SyncSignupCredit(e.ctx, e.db, org.String(), now)
	if err != nil {
		e.t.Fatalf("SyncSignupCredit(%s): %v", org, err)
	}
	return granted
}

func (e *signupEnv) ledger(org uuid.UUID, entryType string, micros int64) {
	e.db.MustExec(`
		INSERT INTO billing.credit_ledger (organization_id, entry_type, amount_micros, resource_type, idempotency_key, period_start)
		VALUES ($1, $2, $3, 'sandbox', $4, NOW())`, org, entryType, micros, "test:"+uuid.NewString())
	e.db.MustExec(`
		INSERT INTO billing.credit_balances (organization_id, balance_micros) VALUES ($1, $2)
		ON CONFLICT (organization_id) DO UPDATE SET balance_micros = billing.credit_balances.balance_micros + EXCLUDED.balance_micros`,
		org, micros)
}

func (e *signupEnv) balance(org uuid.UUID) int64 {
	e.t.Helper()
	var ledger, cached int64
	if err := e.db.Get(&ledger, `SELECT COALESCE(SUM(amount_micros), 0)::BIGINT FROM billing.credit_ledger WHERE organization_id = $1`, org); err != nil {
		e.t.Fatal(err)
	}
	if err := e.db.Get(&cached, `SELECT COALESCE((SELECT balance_micros FROM billing.credit_balances WHERE organization_id = $1), 0)`, org); err != nil {
		e.t.Fatal(err)
	}
	if ledger != cached {
		e.t.Fatalf("org %s: balance cache %d drifted from ledger %d", org, cached, ledger)
	}
	return ledger
}

func (e *signupEnv) signupGrants(org uuid.UUID) int {
	var n int
	if err := e.db.Get(&n, `SELECT COUNT(*) FROM billing.credit_ledger WHERE organization_id = $1 AND entry_type = 'grant' AND resource_type = 'signup'`, org); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *signupEnv) expiries(org uuid.UUID) []int64 {
	var out []int64
	if err := e.db.Select(&out, `SELECT amount_micros FROM billing.credit_ledger WHERE organization_id = $1 AND entry_type = 'expiry' ORDER BY created_at`, org); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func TestSignupCreditIsPerUserAcrossOrgs(t *testing.T) {
	e := newSignupEnv(t)
	now := time.Now()
	u := e.user()
	first, second := e.org(u), e.org(u)

	// The later org asks first: it never takes the credit ahead of the first.
	if e.sync(second, now) {
		t.Fatal("a user's second org was granted the signup credit")
	}
	if !e.sync(first, now) {
		t.Fatal("the user's first org was not granted the signup credit")
	}
	if e.sync(first, now) || e.sync(second, now) {
		t.Fatal("signup credit granted twice")
	}
	if e.balance(first) != fiveDollars || e.balance(second) != 0 {
		t.Fatalf("balances first=%d second=%d", e.balance(first), e.balance(second))
	}
	var key string
	var expires time.Time
	if err := e.db.QueryRow(`SELECT idempotency_key, expires_at FROM billing.credit_ledger WHERE organization_id = $1 AND resource_type = 'signup'`, first).Scan(&key, &expires); err != nil {
		t.Fatal(err)
	}
	if key != SignupGrantUserIdempotencyKey(u.String()) {
		t.Fatalf("grant key = %q, want the per-user key", key)
	}
	if d := expires.Sub(now.Add(SignupCreditTTL)); d > time.Second || d < -time.Second {
		t.Fatalf("expires_at = %s, want 30 days after the grant (%s)", expires, now.Add(SignupCreditTTL))
	}

	// The sweep never lists the second org for a grant.
	ids, err := ListOrganizationsWithDueSignupWork(e.ctx, e.db, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if id == second.String() || id == first.String() {
			t.Fatalf("sweep listed %s with nothing due", id)
		}
	}

	// Handing the granted org to someone else does not re-arm the credit.
	other := e.user()
	e.db.MustExec(`DELETE FROM everstack.organization_members WHERE organization_id = $1`, first)
	e.member(first, other, "owner")
	third := e.org(u)
	_ = e.sync(second, now)
	if e.sync(third, now) || e.balance(second) != 0 || e.balance(third) != 0 {
		t.Fatal("a user received a second signup credit after transferring the first org")
	}
}

func TestSignupCreditConcurrentOrgsOfOneUserGrantOnce(t *testing.T) {
	e := newSignupEnv(t)
	now := time.Now()
	u := e.user()
	orgs := []uuid.UUID{e.org(u)}
	// Two orgs created in the same instant still resolve to one first org
	// (id tie-break), and the user key settles the race either way.
	orgs = append(orgs, e.org(u))
	e.db.MustExec(`UPDATE everstack.organizations SET created_at = $1 WHERE id = ANY($2)`, e.tick(), []string{orgs[0].String(), orgs[1].String()})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		for _, o := range orgs {
			wg.Add(1)
			go func(o uuid.UUID) {
				defer wg.Done()
				if _, err := SyncSignupCredit(e.ctx, e.db, o.String(), now); err != nil {
					t.Error(err)
				}
			}(o)
		}
	}
	wg.Wait()
	total := e.signupGrants(orgs[0]) + e.signupGrants(orgs[1])
	if total != 1 {
		t.Fatalf("signup grants across the user's orgs = %d, want 1", total)
	}
}

func TestSignupCreditOldPerOrgGrantCountsAndNeverExpires(t *testing.T) {
	e := newSignupEnv(t)
	u := e.user()
	legacy := e.org(u)
	// A grant made under the old per-org rule: per-org key, no expiry.
	e.legacyGrant(legacy)
	newer := e.org(u)

	later := time.Now().Add(90 * 24 * time.Hour)
	if e.sync(newer, later) || e.sync(legacy, later) {
		t.Fatal("a user who already got the credit under the old rule was granted again")
	}
	if got := e.expiries(legacy); len(got) != 0 {
		t.Fatalf("an old-rule grant was expired: %v", got)
	}
	if e.balance(legacy) != fiveDollars {
		t.Fatalf("old-rule credit was clawed back: %d", e.balance(legacy))
	}
}

func TestSignupCreditCoOwnerKeepsOwnCredit(t *testing.T) {
	e := newSignupEnv(t)
	now := time.Now()
	alice, bob := e.user(), e.user()
	aliceOrg := e.org(alice)
	e.member(aliceOrg, bob, "owner") // invited later as co-owner
	if !e.sync(aliceOrg, now) {
		t.Fatal("alice's org was not granted")
	}
	bobOrg := e.org(bob)
	if !e.sync(bobOrg, now) {
		t.Fatal("an invitation as co-owner spent the invitee's own signup credit")
	}
}

// Tenant isolation: one org's grant and expiry never write to, or read the
// spend of, another organization.
func TestSignupCreditTenantIsolation(t *testing.T) {
	e := newSignupEnv(t)
	now := time.Now()
	a, b := e.user(), e.user()
	orgA, orgB := e.org(a), e.org(b)
	if !e.sync(orgA, now) || !e.sync(orgB, now) {
		t.Fatal("each user's first org must be granted")
	}
	e.ledger(orgB, "debit", -4_000_000) // B spends; A does not
	e.ledger(orgA, "purchase", 7_000_000)

	expiry := now.Add(SignupCreditTTL + SignupExpirySettleWindow + time.Minute)
	e.sync(orgA, expiry)
	if got := e.expiries(orgA); len(got) != 1 || got[0] != -fiveDollars {
		t.Fatalf("org A expiries = %v; B's spend must not reduce A's unspent credit", got)
	}
	if got := e.expiries(orgB); len(got) != 0 {
		t.Fatalf("expiring org A wrote to org B: %v", got)
	}
	if e.balance(orgA) != 7_000_000 || e.balance(orgB) != 1_000_000 {
		t.Fatalf("balances A=%d B=%d", e.balance(orgA), e.balance(orgB))
	}
	e.sync(orgB, expiry)
	if got := e.expiries(orgB); len(got) != 1 || got[0] != -1_000_000 {
		t.Fatalf("org B expiries = %v, want only its own unspent $1", got)
	}
}

func TestSignupCreditExpiry(t *testing.T) {
	cases := []struct {
		name        string
		purchase    int64
		spend       int64
		held        int64
		wantExpired int64
		wantBalance int64
	}{
		{name: "unspent credit expires in full", wantExpired: fiveDollars, wantBalance: 0},
		{name: "only the unspent remainder expires", spend: 3_000_000, wantExpired: 2_000_000, wantBalance: 0},
		{name: "purchased credit is untouched", purchase: 10_000_000, wantExpired: fiveDollars, wantBalance: 10_000_000},
		// Spend draws on the expiring signup credit before purchased credit:
		// $3 of spend leaves $2 of signup credit to expire and all $10 bought.
		{name: "spend order: signup credit first", purchase: 10_000_000, spend: 3_000_000, wantExpired: 2_000_000, wantBalance: 10_000_000},
		{name: "spend beyond the signup credit leaves nothing to expire", purchase: 10_000_000, spend: 7_000_000, wantExpired: 0, wantBalance: 8_000_000},
		// Holds (in-flight requests, dispute holds) limit what can be spent,
		// not what expired: a hold never stops the signup credit expiring.
		{name: "holds do not block expiry", held: fiveDollars, wantExpired: fiveDollars, wantBalance: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newSignupEnv(t)
			now := time.Now()
			org := e.org(e.user())
			if !e.sync(org, now) {
				t.Fatal("not granted")
			}
			if tc.purchase > 0 {
				e.ledger(org, "purchase", tc.purchase)
			}
			if tc.spend > 0 {
				e.ledger(org, "debit", -tc.spend)
			}
			if tc.held > 0 {
				e.db.MustExec(`INSERT INTO billing.credit_holds (organization_id, amount_micros, request_id, state) VALUES ($1, $2, 'r1', 'held')`, org, tc.held)
			}

			// Not due yet: nothing expires a day before.
			e.sync(org, now.Add(SignupCreditTTL-24*time.Hour))
			if got := e.expiries(org); len(got) != 0 {
				t.Fatalf("expired early: %v", got)
			}
			// Within the settle window after expires_at nothing is written yet.
			e.sync(org, now.Add(SignupCreditTTL+time.Minute))
			if got := e.expiries(org); len(got) != 0 {
				t.Fatalf("expired inside the settle window: %v", got)
			}
			due := now.Add(SignupCreditTTL + SignupExpirySettleWindow + time.Minute)
			ids, err := ListOrganizationsWithDueSignupWork(e.ctx, e.db, due)
			if err != nil {
				t.Fatal(err)
			}
			listed := false
			for _, id := range ids {
				listed = listed || id == org.String()
			}
			if !listed {
				t.Fatal("sweep did not list an org with a due signup expiry")
			}
			e.sync(org, due)
			e.sync(org, due.Add(time.Hour)) // idempotent
			got := e.expiries(org)
			if len(got) != 1 || got[0] != -tc.wantExpired {
				t.Fatalf("expiries = %v, want one of %d", got, -tc.wantExpired)
			}
			if b := e.balance(org); b != tc.wantBalance {
				t.Fatalf("balance = %d, want %d", b, tc.wantBalance)
			}
		})
	}
}

// The review's blocking case: a user granted under the old per-org rule
// deletes (or hands away) that org and creates another. The backfilled claim
// refuses the second $5 whatever the user owns today.
func TestSignupCreditBackfillBlocksLegacyUserAfterDeleteOrTransfer(t *testing.T) {
	e := newSignupEnv(t)
	now := time.Now()
	deleter, transferrer, stranger := e.user(), e.user(), e.user()
	deletedOrg, handedOrg := e.org(deleter), e.org(transferrer)
	e.legacyGrant(deletedOrg)
	e.legacyGrant(handedOrg)
	// An org deleted before the backfill: its owner is unrecoverable, but the
	// grant still gets its org claim.
	gone := e.org(e.user())
	e.legacyGrant(gone)
	e.db.MustExec(`DELETE FROM everstack.organizations WHERE id = $1`, gone)

	e.runClaimsMigration()
	e.runClaimsMigration() // idempotent

	var claims []string
	if err := e.db.Select(&claims, `SELECT claim_key FROM billing.signup_credit_claims ORDER BY claim_key`); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"user:" + deleter.String(): true, "user:" + transferrer.String(): true,
		"org:" + deletedOrg.String(): true, "org:" + handedOrg.String(): true, "org:" + gone.String(): true,
		"email:" + deleter.String() + "@example.test": true, "email:" + transferrer.String() + "@example.test": true,
	}
	if len(claims) != len(want) {
		t.Fatalf("backfilled claims = %v", claims)
	}
	for _, c := range claims {
		if !want[c] {
			t.Fatalf("unexpected backfilled claim %q in %v", c, claims)
		}
	}

	e.db.MustExec(`DELETE FROM everstack.organizations WHERE id = $1`, deletedOrg)
	afterDelete := e.org(deleter)
	e.db.MustExec(`DELETE FROM everstack.organization_members WHERE organization_id = $1`, handedOrg)
	e.member(handedOrg, stranger, "owner")
	afterTransfer := e.org(transferrer)
	for name, org := range map[string]uuid.UUID{"after delete": afterDelete, "after transfer": afterTransfer} {
		if e.sync(org, now) || e.balance(org) != 0 {
			t.Fatalf("%s: a legacy user received a second signup credit", name)
		}
	}
}

// Anti-farming: +aliases and Gmail dot variants of one mailbox share a claim.
func TestSignupCreditDedupesCanonicalEmail(t *testing.T) {
	e := newSignupEnv(t)
	now := time.Now()
	first := e.userWithEmail(uuid.New(), "Jane.Doe+one@Gmail.com")
	if !e.sync(e.org(first), now) {
		t.Fatal("first account not granted")
	}
	for _, alias := range []string{"janedoe@gmail.com", "j.a.n.e.doe+two@googlemail.com", " JANEDOE@GMAIL.COM "} {
		u := e.userWithEmail(uuid.New(), alias)
		if e.sync(e.org(u), now) {
			t.Fatalf("alias %q received a second signup credit", alias)
		}
	}
	work := e.userWithEmail(uuid.New(), "sam+trial@acme.io")
	if !e.sync(e.org(work), now) {
		t.Fatal("a distinct mailbox was refused")
	}
	if u := e.userWithEmail(uuid.New(), "SAM+again@acme.io"); e.sync(e.org(u), now) {
		t.Fatal("a +alias on a custom domain received a second signup credit")
	}
	// Dots are only folded for Gmail: s.am@acme.io is another mailbox.
	if u := e.userWithEmail(uuid.New(), "s.am@acme.io"); !e.sync(e.org(u), now) {
		t.Fatal("dots were folded on a non-Gmail domain")
	}
}

func TestCanonicalEmailSQL(t *testing.T) {
	e := newSignupEnv(t)
	for in, want := range map[string]string{
		"Jane.Doe+promo@Gmail.com": "janedoe@gmail.com",
		"jane.doe@googlemail.com":  "janedoe@gmail.com",
		"  Sam+x+y@Acme.IO ":       "sam@acme.io",
		"first.last@acme.io":       "first.last@acme.io",
		"odd@name@acme.io":         "odd@name@acme.io",
		"plain":                    "plain",
		"user+tag@sub.gmail.com":   "user@sub.gmail.com",
	} {
		var got string
		if err := e.db.Get(&got, `SELECT billing.canonical_email($1)`, in); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("canonical_email(%q) = %q, want %q", in, got, want)
		}
	}
}

// A debit written after expires_at, for usage before it (the last compute
// tick, a retried inference settle), counts as signup spend, so it never
// comes out of purchased credit.
func TestSignupCreditLateDebitIsSignupSpend(t *testing.T) {
	e := newSignupEnv(t)
	now := time.Now()
	org := e.org(e.user())
	if !e.sync(org, now) {
		t.Fatal("not granted")
	}
	e.ledger(org, "purchase", 10_000_000)
	var expiresAt time.Time
	if err := e.db.Get(&expiresAt, `SELECT expires_at FROM billing.credit_ledger WHERE organization_id = $1 AND resource_type = 'signup'`, org); err != nil {
		t.Fatal(err)
	}
	e.db.MustExec(`
		INSERT INTO billing.credit_ledger (organization_id, entry_type, amount_micros, resource_type, idempotency_key, period_start, created_at)
		VALUES ($1, 'debit', -3000000, 'sandbox', 'late-debit', $2, $3)`, org, expiresAt, expiresAt.Add(30*time.Minute))
	e.db.MustExec(`UPDATE billing.credit_balances SET balance_micros = balance_micros - 3000000 WHERE organization_id = $1`, org)

	e.sync(org, expiresAt.Add(SignupExpirySettleWindow+time.Minute))
	if got := e.expiries(org); len(got) != 1 || got[0] != -2_000_000 {
		t.Fatalf("expiries = %v, want -$2 (the late $3 debit is signup spend)", got)
	}
	if b := e.balance(org); b != 10_000_000 {
		t.Fatalf("balance = %d, want all $10 of purchased credit", b)
	}
}

// A gateway that deploys before the services migration sees no claims table
// or canonical_email function. The grant is skipped; admission (funded or
// not) and expiry still work.
func TestSignupCreditWithoutClaimsSchema(t *testing.T) {
	e := newSignupEnv(t)
	now := time.Now()
	expiring := e.org(e.user())
	if !e.sync(expiring, now.Add(-SignupCreditTTL-SignupExpirySettleWindow-time.Hour)) {
		t.Fatal("not granted")
	}
	e.db.MustExec(`DROP TABLE billing.signup_credit_claims; DROP FUNCTION billing.canonical_email(TEXT)`)

	funded := e.org(e.user())
	e.ledger(funded, "purchase", 10_000_000)
	access, err := Resolve(e.ctx, e.db, funded.String(), "free")
	if err != nil || !access.Allowed || access.Credit.RemainingMicros != 10_000_000 {
		t.Fatalf("funded org admission = %+v, %v", access, err)
	}
	fresh := e.org(e.user())
	access, err = Resolve(e.ctx, e.db, fresh.String(), "free")
	if err != nil {
		t.Fatalf("new org admission failed: %v", err)
	}
	if access.Credit.RemainingMicros != 0 || e.signupGrants(fresh) != 0 {
		t.Fatalf("new org = %+v; the grant must be skipped without the claims schema", access)
	}
	// Expiry of an existing grant still runs.
	if _, err := Resolve(e.ctx, e.db, expiring.String(), "free"); err != nil {
		t.Fatal(err)
	}
	if got := e.expiries(expiring); len(got) != 1 || got[0] != -fiveDollars {
		t.Fatalf("expiries = %v, want the due signup credit expired", got)
	}
}

// Inference debits record when their usage happened; expiry attributes them
// by that time rather than by when they were written.
func TestSignupCreditCountsInferenceByUsageTime(t *testing.T) {
	for _, tc := range []struct {
		name        string
		usageOffset time.Duration // relative to expires_at
		wantExpired int64
	}{
		{"usage before expiry, debited late", -time.Minute, 2_000_000},
		{"usage after expiry, debited inside the window", 10 * time.Minute, fiveDollars},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newSignupEnv(t)
			org := e.org(e.user())
			if !e.sync(org, time.Now()) {
				t.Fatal("not granted")
			}
			e.ledger(org, "purchase", 10_000_000)
			var expiresAt time.Time
			if err := e.db.Get(&expiresAt, `SELECT expires_at FROM billing.credit_ledger WHERE organization_id = $1 AND resource_type = 'signup'`, org); err != nil {
				t.Fatal(err)
			}
			meta := fmt.Sprintf(`{"occurred_at": %q}`, expiresAt.Add(tc.usageOffset).UTC().Format(time.RFC3339Nano))
			e.db.MustExec(`
				INSERT INTO billing.credit_ledger (organization_id, entry_type, amount_micros, resource_type, idempotency_key, period_start, metadata, created_at)
				VALUES ($1, 'debit', -3000000, 'inference', 'inference:late', $2, $3::jsonb, $4)`,
				org, expiresAt, meta, expiresAt.Add(30*time.Minute))
			e.db.MustExec(`UPDATE billing.credit_balances SET balance_micros = balance_micros - 3000000 WHERE organization_id = $1`, org)

			e.sync(org, expiresAt.Add(SignupExpirySettleWindow+time.Minute))
			if got := e.expiries(org); len(got) != 1 || got[0] != -tc.wantExpired {
				t.Fatalf("expiries = %v, want %d", got, -tc.wantExpired)
			}
			if b := e.balance(org); b != 12_000_000-tc.wantExpired {
				t.Fatalf("balance = %d", b)
			}
		})
	}
}

// A grant minted without claims (an old pod during the rollout) gets them
// from the sweep's reconcile, so its user is never granted again.
func TestReconcileSignupClaimsCoversRolloutGrants(t *testing.T) {
	e := newSignupEnv(t)
	u := e.user()
	first := e.org(u)
	e.legacyGrant(first) // minted by an old pod: per-org key, no claims

	n, err := ReconcileSignupClaims(e.ctx, e.db)
	if err != nil || n != 3 {
		t.Fatalf("reconcile inserted %d claims (%v), want org, user and email", n, err)
	}
	if n, err := ReconcileSignupClaims(e.ctx, e.db); err != nil || n != 0 {
		t.Fatalf("second reconcile inserted %d (%v), want an idempotent no-op", n, err)
	}
	e.db.MustExec(`DELETE FROM everstack.organizations WHERE id = $1`, first)
	if e.sync(e.org(u), time.Now()) {
		t.Fatal("a user granted by an old pod received a second signup credit")
	}
}
