package authratelimit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
)

type clock struct{ now time.Time }

func (c *clock) advance(d time.Duration) { c.now = c.now.Add(d) }

type sleepRecorder struct{ delays []time.Duration }

func (s *sleepRecorder) sleep(_ context.Context, d time.Duration) { s.delays = append(s.delays, d) }

type fixture struct {
	limiter *Limiter
	clock   *clock
	sleeps  *sleepRecorder
}

// newFixture builds a limiter over a memory store whose clock the test
// drives, so hours of attack run in milliseconds.
func newFixture(t *testing.T, adjust func(*LimiterConfig)) fixture {
	t.Helper()
	c := &clock{now: time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)}
	store := NewMemoryStore()
	store.now = func() time.Time { return c.now }
	sleeps := &sleepRecorder{}
	cfg := LimiterConfig{
		Store: store,
		// Generous, so these tests exercise the per-account rules; the
		// per-IP rule has its own tests.
		PerIP:     Rule{Limit: 1000, Window: time.Minute},
		Procedure: func(name string) bool { return name != "GetSession" },
		Accounts:  DefaultAccountPolicy("Login", "RequestMagicLink", "RequestPasswordReset", "Register"),
		Sleep:     sleeps.sleep,
	}
	if adjust != nil {
		adjust(&cfg)
	}
	return fixture{limiter: NewLimiter(cfg), clock: c, sleeps: sleeps}
}

type emailRequest struct{ email string }

func (r *emailRequest) GetEmail() string { return r.email }

var errWrongPassword = connect.NewError(connect.CodeUnauthenticated, errors.New("invalid credentials"))

// call drives the limiter's decision directly with a stand-in handler. The
// Connect adapter around it is exercised end to end against a real
// AuthService handler in services/auth/internal/transport/connect.
func (f fixture) call(procedure, ip, email string, handler func() error) (reached bool, err error) {
	err = f.limiter.guard(
		context.Background(),
		"/everstack.auth.v1.AuthService/"+procedure,
		"",
		ip+":1234",
		&emailRequest{email: email},
		func() error {
			reached = true
			return handler()
		},
	)
	return reached, err
}

func (f fixture) login(ip, email string, correct bool) error {
	_, err := f.call("Login", ip, email, func() error {
		if correct {
			return nil
		}
		return errWrongPassword
	})
	return err
}

func (f fixture) send(procedure, ip, email string) error {
	_, err := f.call(procedure, ip, email, func() error { return nil })
	return err
}

func retryAfterSecondsOf(t *testing.T, err error) int {
	t.Helper()
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) || connectErr.Code() != connect.CodeResourceExhausted {
		t.Fatalf("err = %v; want ResourceExhausted", err)
	}
	seconds, convErr := strconv.Atoi(connectErr.Meta().Get("Retry-After"))
	if convErr != nil || seconds < 1 {
		t.Fatalf("Retry-After = %q; want whole seconds", connectErr.Meta().Get("Retry-After"))
	}
	return seconds
}

const (
	victim     = "victim@example.com"
	victimIP   = "203.0.113.7"
	attackerIP = "198.51.100.66"
)

// The lockout attack: one address guesses the victim's password at a steady,
// low rate for longer than any window. Budgets keyed on the account alone let
// that keep the victim out for as long as the attacker keeps going.
func TestLoginAttackFromOneAddressNeverLocksOutTheVictim(t *testing.T) {
	f := newFixture(t, nil)
	var attacker []connect.Code
	retryAfterSeen := 0

	for tick := 0; tick < 240; tick++ { // one guess every 30s for two hours
		err := f.login(attackerIP, victim, false)
		attacker = append(attacker, connect.CodeOf(err))
		if connect.CodeOf(err) == connect.CodeResourceExhausted {
			retryAfterSeen = retryAfterSecondsOf(t, err)
		}
		if tick%20 == 10 {
			if err := f.login(victimIP, victim, true); err != nil {
				t.Fatalf("tick %d: victim's correct password = %v; want success", tick, err)
			}
		}
		if tick == 100 {
			for _, procedure := range []string{"RequestMagicLink", "RequestPasswordReset"} {
				if err := f.send(procedure, victimIP, victim); err != nil {
					t.Fatalf("victim's %s during the attack = %v; want success", procedure, err)
				}
			}
		}
		f.clock.advance(30 * time.Second)
	}

	for i, code := range attacker[:10] {
		if code != connect.CodeUnauthenticated {
			t.Fatalf("attempt %d = %v; want the first 10 guesses to reach the handler", i+1, code)
		}
	}
	if attacker[10] != connect.CodeResourceExhausted {
		t.Fatalf("attempt 11 = %v; want the attacker's own pair refused", attacker[10])
	}
	if retryAfterSeen < 1 || retryAfterSeen > 15*60 {
		t.Fatalf("Retry-After = %d; want the time left in the 15 minute window", retryAfterSeen)
	}
	reopened := false
	guesses := 0
	for i, code := range attacker {
		if code == connect.CodeUnauthenticated {
			guesses++
			if i > 10 {
				reopened = true
			}
		}
	}
	if !reopened {
		t.Fatal("the attacker's pair should reopen when its window ends")
	}
	if guesses > 10*9 {
		t.Fatalf("guesses = %d; want at most 10 per 15 minute window", guesses)
	}
	if len(f.sleeps.delays) != 0 {
		t.Fatalf("delays = %v; one address stays under the account-wide cap, and refused attempts do not count", f.sleeps.delays)
	}
}

