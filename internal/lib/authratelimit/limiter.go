package authratelimit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// AccountPolicy sets the per-account limits.
//
// Every budget that can refuse a request is keyed on the account AND the
// client address. A budget keyed on the account alone is a lockout switch:
// anyone who knows an email can spend it from anywhere, at a rate low enough
// to pass every per-IP limit, and keep the owner out indefinitely. Keyed per
// pair, an attacker only exhausts their own pair.
//
// What is account-wide is deliberately softer. Password failures past an
// account-wide cap slow further FAILED attempts by a bounded delay and log a
// warning; a correct password is never refused or delayed because of other
// addresses' failures. Mail to one account is capped per address across all
// flows, so one address cannot spend the account's mail by cycling flows,
// and then account-wide far above legitimate use: only a large distributed
// attack reaches that, it can then pause mail to the account (the price of
// not letting the service bomb an inbox), and it never touches password
// sign-in. Counts do not depend on whether the account exists.
type AccountPolicy struct {
	// PasswordProcedure names the procedure whose failures are counted.
	PasswordProcedure string
	// PasswordFailuresPerAccountAndIP refuses an address that keeps failing
	// for one account.
	PasswordFailuresPerAccountAndIP Rule
	// PasswordFailuresPerAccount is the account-wide failure count past
	// which failures are slowed. It never refuses.
	PasswordFailuresPerAccount Rule
	FailureDelayStep           time.Duration
	FailureDelayMax            time.Duration
	// SendProcedures name the procedures that mail the address in the
	// request; registration counts, since it mails the owner.
	SendProcedures map[string]bool
	// SendsPerAccountAndIP is per procedure; MailPerAccountAndIP spans them.
	SendsPerAccountAndIP Rule
	MailPerAccountAndIP  Rule
	SendsPerAccount      Rule
}

// DefaultAccountPolicy matches the identity service's per-account policy.
func DefaultAccountPolicy(passwordProcedure string, sendProcedures ...string) AccountPolicy {
	sends := make(map[string]bool, len(sendProcedures))
	for _, name := range sendProcedures {
		sends[name] = true
	}
	return AccountPolicy{
		PasswordProcedure: passwordProcedure,
		// Enough for a person working through their password manager; an
		// attacker gets 10 guesses per address per quarter hour.
		PasswordFailuresPerAccountAndIP: Rule{Limit: 10, Window: 15 * time.Minute},
		// Five addresses' worth of full budgets in an hour is not one person.
		PasswordFailuresPerAccount: Rule{Limit: 50, Window: time.Hour},
		FailureDelayStep:           250 * time.Millisecond,
		FailureDelayMax:            3 * time.Second,
		SendProcedures:             sends,
		SendsPerAccountAndIP:       Rule{Limit: 3, Window: 10 * time.Minute},
		// Without it one address cycling flows gets 3 per flow every 10
		// minutes; with it, what a person might plausibly ask for in an hour.
		MailPerAccountAndIP: Rule{Limit: 10, Window: time.Hour},
		// Ten addresses' worth of mail in an hour: a distributed attack.
		SendsPerAccount: Rule{Limit: 100, Window: time.Hour},
	}
}

// LimiterConfig wires a Limiter.
type LimiterConfig struct {
	// Store holds the counters. A Redis-backed store shares them between
	// replicas; a MemoryStore keeps them per process.
	Store Store
	// PerIP is charged once per guarded request, HTTP route or procedure
	// alike, against the caller's address, before any account budget.
	PerIP Rule
	// Procedure reports whether the interceptor guards a procedure name.
	Procedure func(name string) bool
	// Accounts sets the per-account limits for procedures carrying an email.
	Accounts AccountPolicy
	// Sleep holds back a failed password attempt; tests replace it.
	Sleep func(ctx context.Context, d time.Duration)
}

// Limiter enforces fixed-window limits on authentication endpoints through a
// Store, for HTTP routes (Wrap) and Connect procedures (Interceptor).
type Limiter struct {
	store     Store
	perIP     Rule
	procedure func(string) bool
	accounts  AccountPolicy
	sleep     func(context.Context, time.Duration)
}

func NewLimiter(cfg LimiterConfig) *Limiter {
	procedure := cfg.Procedure
	if procedure == nil {
		procedure = func(string) bool { return false }
	}
	sleep := cfg.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	return &Limiter{
		store:     cfg.Store,
		perIP:     cfg.PerIP,
		procedure: procedure,
		accounts:  cfg.Accounts,
		sleep:     sleep,
	}
}

