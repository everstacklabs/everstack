package interceptors

import (
	"fmt"
	"net/http"
	"time"
)

func CallDurationHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startTime := time.Now()
		next.ServeHTTP(w, r)
		duration := time.Since(startTime)
		durationMs := duration.Milliseconds()
		w.Header().Set("x-request-duration-ms", fmt.Sprintf("%d", durationMs))
	})
}

// CallDurationHandler returns a middleware that measures and sets per-request duration.
func CallDurationHandlerFunc() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return CallDurationHandler(next) }
}
