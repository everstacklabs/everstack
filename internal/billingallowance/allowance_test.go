package billingallowance

import (
	"math"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/pkg/plans"
)

func catalog(t *testing.T) *plans.PlansConfig {
	t.Helper()
	cfg, err := plans.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestForTierPerCounter(t *testing.T) {
	cfg := catalog(t)
	cases := []struct {
		tier     string
		c        Counter
		included int64
		hour     int64
		unlim    bool
	}{
		{"free", SandboxCPU, 2_016_000, 50_400, false},
		{"free", SandboxMemory, 648_000, 16_200, false},
		{"free", BrowserTime, 250_000, 50_000, false},
		{"basic", SandboxCPU, 7_560_000, 50_400, false},
		{"basic", SandboxMemory, 2_430_000, 16_200, false},
		{"basic", BrowserTime, 5_000_000, 50_000, false},
		{"pro", SandboxCPU, 37_800_000, 50_400, false},
		{"pro", SandboxMemory, 12_150_000, 16_200, false},
		{"pro", BrowserTime, 25_000_000, 50_000, false},
		{"enterprise", SandboxCPU, 0, 50_400, true},
		{"no-such-tier", SandboxCPU, 2_016_000, 50_400, false}, // unknown tiers get Starter
		{" FREE ", BrowserTime, 250_000, 50_000, false},
	}
	for _, c := range cases {
		got := ForTier(cfg, c.tier, c.c)
		if got.IncludedMicros != c.included || got.HourMicros != c.hour || got.Unlimited != c.unlim {
			t.Errorf("ForTier(%q, %s) = %+v, want included=%d hour=%d unlimited=%t", c.tier, c.c.LimitType, got, c.included, c.hour, c.unlim)
		}
	}
	if got := ForTier(nil, "free", SandboxCPU); got.IncludedMicros != 0 || got.Unlimited {
		t.Errorf("no catalog = %+v, want no allowance", got)
	}
	bare := &plans.PlansConfig{Plans: map[string]plans.Plan{"free": {Tier: "free"}},
		SandboxComputePricing: cfg.SandboxComputePricing, BrowserRuntimePricing: cfg.BrowserRuntimePricing}
	if got := ForTier(bare, "free", SandboxCPU); got.IncludedMicros != 0 || got.Unlimited {
		t.Errorf("plan without allowance = %+v, want none", got)
	}
}

func TestCountersFor(t *testing.T) {
	if got := CountersFor(MetricSandboxCostMicros); len(got) != 2 || got[0] != SandboxCPU || got[1] != SandboxMemory {
		t.Errorf("sandbox counters = %+v", got)
	}
	if got := CountersFor(MetricBrowserCostMicros); len(got) != 1 || got[0] != BrowserTime {
		t.Errorf("browser counters = %+v", got)
	}
	if got := CountersFor("otlp.processed_bytes"); len(got) != 0 {
		t.Errorf("processed bytes counters = %+v", got)
	}
}

func seg(baseline, included float64) Segment { return Segment{Baseline: baseline, Included: included} }

func TestFreeWithinSplitsAcrossSegments(t *testing.T) {
	one := []Segment{seg(0, 1_000)}
	two := []Segment{seg(0, 1_000), seg(1_500, 1_000)} // first period used 1500, second began
	cases := []struct {
		name      string
		segs      []Segment
		prev, cur float64
		want      float64
	}{
		{"inside", one, 0, 500, 500},
		{"up to the boundary", one, 500, 1_000, 500},
		{"crosses: split at 1000", one, 900, 1_300, 100},
		{"beyond", one, 1_200, 1_500, 0},
		{"no growth", one, 100, 100, 0},
		{"old period's overage stays charged", two, 1_000, 1_500, 0},
		{"backlog across the boundary", two, 1_000, 1_800, 300},
		{"new period full allowance", two, 1_500, 2_500, 1_000},
		{"growth before the first segment is free", []Segment{seg(400, 100)}, 100, 600, 400},
		{"unlimited", []Segment{seg(0, -1)}, 0, 1e9, 1e9},
		{"no segments", nil, 0, 100, 0},
	}
	for _, c := range cases {
		if got := FreeWithin(c.segs, c.prev, c.cur); got != c.want {
			t.Errorf("%s: FreeWithin(%v, %v) = %v, want %v", c.name, c.prev, c.cur, got, c.want)
		}
	}
}

func TestFallbackBaselineFavoursCustomer(t *testing.T) {
	cases := []struct {
		name      string
		segs      []Segment
		prev, cur float64
		want      float64
	}{
		{"first segment starts at the watermark", nil, 300, 900, 300},
		{"old room first", []Segment{seg(0, 1_000)}, 700, 2_000, 1_000},
		{"old room covers the whole backlog", []Segment{seg(0, 1_000)}, 700, 800, 800},
		{"old allowance used up", []Segment{seg(0, 1_000)}, 1_200, 2_000, 1_200},
		{"no growth", []Segment{seg(0, 1_000)}, 1_200, 1_200, 1_200},
		{"unlimited old segment keeps the backlog", []Segment{seg(0, -1)}, 10, 90, 90},
	}
	for _, c := range cases {
		if got := fallbackBaseline(c.segs, c.prev, c.cur); got != c.want {
			t.Errorf("%s: fallbackBaseline = %v, want %v", c.name, got, c.want)
		}
		// Whatever the attribution, the free part is the most the two
		// allowances can cover.
		segs := append(append([]Segment(nil), c.segs...), seg(c.want, 1_000))
		free := FreeWithin(segs, c.prev, c.cur)
		if free < 0 || free > c.cur-c.prev+1e-9 || math.IsNaN(free) {
			t.Errorf("%s: free = %v outside [0, %v]", c.name, free, c.cur-c.prev)
		}
	}
}

func TestPeriod(t *testing.T) {
	utc := func(y int, m time.Month, d, h int) time.Time { return time.Date(y, m, d, h, 0, 0, 0, time.UTC) }
	anchor := utc(2026, 9, 25, 18)
	monthly := &Subscription{PeriodStart: anchor, PeriodEnd: anchor.AddDate(0, 1, 0)}
	yearly := &Subscription{PeriodStart: anchor, PeriodEnd: anchor.AddDate(1, 0, 0)}
	clamped := &Subscription{PeriodStart: utc(2027, 1, 31, 0), PeriodEnd: utc(2027, 2, 28, 0)}
	cases := []struct {
		name       string
		tier       string
		sub        *Subscription
		now        time.Time
		start, end time.Time
	}{
		{"starter calendar month", "free", nil, utc(2026, 10, 12, 9), utc(2026, 10, 1, 0), utc(2026, 11, 1, 0)},
		{"starter with a subscription row still calendar", "free", monthly, utc(2026, 10, 12, 9), utc(2026, 10, 1, 0), utc(2026, 11, 1, 0)},
		{"paid live period", "basic", monthly, utc(2026, 10, 12, 9), anchor, anchor.AddDate(0, 1, 0)},
		{"short-month clamp stays one period", "pro", &Subscription{PeriodStart: utc(2027, 2, 28, 0), PeriodEnd: utc(2027, 3, 31, 0)}, utc(2027, 3, 29, 0), utc(2027, 2, 28, 0), utc(2027, 3, 31, 0)},
		{"renewal not recorded: next window starts at the period end", "pro", monthly, utc(2026, 10, 26, 0), anchor.AddDate(0, 1, 0), anchor.AddDate(0, 2, 0)},
		{"clamped anchor renewing late opens Stripe's next start", "basic", clamped, utc(2027, 3, 2, 0), utc(2027, 2, 28, 0), utc(2027, 3, 28, 0)},
		{"very late renewal still one window", "basic", clamped, utc(2027, 4, 15, 0), utc(2027, 2, 28, 0), utc(2027, 3, 28, 0)},
		{"yearly steps monthly", "basic", yearly, utc(2027, 2, 1, 0), anchor.AddDate(0, 4, 0), anchor.AddDate(0, 5, 0)},
		{"yearly last window ends with the period", "basic", yearly, utc(2027, 9, 20, 0), anchor.AddDate(0, 11, 0), anchor.AddDate(1, 0, 0)},
		{"paid without a period", "basic", nil, utc(2026, 10, 12, 9), utc(2026, 10, 1, 0), utc(2026, 11, 1, 0)},
		{"clock before the period", "basic", monthly, utc(2026, 9, 1, 0), anchor, anchor.AddDate(0, 1, 0)},
		{"broken period", "basic", &Subscription{PeriodStart: anchor, PeriodEnd: anchor}, utc(2026, 10, 12, 9), utc(2026, 10, 1, 0), utc(2026, 11, 1, 0)},
	}
	for _, c := range cases {
		s, e := Period(c.tier, c.sub, c.now)
		if !s.Equal(c.start) || !e.Equal(c.end) {
			t.Errorf("%s: Period = %s..%s, want %s..%s", c.name, s, e, c.start, c.end)
		}
	}
}

func TestStatusSeconds(t *testing.T) {
	s := Status{Allowance: Allowance{Counter: SandboxCPU, IncludedMicros: 2_016_000, HourMicros: 50_400}, UsedMicros: 25_200}
	if s.UsedSeconds() != 1_800 || s.IncludedSeconds() != 40*3600 || s.Exhausted() {
		t.Fatalf("status = %+v used=%d included=%d", s, s.UsedSeconds(), s.IncludedSeconds())
	}
	s.UsedMicros = 2_016_000
	if !s.Exhausted() || s.RemainingMicros() != 0 {
		t.Fatalf("used up status = %+v", s)
	}
	u := Status{Allowance: Allowance{Counter: BrowserTime, Unlimited: true, HourMicros: 50_000}, UsedMicros: 1e12}
	if u.Exhausted() || u.IncludedSeconds() != -1 {
		t.Fatalf("unlimited status = %+v", u)
	}
}