func sleepContext(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// unavailableRetryAfter is what a caller is told when the required store is
// down: long enough not to hammer it, short enough to ride out a failover.
const unavailableRetryAfter = 5 * time.Second

// Wrap charges every request it sees to the caller's address. On a block it
// answers 429 with Retry-After; when a required store is down, 503.
func (l *Limiter) Wrap(next http.Handler) http.Handler {
	if l == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		decision, err := l.store.Consume(r.Context(), ipKey(ip), l.perIP)
		if err != nil {
			writeUnavailable(w, err, r.URL.Path)
			return
		}
		if !decision.Allowed {
			retryAfter := retryAfterSeconds(decision.RetryAfter)
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limit exceeded","retry_after_seconds":` + strconv.Itoa(retryAfter) + `}`))
			logger.WithFields("ip", ip, "path", r.URL.Path, "retry_after_s", retryAfter).
				Warn("auth: rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Interceptor limits guarded Connect procedures. It runs after Connect has
// decoded the request, which is why it can read the email for the
// per-account budgets regardless of protocol (Connect, gRPC, gRPC-Web) or
// encoding, why it can see whether a password attempt failed, and why a
// block surfaces as a proper ResourceExhausted rather than the raw HTTP 429 a
// Connect client would read as Unavailable.
func (l *Limiter) Interceptor() connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if req.Spec().IsClient {
				return next(ctx, req)
			}
			var resp connect.AnyResponse
			err := l.guard(
				ctx,
				req.Spec().Procedure,
				req.Header().Get("X-Forwarded-For"),
				req.Peer().Addr,
				req.Any(),
				func() error {
					var handlerErr error
					resp, handlerErr = next(ctx, req)
					return handlerErr
				},
			)
			return resp, err
		}
	})
}

type emailBearer interface{ GetEmail() string }

// guard runs handler under the limits for procedure: per IP first, then the
// per-account rules for the procedure.
func (l *Limiter) guard(ctx context.Context, procedure, xff, peerAddr string, msg any, handler func() error) error {
	name := procedure
	if i := strings.LastIndexByte(procedure, '/'); i >= 0 {
		name = procedure[i+1:]
	}
	if !l.procedure(name) {
		return handler()
	}
	ip := clientIPFromParts(xff, peerAddr, trustedProxies)
	decision, err := l.store.Consume(ctx, ipKey(ip), l.perIP)
	if err != nil {
		return unavailableError(err, procedure)
	}
	if !decision.Allowed {
		logger.WithFields("ip", ip, "procedure", procedure).Warn("auth: rate limit exceeded")
		return resourceExhausted(decision.RetryAfter)
	}

	accountScoped := name == l.accounts.PasswordProcedure || l.accounts.SendProcedures[name]
	if !accountScoped {
		return handler()
	}
	email := ""
	if bearer, ok := msg.(emailBearer); ok {
		email = normalizeEmail(bearer.GetEmail())
	}
	if email == "" {
		// These procedures need the account key. Running one without it
		// would be a guess or a mail no account budget counted.
		return connect.NewError(connect.CodeInvalidArgument, errors.New("a valid email is required"))
	}
	account := emailDigest(email)

	switch {
	case name == l.accounts.PasswordProcedure:
		return l.guardPassword(ctx, procedure, ip, account, handler)
	case l.accounts.SendProcedures[name]:
		// Each budget is charged only if the one before allowed the mail, so
		// a mail refused per address does not spend the account-wide cap.
		if err := l.charge(ctx, procedure, ip, "account:send:"+account+":"+ip+"|"+name, l.accounts.SendsPerAccountAndIP); err != nil {
			return err
		}
		if err := l.charge(ctx, procedure, ip, "account:mail:"+account+":"+ip+"|mail", l.accounts.MailPerAccountAndIP); err != nil {
			return err
		}
		if err := l.charge(ctx, procedure, ip, "account:mail:"+account+"|mail", l.accounts.SendsPerAccount); err != nil {
			return err
		}
	}
	return handler()
}

