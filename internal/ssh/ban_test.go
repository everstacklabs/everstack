package ssh

import (
	"testing"
	"time"
)

// TestFastTierBansABurst is the case the original policy was written for:
// several bad keys in quick succession.
func TestFastTierBansABurst(t *testing.T) {
	p := NewProxy(ProxyConfig{})
	const ip = "203.0.113.10"

	for i := 0; i < maxFailuresPerWindow-1; i++ {
		p.recordAuthFailure(ip)
		if p.isBanned(ip) {
			t.Fatalf("banned after %d failures, want ban only at %d", i+1, maxFailuresPerWindow)
		}
	}
	p.recordAuthFailure(ip)
	if !p.isBanned(ip) {
		t.Errorf("not banned after %d rapid failures", maxFailuresPerWindow)
	}
}

// TestSlowScannerIsBanned reproduces the eu-gra-1-prod pattern: one failed
// handshake roughly every three minutes, sustained for hours. It stays under
// the fast threshold at every instant because failures older than
// failureWindow are trimmed before that threshold is checked, so before the
// slow tier existed this ran unbanned for 14 hours and 1,649 attempts.
func TestSlowScannerIsBanned(t *testing.T) {
	p := NewProxy(ProxyConfig{})
	const ip = "203.0.113.20"
	now := time.Now()

	// Seed the history the pacing would have produced: one failure every
	// three minutes, all inside the slow window, none of them clustered
	// enough to trip the fast tier.
	entry := &banEntry{}
	for i := maxFailuresPerSlowWindow - 1; i > 0; i-- {
		entry.failures = append(entry.failures, now.Add(-time.Duration(i)*3*time.Minute))
	}
	p.bans[ip] = entry

	if got := countFailuresSince(entry.failures, now.Add(-failureWindow)); got >= maxFailuresPerWindow {
		t.Fatalf("seeded history trips the fast tier (%d in window); the test would not prove anything", got)
	}
	if p.isBanned(ip) {
		t.Fatal("banned before the slow threshold was reached")
	}

	p.recordAuthFailure(ip)
	if !p.isBanned(ip) {
		t.Errorf("slow scanner not banned after %d paced failures", maxFailuresPerSlowWindow)
	}
}

// TestSuccessClearsBanState protects the NAT case: one address can carry many
// legitimate users, and proving possession of a valid key has to wipe the
// history a neighbour accumulated.
func TestSuccessClearsBanState(t *testing.T) {
	p := NewProxy(ProxyConfig{})
	const ip = "203.0.113.30"

	for i := 0; i < maxFailuresPerSlowWindow-1; i++ {
		p.recordAuthFailure(ip)
	}
	p.recordAuthSuccess(ip)

	if p.isBanned(ip) {
		t.Error("still banned after a successful handshake")
	}
	if got := p.recordAuthFailure(ip); got != 1 {
		t.Errorf("failure count after success = %d, want 1 (history should be cleared)", got)
	}
}

// TestRecordAuthFailureReportsWindowCount covers the log de-escalation: the
// caller logs the first failure at Warn and the repeats at Debug, so the count
// has to start at 1 and climb.
func TestRecordAuthFailureReportsWindowCount(t *testing.T) {
	p := NewProxy(ProxyConfig{})
	const ip = "203.0.113.40"

	for want := 1; want <= 3; want++ {
		if got := p.recordAuthFailure(ip); got != want {
			t.Errorf("recordAuthFailure() call %d returned %d, want %d", want, got, want)
		}
	}
	if got := p.recordAuthFailure(""); got != 0 {
		t.Errorf("recordAuthFailure(\"\") = %d, want 0", got)
	}
}

// TestAppendFailureIsBounded stops a flood from growing per-IP state without
// limit, and drops history that has aged out of the slow window.
func TestAppendFailureIsBounded(t *testing.T) {
	now := time.Now()

	var failures []time.Time
	for i := 0; i < maxFailuresPerSlowWindow*3; i++ {
		failures = appendFailure(failures, now)
	}
	if len(failures) != maxFailuresPerSlowWindow {
		t.Errorf("len(failures) = %d, want it capped at %d", len(failures), maxFailuresPerSlowWindow)
	}

	stale := []time.Time{now.Add(-slowFailureWindow - time.Minute)}
	if got := appendFailure(stale, now); len(got) != 1 {
		t.Errorf("stale entry survived trimming: %v", got)
	}
}

// TestBannedRejectionsAnnounceOncePerBan covers the accept-path noise. Banning
// a scanner does not stop it knocking, so without this the 1,649 handshake
// Warns would simply have become accept-time Warns describing the ban working
// — a line even less actionable than the failure it replaced.
func TestBannedRejectionsAnnounceOncePerBan(t *testing.T) {
	p := NewProxy(ProxyConfig{})
	const ip = "203.0.113.50"

	if banned, n := p.noteBannedRejection(ip); banned || n != 0 {
		t.Fatalf("unknown IP reported banned=%v n=%d, want false/0", banned, n)
	}

	for i := 0; i < maxFailuresPerWindow; i++ {
		p.recordAuthFailure(ip)
	}
	if !p.isBanned(ip) {
		t.Fatal("setup failed: IP is not banned")
	}

	// First refusal announces, the rest are counted but quiet.
	for want := 1; want <= 4; want++ {
		banned, n := p.noteBannedRejection(ip)
		if !banned {
			t.Fatalf("refusal %d: banned = false, want true", want)
		}
		if n != want {
			t.Errorf("refusal %d: count = %d, want %d", want, n, want)
		}
	}

	// A ban is only re-armed once the previous one has expired, which is the
	// real cycle for a persistent scanner: ban lapses, it knocks again, trips
	// again. The counter must reset so the new ban is announced rather than
	// staying silent forever behind a stale count.
	p.bans[ip].bannedUntil = time.Now().Add(-time.Minute)
	p.bans[ip].failures = nil
	if banned, _ := p.noteBannedRejection(ip); banned {
		t.Fatal("expired ban still reported as banned")
	}
	for i := 0; i < maxFailuresPerWindow; i++ {
		p.recordAuthFailure(ip)
	}
	if _, n := p.noteBannedRejection(ip); n != 1 {
		t.Errorf("count after a new ban was armed = %d, want 1", n)
	}
}
