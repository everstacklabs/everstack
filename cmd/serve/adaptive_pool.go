package serve

import (
	"net/http"
	"sync/atomic"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// ---- Adaptive pool tuning based on ingress ----
var inFlight int64
var totalRequests uint64
var latencyMsEWMA int64

func withInFlight(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		atomic.AddInt64(&inFlight, 1)
		defer atomic.AddInt64(&inFlight, -1)
		next.ServeHTTP(w, r)
		atomic.AddUint64(&totalRequests, 1)
		updateLatency(time.Since(start))
	})
}

func updateLatency(d time.Duration) {
	ms := d.Milliseconds()
	if ms <= 0 {
		return
	}
	old := atomic.LoadInt64(&latencyMsEWMA)
	if old == 0 {
		atomic.StoreInt64(&latencyMsEWMA, ms)
		return
	}
	// alpha=0.2: new = 0.8*old + 0.2*ms
	newV := (4*old + ms) / 5
	atomic.StoreInt64(&latencyMsEWMA, newV)
}

// startAdaptivePoolTuner adjusts sql DB pool sizes based on current ingress using Little's law.
// Pool size ≈ RPS × AvgLatency(s) × headroom.
func startAdaptivePoolTuner(pg *sqlx.DB, ch *sqlx.DB, capPG int, capCH int) {
	if pg == nil && ch == nil {
		return
	}
	const (
		minOpen  = 25 // raised from 10 — agent runtime has many concurrent DB consumers
		headroom = 15 // 1.5x represented as /10 factor (raised from 1.2x for bursty agent workloads)
	)
	if capPG <= 0 {
		capPG = 100 // raised from 50 — sessions, heartbeats, projections, checkpoints all need connections
	}
	if capCH <= 0 {
		capCH = 100
	}

	// Log pool stats periodically so pool exhaustion can be detected
	statsInterval := 0
	logPoolStats := func(pg *sqlx.DB, target int) {
		statsInterval++
		if statsInterval%15 != 0 { // every 30s (15 × 2s tick)
			return
		}
		if pg == nil {
			return
		}
		stats := pg.Stats()
		logger.WithFields(
			"pool_target", target,
			"open", stats.OpenConnections,
			"in_use", stats.InUse,
			"idle", stats.Idle,
			"wait_count", stats.WaitCount,
			"wait_duration_ms", stats.WaitDuration.Milliseconds(),
			"max_idle_closed", stats.MaxIdleClosed,
			"max_lifetime_closed", stats.MaxLifetimeClosed,
		).Info("adaptive_pool: pg stats")
	}

	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		var lastCount uint64
		lastTick := time.Now()
		for range ticker.C {
			curCount := atomic.LoadUint64(&totalRequests)
			now := time.Now()
			elapsed := now.Sub(lastTick).Seconds()
			if elapsed <= 0 {
				continue
			}
			delta := float64(curCount - lastCount)
			rps := delta / elapsed
			latMs := float64(atomic.LoadInt64(&latencyMsEWMA))
			if latMs <= 0 {
				latMs = 1
			}
			latSec := latMs / 1000.0
			target := int((rps*latSec*float64(headroom))/10.0 + 0.5)
			if target < minOpen {
				target = minOpen
			}
			if target > capPG {
				target = capPG
			}
			if pg != nil {
				pg.SetMaxOpenConns(target)
				pg.SetMaxIdleConns(target)
				logPoolStats(pg, target)
			}
			chTarget := target
			if chTarget > capCH {
				chTarget = capCH
			}
			if ch != nil {
				ch.SetMaxOpenConns(chTarget)
				ch.SetMaxIdleConns(chTarget)
			}
			lastTick = now
			lastCount = curCount
		}
	}()
}