func TestLoginUnderADistributedAttackStillAcceptsTheRightPasswordWithoutDelay(t *testing.T) {
	f := newFixture(t, func(cfg *LimiterConfig) {
		cfg.Accounts.PasswordFailuresPerAccount = Rule{Limit: 5, Window: time.Hour}
	})

	for address := 1; address <= 8; address++ {
		if err := f.login("198.51.100."+strconv.Itoa(address), victim, false); connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("guess from address %d = %v; want the handler's answer", address, err)
		}
	}
	if len(f.sleeps.delays) != 3 {
		t.Fatalf("delays = %v; want one for each failure past the cap of 5", f.sleeps.delays)
	}
	if !(f.sleeps.delays[0] > 0 && f.sleeps.delays[2] > f.sleeps.delays[0]) {
		t.Fatalf("delays = %v; want them to grow", f.sleeps.delays)
	}
	for _, d := range f.sleeps.delays {
		if d > f.limiter.accounts.FailureDelayMax {
			t.Fatalf("delay %s exceeds the bound", d)
		}
	}

	f.sleeps.delays = nil
	if err := f.login(victimIP, victim, true); err != nil {
		t.Fatalf("victim's correct password = %v; want success", err)
	}
	if len(f.sleeps.delays) != 0 {
		t.Fatalf("delays = %v; a correct password must never be slowed by others' failures", f.sleeps.delays)
	}
	if err := f.login("198.51.100.200", victim, false); connect.CodeOf(err) != connect.CodeUnauthenticated || len(f.sleeps.delays) != 1 {
		t.Fatalf("a fresh wrong guess = %v, delays %v; want it answered and slowed", err, f.sleeps.delays)
	}
}

func TestLoginCountsOnlyRejectedCredentials(t *testing.T) {
	f := newFixture(t, nil)
	mfa := connect.NewError(connect.CodeFailedPrecondition, errors.New("mfa_required"))

	for range 20 {
		_, err := f.call("Login", attackerIP, victim, func() error { return mfa })
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("err = %v; MFA and verification challenges are not failures", err)
		}
	}
}

func TestSendsAreLimitedPerAccountAndAddress(t *testing.T) {
	for _, procedure := range []string{"RequestMagicLink", "RequestPasswordReset", "Register"} {
		t.Run(procedure, func(t *testing.T) {
			f := newFixture(t, nil)
			for i := range 3 {
				if err := f.send(procedure, attackerIP, victim); err != nil {
					t.Fatalf("send %d = %v", i+1, err)
				}
			}
			if seconds := retryAfterSecondsOf(t, f.send(procedure, attackerIP, victim)); seconds > 600 {
				t.Fatalf("Retry-After = %d; want within the 10 minute window", seconds)
			}
			if err := f.send(procedure, victimIP, victim); err != nil {
				t.Fatalf("the owner's own address = %v; want its own budget", err)
			}
		})
	}
}

func TestOneAddressCyclingEveryMailFlowCannotStopTheOwnersMail(t *testing.T) {
	f := newFixture(t, nil)
	flows := []string{"RequestMagicLink", "RequestPasswordReset", "Register"}
	sentThisHour := 0

	for tick := 0; tick < 240; tick++ { // every 30s for two hours
		for _, flow := range flows {
			if f.send(flow, attackerIP, victim) == nil {
				sentThisHour++
			}
		}
		if tick%20 == 10 {
			if err := f.send("RequestMagicLink", victimIP, victim); err != nil {
				t.Fatalf("tick %d: the owner's magic link = %v; want it sent", tick, err)
			}
		}
		if tick%120 == 119 {
			if sentThisHour > 10 {
				t.Fatalf("the attacker's address got %d mails in an hour; want at most 10 across flows", sentThisHour)
			}
			sentThisHour = 0
		}
		f.clock.advance(30 * time.Second)
	}
}

func TestAccountProceduresRefuseAnUnusableEmailBeforeTheHandler(t *testing.T) {
	f := newFixture(t, nil)
	for _, procedure := range []string{"Login", "RequestMagicLink", "RequestPasswordReset", "Register"} {
		for _, email := range []string{"", "   ", strings.Repeat("a", 321) + "@example.com"} {
			reached, err := f.call(procedure, attackerIP, email, func() error { return nil })
			if reached || connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("%s with email %q: reached=%v err=%v; want InvalidArgument before the handler", procedure, email, reached, err)
			}
		}
	}
}

