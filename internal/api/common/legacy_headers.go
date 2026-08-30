package common

import (
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// legacySampleEvery bounds how often a given legacy header's usage is logged, so
// hot auth paths don't spam logs while still giving a retirement signal.
const legacySampleEvery = 1000

// legacyHits tracks per-header fallback counts (header name -> *int64) so each
// legacy alias produces its own retirement signal, independent of traffic to the
// others. Intentionally lightweight — this is telemetry, not a metrics system.
var legacyHits sync.Map

// GetHeader returns the value of the canonical header if present; otherwise it
// falls back to each legacy name in order and records the fallback. The canonical
// name always wins when multiple are present.
//
// get is http.Header.Get or any func(string) string (e.g. a gRPC metadata
// lookup), so the same precedence works for REST and Connect/gRPC read-sites.
func GetHeader(get func(string) string, canonical string, legacy ...string) string {
	if v := get(canonical); v != "" {
		return v
	}
	for _, name := range legacy {
		if v := get(name); v != "" {
			noteLegacyHeader(name)
			return v
		}
	}
	return ""
}

// GetHTTPHeader is GetHeader specialized for http.Header.
func GetHTTPHeader(h http.Header, canonical string, legacy ...string) string {
	return GetHeader(h.Get, canonical, legacy...)
}

// noteLegacyHeader records that a legacy header name supplied a value. It logs
// the first hit and every legacySampleEvery-th hit per header, so we can see
// which legacy aliases are still in use before dropping them.
func noteLegacyHeader(name string) {
	ctr, _ := legacyHits.LoadOrStore(name, new(int64))
	n := atomic.AddInt64(ctr.(*int64), 1)
	if n == 1 || n%legacySampleEvery == 0 {
		logger.WithFields(
			"header", name,
			"count", n,
			"canonical_prefix", "x-evs-",
		).Info("legacy request header in use (accepted for backward compatibility; retire once count stops growing)")
	}
}