// guardPassword counts only failed attempts, which needs the handler's
// outcome. Before: a read-only look at this (account, address) pair, refused
// when spent; nothing is charged, so the check cannot be used to burn an
// account's budget. After: a rejected credential counts against the pair and
// the account-wide total; past the total's cap the failure is held back a
// little longer, up to a bound. A correct password returns as soon as the
// handler does.
func (l *Limiter) guardPassword(ctx context.Context, procedure, ip, account string, handler func() error) error {
	pairKey := "account:pw-fail:" + account + ":" + ip + "|" + l.accounts.PasswordProcedure
	accountKey := "account:pw-fail:" + account + "|" + l.accounts.PasswordProcedure

	pair, err := l.store.Peek(ctx, pairKey)
	if err != nil {
		return unavailableError(err, procedure)
	}
	if pair.Count >= int64(l.accounts.PasswordFailuresPerAccountAndIP.Limit) {
		logger.WithFields("ip", ip, "procedure", procedure).Warn("auth: password failures for this account from this address exceeded")
		return resourceExhausted(pair.RetryAfter)
	}

	handlerErr := handler()
	if connect.CodeOf(handlerErr) != connect.CodeUnauthenticated {
		// Success, or a challenge (MFA, email verification) that is not a
		// wrong password.
		return handlerErr
	}
	if _, err := l.store.Consume(ctx, pairKey, l.accounts.PasswordFailuresPerAccountAndIP); err != nil {
		logger.WithFields("procedure", procedure, "error", err.Error()).Error("auth: could not count a failed sign-in")
		return handlerErr
	}
	total, err := l.store.Consume(ctx, accountKey, l.accounts.PasswordFailuresPerAccount)
	if err != nil {
		logger.WithFields("procedure", procedure, "error", err.Error()).Error("auth: could not count a failed sign-in")
		return handlerErr
	}
	if over := total.Count - int64(l.accounts.PasswordFailuresPerAccount.Limit); over > 0 {
		delay := min(time.Duration(over)*l.accounts.FailureDelayStep, l.accounts.FailureDelayMax)
		// The account is deliberately not logged: not the email, not its hash.
		logger.WithFields("ip", ip, "procedure", procedure, "failures", total.Count, "delay_ms", delay.Milliseconds()).
			Warn("auth: failed sign-ins for one account are past the account-wide cap; slowing further failures")
		l.sleep(ctx, delay)
	}
	return handlerErr
}

func (l *Limiter) charge(ctx context.Context, procedure, ip, key string, rule Rule) error {
	decision, err := l.store.Consume(ctx, key, rule)
	if err != nil {
		return unavailableError(err, procedure)
	}
	if !decision.Allowed {
		// The account is deliberately not logged: not the email, not its hash.
		logger.WithFields("ip", ip, "procedure", procedure).Warn("auth: per-account rate limit exceeded")
		return resourceExhausted(decision.RetryAfter)
	}
	return nil
}

func ipKey(ip string) string { return "ip:" + ip }

// emailDigest hashes the address so the shared store never holds an email.
func emailDigest(email string) string {
	sum := sha256.Sum256([]byte(email))
	return hex.EncodeToString(sum[:])
}

// normalizeEmail matches how accounts are looked up, so changing case or
// adding spaces does not mint a fresh budget.
func normalizeEmail(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	if len(email) > 320 {
		return ""
	}
	return email
}

func retryAfterSeconds(d time.Duration) int {
	seconds := int(math.Ceil(d.Seconds()))
	if seconds < 1 {
		return 1
	}
	return seconds
}

func resourceExhausted(retryAfter time.Duration) error {
	err := connect.NewError(connect.CodeResourceExhausted, errors.New("too many attempts, try again later"))
	err.Meta().Set("Retry-After", strconv.Itoa(retryAfterSeconds(retryAfter)))
	return err
}

func unavailableError(cause error, procedure string) error {
	logger.WithFields("procedure", procedure, "error", cause.Error()).Error("auth: rate-limit store unavailable")
	err := connect.NewError(connect.CodeUnavailable, errors.New("sign-in is temporarily unavailable, try again shortly"))
	err.Meta().Set("Retry-After", strconv.Itoa(retryAfterSeconds(unavailableRetryAfter)))
	return err
}

func writeUnavailable(w http.ResponseWriter, cause error, path string) {
	logger.WithFields("path", path, "error", cause.Error()).Error("auth: rate-limit store unavailable")
	w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(unavailableRetryAfter)))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`{"error":"rate limiting unavailable"}`))
}