func TestSendsHaveAnAccountWideCapThatRefusedRequestsDoNotSpend(t *testing.T) {
	f := newFixture(t, func(cfg *LimiterConfig) {
		cfg.Accounts.SendsPerAccount = Rule{Limit: 5, Window: time.Hour}
	})

	// One address: 3 sends, then 20 refused per pair. Only the 3 count.
	for range 23 {
		_ = f.send("RequestMagicLink", attackerIP, victim)
	}
	if err := f.send("RequestMagicLink", victimIP, victim); err != nil {
		t.Fatalf("send 4 = %v", err)
	}
	if err := f.send("RequestPasswordReset", victimIP, victim); err != nil {
		t.Fatalf("send 5 = %v", err)
	}
	if err := f.send("RequestPasswordReset", "198.51.100.9", victim); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("send 6 = %v; want the account-wide cap", err)
	}
}

func TestPerIPLimitIsCheckedFirstAndRefusedRequestsSpendNoAccountBudget(t *testing.T) {
	var keys []string
	f := newFixture(t, func(cfg *LimiterConfig) {
		cfg.PerIP = Rule{Limit: 2, Window: time.Minute}
		cfg.Store = recordingStore{record: &keys, next: cfg.Store}
	})

	for range 5 {
		_ = f.login(attackerIP, victim, false)
		_ = f.send("RequestMagicLink", attackerIP, victim)
	}

	accountCharges := 0
	for _, key := range keys {
		if strings.HasPrefix(key, "account:") {
			accountCharges++
		}
		if strings.Contains(key, "victim") || strings.Contains(key, "example.com") {
			t.Fatalf("key %q holds the plaintext email", key)
		}
	}
	// Two requests per IP got through: one login (a peek and two failure
	// counts) and one send (per flow, per address, account-wide).
	if accountCharges != 6 {
		t.Fatalf("account keys touched %d times (%v); requests refused per IP must not touch them", accountCharges, keys)
	}
}

func TestInterceptorIgnoresProceduresItDoesNotGuard(t *testing.T) {
	f := newFixture(t, func(cfg *LimiterConfig) {
		cfg.Store = NewFallbackStore(&failingStore{}, NewMemoryStore(), true)
	})

	reached, err := f.call("GetSession", victimIP, "", func() error { return nil })
	if err != nil || !reached {
		t.Fatalf("GetSession = %v; hot read paths must not depend on the limiter", err)
	}
}

func TestInterceptorReturnsUnavailableWhenTheRequiredStoreIsDown(t *testing.T) {
	f := newFixture(t, func(cfg *LimiterConfig) {
		cfg.Store = NewFallbackStore(&failingStore{}, NewMemoryStore(), true)
	})

	reached, err := f.call("Login", victimIP, victim, func() error { return nil })
	if connect.CodeOf(err) != connect.CodeUnavailable || reached {
		t.Fatalf("err = %v, reached %v; want Unavailable before the handler", err, reached)
	}
}

func TestLimiterWrapSetsRetryAfterOnHTTPRoutes(t *testing.T) {
	limiter := NewLimiter(LimiterConfig{Store: NewMemoryStore(), PerIP: Rule{Limit: 3, Window: time.Minute}})
	served := 0
	handler := limiter.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		served++
	}))

	var rec *httptest.ResponseRecorder
	for range 4 {
		req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		req.RemoteAddr = "198.51.100.10:1234"
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d; want 429", rec.Code)
	}
	retryAfter, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || retryAfter < 1 || retryAfter > 60 {
		t.Fatalf("Retry-After = %q; want whole seconds within the window", rec.Header().Get("Retry-After"))
	}
	if served != 3 {
		t.Fatalf("served = %d; want 3", served)
	}
}

func TestLimiterWrapReturns503WhenTheRequiredStoreIsDown(t *testing.T) {
	limiter := NewLimiter(LimiterConfig{
		Store: NewFallbackStore(&failingStore{}, NewMemoryStore(), true),
		PerIP: Rule{Limit: 3, Window: time.Minute},
	})
	handler := limiter.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("a request must not pass when the limiter cannot count it")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/login", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503", rec.Code)
	}
}

type recordingStore struct {
	record *[]string
	next   Store
}

func (r recordingStore) Consume(ctx context.Context, key string, rule Rule) (Decision, error) {
	*r.record = append(*r.record, key)
	return r.next.Consume(ctx, key, rule)
}

func (r recordingStore) Peek(ctx context.Context, key string) (Window, error) {
	*r.record = append(*r.record, key)
	return r.next.Peek(ctx, key)
}
