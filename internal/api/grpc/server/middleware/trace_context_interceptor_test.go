package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/types/known/emptypb"
)

const testTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

func TestTraceContextInterceptorExtractsUnaryParent(t *testing.T) {
	request := connect.NewRequest(&emptypb.Empty{})
	request.Header().Set("traceparent", testTraceparent)

	var got trace.SpanContext
	handler := NewTraceContextInterceptor().WrapUnary(
		func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
			got = trace.SpanContextFromContext(ctx)
			return connect.NewResponse(&emptypb.Empty{}), nil
		},
	)

	if _, err := handler(context.Background(), request); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	assertRemoteTestSpanContext(t, got)
}

func TestTraceContextInterceptorExtractsStreamingParent(t *testing.T) {
	conn := &traceContextTestStream{requestHeader: make(http.Header)}
	conn.requestHeader.Set("traceparent", testTraceparent)

	var got trace.SpanContext
	handler := NewTraceContextInterceptor().WrapStreamingHandler(
		func(ctx context.Context, _ connect.StreamingHandlerConn) error {
			got = trace.SpanContextFromContext(ctx)
			return nil
		},
	)

	if err := handler(context.Background(), conn); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	assertRemoteTestSpanContext(t, got)
}

func TestTraceContextHTTPHandlerExtractsParent(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	request.Header.Set("traceparent", testTraceparent)

	var got trace.SpanContext
	handler := TraceContextHTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = trace.SpanContextFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	assertRemoteTestSpanContext(t, got)
}

func assertRemoteTestSpanContext(t *testing.T, got trace.SpanContext) {
	t.Helper()
	if !got.IsValid() {
		t.Fatal("extracted span context is invalid")
	}
	if !got.IsRemote() {
		t.Fatal("extracted span context is not marked remote")
	}
	if got.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("trace id = %s", got.TraceID())
	}
	if got.SpanID().String() != "00f067aa0ba902b7" {
		t.Fatalf("span id = %s", got.SpanID())
	}
	if !got.IsSampled() {
		t.Fatal("extracted span context is not sampled")
	}
}

type traceContextTestStream struct {
	requestHeader   http.Header
	responseHeader  http.Header
	responseTrailer http.Header
}

func (s *traceContextTestStream) Spec() connect.Spec { return connect.Spec{} }
func (s *traceContextTestStream) Peer() connect.Peer { return connect.Peer{} }
func (s *traceContextTestStream) Receive(any) error  { return nil }
func (s *traceContextTestStream) RequestHeader() http.Header {
	return s.requestHeader
}
func (s *traceContextTestStream) Send(any) error { return nil }
func (s *traceContextTestStream) ResponseHeader() http.Header {
	if s.responseHeader == nil {
		s.responseHeader = make(http.Header)
	}
	return s.responseHeader
}
func (s *traceContextTestStream) ResponseTrailer() http.Header {
	if s.responseTrailer == nil {
		s.responseTrailer = make(http.Header)
	}
	return s.responseTrailer
}
