package middleware

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/everstacklabs/everstack/internal/billingmeter"
)

// SpendLimitStatus answers the Cloud spend limit refusal as HTTP 402 with an
// OpenAI-shaped error, instead of the 429 its gRPC code (ResourceExhausted)
// maps to in grpc-gateway. OpenAI SDKs retry 429 automatically, which is
// wrong for a cap that only resets at 00:00 UTC.
//
// It wraps the grpc-gateway mux directly. Only the refusal's reason survives
// the gRPC hop, as a google.rpc.ErrorInfo detail, so only a 429 is held: its
// body (always terminal) is buffered and, if it carries the
// spend_limit_exceeded reason, replaced by
//
//	{"error":{"message":...,"type":"insufficient_quota","param":null,"code":"spend_limit_exceeded"}}
//
// The model trial's refusals (docs/design/free-trial-onboarding.md 3.6) take
// the same path: a model_trial_* reason is answered 402 with the same body
// shape and the reason as code, except model_trial_rate_limited, which stays
// 429 and carries Retry-After (an SDK retry after the interval is wanted).
// A served trial response carries x-evs-model-trial, which grpc-gateway
// forwards as Grpc-Metadata-X-Evs-Model-Trial; it is renamed here.
//
// Every other response, including a real rate limit, passes through
// unchanged and unbuffered.
func SpendLimitStatus(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &spendLimitStatusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		sw.finish()
	})
}

type spendLimitStatusWriter struct {
	http.ResponseWriter
	wroteHeader bool
	held        bool
	buf         bytes.Buffer
}

// grpcModelTrialHeader is how grpc-gateway forwards the x-evs-model-trial
// response metadata.
var grpcModelTrialHeader = http.CanonicalHeaderKey("Grpc-Metadata-X-Evs-Model-Trial")

func (w *spendLimitStatusWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	h := w.ResponseWriter.Header()
	if v := h.Get(grpcModelTrialHeader); v != "" {
		h.Set("X-Evs-Model-Trial", v)
		h.Del(grpcModelTrialHeader)
	}
	if code == http.StatusTooManyRequests {
		w.held = true
		return
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *spendLimitStatusWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.held {
		return w.buf.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

// Flush is required by grpc-gateway's streaming forwarder; a held error is
// flushed by finish.
func (w *spendLimitStatusWriter) Flush() {
	if w.held {
		return
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *spendLimitStatusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *spendLimitStatusWriter) finish() {
	if !w.held {
		return
	}
	if ref, ok := quotaRefusal(w.buf.Bytes()); ok {
		status, errType := http.StatusPaymentRequired, "insufficient_quota"
		if ref.reason == billingmeter.ReasonModelTrialRateLimited {
			status, errType = http.StatusTooManyRequests, "rate_limit_exceeded"
		}
		body, err := json.Marshal(map[string]any{
			"error": map[string]any{
				"message": ref.message,
				"type":    errType,
				"param":   nil,
				"code":    ref.reason,
			},
		})
		if err == nil {
			h := w.ResponseWriter.Header()
			h.Set("Content-Type", "application/json")
			h.Set("Content-Length", strconv.Itoa(len(body)))
			if status == http.StatusTooManyRequests {
				retry := ref.metadata["retry_after_seconds"]
				if n, err := strconv.Atoi(retry); err != nil || n < 1 {
					retry = "1"
				}
				h.Set("Retry-After", retry)
			}
			w.ResponseWriter.WriteHeader(status)
			_, _ = w.ResponseWriter.Write(body)
			return
		}
	}
	w.ResponseWriter.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.ResponseWriter.Write(w.buf.Bytes())
}

type refusal struct {
	reason   string
	message  string
	metadata map[string]string
}

// quotaRefusal reports whether a grpc-gateway error body, the stream shape
// {"error":{"code","message","details"}} or the unary shape
// {"code","message","details"}, carries the spend limit reason or a model
// trial reason, and returns it with its message and metadata.
func quotaRefusal(body []byte) (refusal, bool) {
	type status struct {
		Message string `json:"message"`
		Details []struct {
			Reason   string            `json:"reason"`
			Metadata map[string]string `json:"metadata"`
		} `json:"details"`
	}
	var probe struct {
		Error *status `json:"error"`
		status
	}
	if json.Unmarshal(bytes.TrimSpace(body), &probe) != nil {
		return refusal{}, false
	}
	st := probe.status
	if probe.Error != nil {
		st = *probe.Error
	}
	if st.Message == "" {
		return refusal{}, false
	}
	for _, d := range st.Details {
		if d.Reason == billingmeter.ReasonSpendLimitExceeded || strings.HasPrefix(d.Reason, "model_trial_") {
			return refusal{reason: d.Reason, message: st.Message, metadata: d.Metadata}, true
		}
	}
	return refusal{}, false
}
