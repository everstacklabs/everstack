package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	gatewayv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1/gatewayconnect"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
)

// These tests are the direct transport's contract: for the same handlers and
// the same call, it must be indistinguishable from the HTTP/2 loopback it
// replaces, both to the handlers (the request they are served) and to the
// caller (messages, header and trailer metadata, the error).

const equivalenceEndpoint = "gateway.invalid:8089"

// seenRequest is everything about a served request a handler could act on.
type seenRequest struct {
	Method, Path, RawQuery, RequestURI, Proto, Host, RemoteAddr string
	ContentLength                                               int64
	TransferEncoding                                            []string
	Header                                                      map[string][]string
	Trailer                                                     map[string][]string
	Body                                                        []byte
	TLS                                                         bool
	LocalAddr                                                   string
	HasServer                                                   bool
	HasDeadline                                                 bool
	CallerValue                                                 any
}

type callerKey struct{}

// recordRequests wraps next and records each request it is served.
type recordRequests struct {
	next http.Handler
	mu   sync.Mutex
	seen []seenRequest
	// A handler can start after its caller has already returned (a reset
	// stream is reported before the handler runs), so take waits for the
	// recorder to go quiet instead of reading what has arrived so far.
	inflight int
	last     time.Time
}

func (rr *recordRequests) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rr.mu.Lock()
	rr.inflight++
	rr.last = time.Now()
	rr.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	_, hasDeadline := r.Context().Deadline()
	seen := seenRequest{
		Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery, RequestURI: r.RequestURI,
		Proto: r.Proto, Host: r.Host, RemoteAddr: r.RemoteAddr,
		ContentLength: r.ContentLength, TransferEncoding: r.TransferEncoding,
		Header: map[string][]string{}, Trailer: map[string][]string(r.Trailer),
		Body: body, TLS: r.TLS != nil,
		LocalAddr:   fmt.Sprint(r.Context().Value(http.LocalAddrContextKey)),
		HasServer:   r.Context().Value(http.ServerContextKey) != nil,
		HasDeadline: hasDeadline,
		CallerValue: r.Context().Value(callerKey{}),
	}
	for key, values := range r.Header {
		switch key {
		case "Grpc-Timeout":
			// The remaining time differs by when the call was made.
			seen.Header[key] = []string{"<" + values[0][len(values[0])-1:] + ">"}
		case "Grpc-Accept-Encoding":
			// grpc-go offers the compressors linked into the binary; the
			// direct transport offers none, so responses are never compressed.
		default:
			seen.Header[key] = append([]string(nil), values...)
		}
	}
	rr.mu.Lock()
	rr.seen = append(rr.seen, seen)
	rr.inflight--
	rr.last = time.Now()
	rr.mu.Unlock()
	rr.next.ServeHTTP(w, r)
}

// take returns the requests served since the last take, once no handler is
// part way through recording one and nothing has arrived for a short while.
func (rr *recordRequests) take() []seenRequest {
	const settle = 30 * time.Millisecond
	called := time.Now()
	deadline := called.Add(2 * time.Second)
	for {
		rr.mu.Lock()
		quietSince := rr.last
		if called.After(quietSince) {
			quietSince = called
		}
		if (rr.inflight == 0 && time.Since(quietSince) >= settle) || time.Now().After(deadline) {
			seen := rr.seen
			rr.seen = nil
			rr.mu.Unlock()
			return seen
		}
		rr.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
}

// statsEvent is one stats.Handler callback, reduced to what the tracing
// handler reads.
type statsEvent struct {
	Kind   string
	Detail string
}

// recordStats is a stats.Handler that records the calls it is told about and
// adds a header to each, the way the tracing handler injects a trace parent.
type recordStats struct {
	mu     sync.Mutex
	events []statsEvent
}

type statsTagKey struct{}

func (rs *recordStats) TagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	rs.add("tag", info.FullMethodName)
	ctx = metadata.AppendToOutgoingContext(ctx, "x-injected-by-stats", "yes")
	return context.WithValue(ctx, statsTagKey{}, true)
}

func (rs *recordStats) HandleRPC(ctx context.Context, s stats.RPCStats) {
	tagged := ctx.Value(statsTagKey{}) != nil
	switch e := s.(type) {
	case *stats.Begin:
		rs.add("begin", fmt.Sprintf("client=%v tagged=%v clientStream=%v serverStream=%v", e.Client, tagged, e.IsClientStream, e.IsServerStream))
	case *stats.OutHeader:
		rs.add("out-header", fmt.Sprintf("client=%v tagged=%v method=%s remote=%v", e.Client, tagged, e.FullMethod, e.RemoteAddr))
	case *stats.OutPayload:
		rs.add("out-payload", fmt.Sprintf("client=%v tagged=%v length=%d wire=%d", e.Client, tagged, e.Length, e.WireLength))
	case *stats.InPayload:
		rs.add("in-payload", fmt.Sprintf("client=%v tagged=%v length=%d wire=%d", e.Client, tagged, e.Length, e.WireLength))
	case *stats.End:
		rs.add("end", fmt.Sprintf("client=%v tagged=%v code=%v", e.Client, tagged, status.Code(e.Error)))
	}
}

func (rs *recordStats) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context { return ctx }
func (rs *recordStats) HandleConn(context.Context, stats.ConnStats)                       {}

func (rs *recordStats) add(kind, detail string) {
	rs.mu.Lock()
	rs.events = append(rs.events, statsEvent{kind, detail})
	rs.mu.Unlock()
}

// take returns the events of the calls made since the last take. A call that
// was reported at all must have been reported finished: both transports end a
// call before its last method returns to the caller. A call refused before it
// started is not reported, and leaves no events.
func (rs *recordStats) take(t *testing.T) []statsEvent {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		rs.mu.Lock()
		events := rs.events
		ended := len(events) == 0 || events[len(events)-1].Kind == "end"
		if ended || time.Now().After(deadline) {
			rs.events = nil
			rs.mu.Unlock()
			if !ended {
				t.Fatalf("a call was never reported finished: %v", events)
			}
			return events
		}
		rs.mu.Unlock()
		time.Sleep(2 * time.Millisecond)
	}
}

// outcome is what a caller observed from one call.
type outcome struct {
	Messages  []string
	Header    map[string][]string
	HeaderNil bool
	Trailer   map[string][]string
	Code      codes.Code
	Message   string
	Details   []string
}

func normaliseMD(md metadata.MD) map[string][]string {
	if md == nil {
		return nil
	}
	out := map[string][]string{}
	for key, values := range md {
		if key == "date" {
			out[key] = []string{"<date>"}
			continue
		}
		out[key] = append([]string(nil), values...)
	}
	return out
}

func (o *outcome) setError(err error) {
	if err == nil || err == io.EOF {
		o.Code = codes.OK
		return
	}
	st, _ := status.FromError(err)
	o.Code, o.Message = st.Code(), st.Message()
	for _, detail := range st.Details() {
		if msg, ok := detail.(proto.Message); ok {
			o.Details = append(o.Details, prototext.MarshalOptions{}.Format(msg))
		} else {
			o.Details = append(o.Details, fmt.Sprint(detail))
		}
	}
}

// transportPair is the same handler reachable over both transports.
type transportPair struct {
	names    [2]string
	conns    [2]grpc.ClientConnInterface
	requests *recordRequests
	stats    [2]*recordStats
}

func activityLikeInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-added-by-interceptor", method)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

func newTransportPair(t *testing.T, handler http.Handler) *transportPair {
	t.Helper()
	requests := &recordRequests{next: handler}
	pair := &transportPair{names: [2]string{"http2", "direct"}, requests: requests}
	pair.stats[0], pair.stats[1] = &recordStats{}, &recordStats{}

	h2 := NewLoopback()
	stopH2 := h2.Serve(requests)
	dialOpts := append(h2.DialOptions(),
		grpc.WithStatsHandler(pair.stats[0]),
		grpc.WithChainUnaryInterceptor(activityLikeInterceptor()),
	)
	conn, err := grpc.NewClient(h2.Target(equivalenceEndpoint), dialOpts...)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	pair.conns[0] = conn

	direct := NewLoopback()
	stopDirect := direct.Serve(requests)
	pair.conns[1] = direct.ClientConn(equivalenceEndpoint,
		WithDirectStatsHandler(pair.stats[1]),
		WithDirectUnaryInterceptor(activityLikeInterceptor()),
	)
	t.Cleanup(func() {
		_ = conn.Close()
		stopH2()
		stopDirect()
	})
	return pair
}

func withoutOutPayload(events []statsEvent) []statsEvent {
	var kept []statsEvent
	for _, e := range events {
		if e.Kind != "out-payload" {
			kept = append(kept, e)
		}
	}
	return kept
}

// compare runs call over both transports and requires the caller's outcome,
// the requests the handler was served and the stats events to be identical.
func (p *transportPair) compare(t *testing.T, call func(ctx context.Context, cc grpc.ClientConnInterface) outcome) outcome {
	t.Helper()
	var outcomes [2]outcome
	var served [2][]seenRequest
	var events [2][]statsEvent
	for i := range p.conns {
		ctx := context.WithValue(context.Background(), callerKey{}, "caller-only")
		outcomes[i] = call(ctx, p.conns[i])
		events[i] = p.stats[i].take(t)
		served[i] = p.requests.take()
	}
	if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
		t.Errorf("the caller saw different outcomes\n%s: %+v\n%s: %+v", p.names[0], outcomes[0], p.names[1], outcomes[1])
	}
	if !reflect.DeepEqual(served[0], served[1]) {
		t.Errorf("the handler was served different requests\n%s: %+v\n%s: %+v", p.names[0], served[0], p.names[1], served[1])
	}
	// A request the HTTP/2 server refuses before any handler runs (a
	// connection-specific header, for one) is refused either before or after
	// grpc-go has written the message, so whether its client records an
	// out-payload event is a race inside grpc-go. For a call that reached no
	// handler on either transport, compare the events without it.
	if len(served[0]) == 0 && len(served[1]) == 0 {
		for i := range events {
			events[i] = withoutOutPayload(events[i])
		}
	}
	if !reflect.DeepEqual(events[0], events[1]) {
		t.Errorf("the stats handler saw different calls\n%s: %+v\n%s: %+v", p.names[0], events[0], p.names[1], events[1])
	}
	for _, req := range served[1] {
		if req.CallerValue != nil {
			t.Errorf("a value from the caller's context reached the handler: %v", req.CallerValue)
		}
	}
	return outcomes[1]
}

func chatCall(model string, opts ...grpc.CallOption) func(context.Context, grpc.ClientConnInterface) outcome {
	return func(ctx context.Context, cc grpc.ClientConnInterface) outcome {
		var out outcome
		stream, err := gatewayv1.NewGatewayServiceClient(cc).ChatCompletion(ctx, &gatewayv1.ChatCompletionRequest{
			Model:    model,
			Messages: []*gatewayv1.Message{{Name: "prompt " + strings.Repeat("x", 300)}},
		}, opts...)
		if err != nil {
			out.setError(err)
			return out
		}
		header, err := stream.Header()
		if err != nil {
			out.setError(err)
			return out
		}
		out.Header, out.HeaderNil = normaliseMD(header), header == nil
		for {
			msg, err := stream.Recv()
			if err != nil {
				out.setError(err)
				break
			}
			out.Messages = append(out.Messages, prototext.MarshalOptions{}.Format(msg))
		}
		out.Trailer = normaliseMD(stream.Trailer())
		return out
	}
}

func modelsCall(mdPairs ...string) func(context.Context, grpc.ClientConnInterface) outcome {
	return func(ctx context.Context, cc grpc.ClientConnInterface) outcome {
		var out outcome
		var header, trailer metadata.MD
		if len(mdPairs) > 0 {
			ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(mdPairs...))
		}
		resp, err := gatewayv1.NewGatewayServiceClient(cc).ListModels(ctx, &gatewayv1.ListModelsRequest{}, grpc.Header(&header), grpc.Trailer(&trailer))
		out.setError(err)
		if err == nil {
			out.Messages = []string{prototext.MarshalOptions{}.Format(resp)}
		}
		out.Header, out.HeaderNil = normaliseMD(header), header == nil
		out.Trailer = normaliseMD(trailer)
		return out
	}
}

// scriptedGateway is a Connect GatewayService whose behaviour is chosen by the
// request: the model name for chat, the x-scenario header for ListModels.
type scriptedGateway struct {
	gatewayconnect.UnimplementedGatewayServiceHandler
	cancelled chan struct{}
}

func (s *scriptedGateway) ChatCompletion(ctx context.Context, req *connect.Request[gatewayv1.ChatCompletionRequest], stream *connect.ServerStream[gatewayv1.ChatCompletionResponse]) error {
	send := func(i int) error {
		return stream.Send(&gatewayv1.ChatCompletionResponse{Id: fmt.Sprintf("chunk-%d", i), Model: req.Msg.GetModel()})
	}
	stream.ResponseHeader().Set("X-Chat-Header", "from-handler")
	stream.ResponseHeader().Add("X-Multi", "one")
	stream.ResponseHeader().Add("X-Multi", "two")
	stream.ResponseHeader().Set("X-Binary-Bin", connect.EncodeBinaryHeader([]byte{0, 1, 2, 250}))
	stream.ResponseTrailer().Set("X-Chat-Trailer", "the-end")
	switch req.Msg.GetModel() {
	case "three-messages":
		for i := 0; i < 3; i++ {
			if err := send(i); err != nil {
				return err
			}
		}
		return nil
	case "no-messages":
		return nil
	case "refused-before-any-message":
		err := connect.NewError(connect.CodeUnauthenticated, errors.New("no api key"))
		if detail, derr := connect.NewErrorDetail(&errdetails.ErrorInfo{Reason: "API_KEY_MISSING", Domain: "everstack.test"}); derr == nil {
			err.AddDetail(detail)
		}
		err.Meta().Set("X-Error-Meta", "why")
		return err
	case "fails-after-two-messages":
		_ = send(0)
		_ = send(1)
		return connect.NewError(connect.CodeResourceExhausted, errors.New("rate limit 100% reached; retry in 5s (naïve clients beware)"))
	case "message-over-the-limit":
		return stream.Send(&gatewayv1.ChatCompletionResponse{Id: strings.Repeat("y", 5<<20)})
	case "panics-before-any-message":
		panic("scripted panic before any message")
	case "panics-after-one-message":
		_ = send(0)
		panic("scripted panic after one message")
	case "waits-for-the-caller-to-leave":
		_ = send(0)
		<-ctx.Done()
		select {
		case s.cancelled <- struct{}{}:
		default:
		}
		return ctx.Err()
	}
	return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unknown scenario %q", req.Msg.GetModel()))
}

func (s *scriptedGateway) ListModels(_ context.Context, req *connect.Request[gatewayv1.ListModelsRequest]) (*connect.Response[gatewayv1.ListModelsResponse], error) {
	switch req.Header().Get("X-Scenario") {
	case "not-found":
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no such gateway"))
	case "panic":
		panic("scripted unary panic")
	}
	resp := connect.NewResponse(&gatewayv1.ListModelsResponse{Providers: []*gatewayv1.ProviderModels{{Provider: "p1"}, {Provider: "p2"}}})
	resp.Header().Set("X-Models-Header", "h")
	resp.Trailer().Set("X-Models-Trailer", "t")
	return resp, nil
}

func scriptedTree(stub *scriptedGateway) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(gatewayconnect.NewGatewayServiceHandler(stub))
	return mux
}

func TestDirectTransportMatchesHTTP2ForConnectHandlers(t *testing.T) {
	stub := &scriptedGateway{cancelled: make(chan struct{}, 4)}
	pair := newTransportPair(t, scriptedTree(stub))

	t.Run("a stream of three messages", func(t *testing.T) {
		got := pair.compare(t, chatCall("three-messages"))
		if len(got.Messages) != 3 || got.Code != codes.OK {
			t.Fatalf("want 3 messages and OK, got %+v", got)
		}
		if got.Header["x-chat-header"][0] != "from-handler" || got.Trailer["x-chat-trailer"][0] != "the-end" {
			t.Fatalf("handler metadata missing: header %v trailer %v", got.Header, got.Trailer)
		}
		if got.Header["x-binary-bin"][0] != string([]byte{0, 1, 2, 250}) {
			t.Fatalf("binary header not decoded: %q", got.Header["x-binary-bin"])
		}
	})
	t.Run("a stream with no messages", func(t *testing.T) {
		if got := pair.compare(t, chatCall("no-messages")); len(got.Messages) != 0 || got.Code != codes.OK {
			t.Fatalf("want an empty OK stream, got %+v", got)
		}
	})
	t.Run("an error before any message, with details and metadata", func(t *testing.T) {
		got := pair.compare(t, chatCall("refused-before-any-message"))
		if got.Code != codes.Unauthenticated || got.Message != "no api key" || len(got.Details) != 1 {
			t.Fatalf("want Unauthenticated with one detail, got %+v", got)
		}
	})
	t.Run("an error after two messages", func(t *testing.T) {
		got := pair.compare(t, chatCall("fails-after-two-messages"))
		if len(got.Messages) != 2 || got.Code != codes.ResourceExhausted || !strings.Contains(got.Message, "100% reached") || !strings.Contains(got.Message, "naïve") {
			t.Fatalf("want two messages then ResourceExhausted with the message intact, got %+v", got)
		}
	})
	t.Run("a message over the client's 4 MB limit", func(t *testing.T) {
		if got := pair.compare(t, chatCall("message-over-the-limit")); got.Code != codes.ResourceExhausted {
			t.Fatalf("want ResourceExhausted, got %+v", got)
		}
	})
	t.Run("a handler that panics before any message", func(t *testing.T) {
		if got := pair.compare(t, chatCall("panics-before-any-message")); got.Code != codes.Internal {
			t.Fatalf("want Internal, got %+v", got)
		}
	})
	t.Run("a handler that panics after one message", func(t *testing.T) {
		if got := pair.compare(t, chatCall("panics-after-one-message")); got.Code != codes.Internal || len(got.Messages) != 1 {
			t.Fatalf("want one message then Internal, got %+v", got)
		}
	})
	t.Run("a unary call", func(t *testing.T) {
		got := pair.compare(t, modelsCall("authorization", "Bearer k", "x-evs-api-key", "k", "x-multi", "a", "x-multi", "b", "x-payload-bin", string([]byte{9, 8, 255})))
		if got.Code != codes.OK || len(got.Messages) != 1 || got.Header["x-models-header"] == nil || got.Trailer["x-models-trailer"] == nil {
			t.Fatalf("want one response with header and trailer metadata, got %+v", got)
		}
	})
	t.Run("a unary error", func(t *testing.T) {
		if got := pair.compare(t, modelsCall("x-scenario", "not-found")); got.Code != codes.NotFound {
			t.Fatalf("want NotFound, got %+v", got)
		}
	})
	t.Run("a unary handler that panics", func(t *testing.T) {
		if got := pair.compare(t, modelsCall("x-scenario", "panic")); got.Code != codes.Internal {
			t.Fatalf("want Internal, got %+v", got)
		}
	})
	t.Run("metadata names the protocol reserves are not sent", func(t *testing.T) {
		pair.compare(t, modelsCall("content-type", "text/evil", "user-agent", "spoof", "te", "gzip", "grpc-timeout", "1n", "x-kept", "yes"))
	})
	t.Run("metadata gRPC refuses fails the call before it is sent", func(t *testing.T) {
		if got := pair.compare(t, modelsCall("x-bad", "line\nbreak")); got.Code != codes.Internal {
			t.Fatalf("want Internal for a non-printable value, got %+v", got)
		}
		if got := pair.compare(t, modelsCall("x bad key", "v")); got.Code != codes.Internal {
			t.Fatalf("want Internal for an illegal key, got %+v", got)
		}
	})
	t.Run("a deadline reaches the handler as grpc-timeout", func(t *testing.T) {
		pair.compare(t, func(ctx context.Context, cc grpc.ClientConnInterface) outcome {
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			return modelsCall()(ctx, cc)
		})
	})
	t.Run("a deadline that has already passed", func(t *testing.T) {
		got := pair.compare(t, func(ctx context.Context, cc grpc.ClientConnInterface) outcome {
			ctx, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
			defer cancel()
			return modelsCall()(ctx, cc)
		})
		if got.Code != codes.DeadlineExceeded {
			t.Fatalf("want DeadlineExceeded, got %+v", got)
		}
	})
	t.Run("a caller that cancels ends the handler's context", func(t *testing.T) {
		got := pair.compare(t, func(ctx context.Context, cc grpc.ClientConnInterface) outcome {
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			var out outcome
			stream, err := gatewayv1.NewGatewayServiceClient(cc).ChatCompletion(ctx, &gatewayv1.ChatCompletionRequest{Model: "waits-for-the-caller-to-leave"})
			if err != nil {
				out.setError(err)
				return out
			}
			if msg, err := stream.Recv(); err == nil {
				out.Messages = append(out.Messages, prototext.MarshalOptions{}.Format(msg))
			}
			cancel()
			_, err = stream.Recv()
			out.setError(err)
			select {
			case <-stub.cancelled:
			case <-time.After(5 * time.Second):
				t.Errorf("the handler's context was not cancelled when the caller left")
			}
			return out
		})
		if got.Code != codes.Canceled || len(got.Messages) != 1 {
			t.Fatalf("want one message then Canceled, got %+v", got)
		}
	})
}

// A middleware in front of the Connect handlers can answer in plain HTTP. A
// gRPC client turns that into a status, and the REST gateway turns the status
// into the response the REST caller sees, so the mapping has to be the same.
func TestDirectTransportMatchesHTTP2ForPlainHTTPResponses(t *testing.T) {
	respond := func(code int, contentType, body string, extra ...string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			if contentType != "" {
				w.Header().Set("Content-Type", contentType)
			}
			for i := 0; i+1 < len(extra); i += 2 {
				w.Header().Set(extra[i], extra[i+1])
			}
			w.WriteHeader(code)
			_, _ = io.WriteString(w, body)
		}
	}
	cases := []struct {
		name    string
		handler http.HandlerFunc
		want    codes.Code
	}{
		{"401 with a JSON body", respond(401, "application/json", `{"error":"invalid api key"}`), codes.Unauthenticated},
		{"403 with a body and no content type", respond(403, "", "forbidden for this tenant"), codes.PermissionDenied},
		{"403 with an HTML body and no content type", respond(403, "", "<html><body>no</body></html>"), codes.PermissionDenied},
		{"404 with nothing", respond(404, "", ""), codes.Unimplemented},
		{"429 with retry-after", respond(429, "text/plain; charset=utf-8", "slow down", "Retry-After", "7"), codes.Unavailable},
		{"400", respond(400, "text/plain", "bad"), codes.Internal},
		{"500", respond(500, "text/plain", "boom"), codes.Unknown},
		{"502", respond(502, "text/html", "<h1>bad gateway</h1>"), codes.Unavailable},
		{"503", respond(503, "application/json", `{}`), codes.Unavailable},
		{"504", respond(504, "", ""), codes.Unavailable},
		{"302 redirect", respond(302, "", "", "Location", "https://login.invalid/"), codes.Unknown},
		{"200 that is not gRPC", respond(200, "text/html", "<html>a login page</html>"), codes.Unknown},
		{"200 with nothing at all", func(http.ResponseWriter, *http.Request) {}, codes.Unknown},
		{"200 gRPC that ends without trailers", respond(200, "application/grpc", ""), codes.Unknown},
		{"gRPC status sent as headers only", respond(200, "application/grpc", "", "Grpc-Status", "7", "Grpc-Message", "denied%20by%20policy", "X-Why", "policy"), codes.PermissionDenied},
		{"gRPC headers flushed, then nothing", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/grpc")
			w.(http.Flusher).Flush()
		}, codes.Internal},
		{"a write before any header", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "plain text with no status or content type")
		}, codes.Unknown},
		{"headers changed after WriteHeader are not sent", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			w.Header().Set("Content-Type", "application/grpc")
			w.Header().Set("X-Late", "ignored")
		}, codes.Unauthenticated},
		{"panic before writing", func(http.ResponseWriter, *http.Request) { panic("middleware panic") }, codes.Internal},
		{"abort handler", func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }, codes.Internal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pair := newTransportPair(t, tc.handler)
			for _, call := range []func(context.Context, grpc.ClientConnInterface) outcome{chatCall("any"), modelsCall()} {
				if got := pair.compare(t, call); got.Code != tc.want {
					t.Errorf("code = %v (%q), want %v", got.Code, got.Message, tc.want)
				}
			}
		})
	}
}

// grpc-go's own HTTP handler announces its trailers in a Trailer header
// instead of using the trailer prefix. Both forms have to work.
func TestDirectTransportMatchesHTTP2ForGRPCGoHandlers(t *testing.T) {
	grpcSrv := grpc.NewServer()
	healthSrv := health.NewServer()
	healthpb.RegisterHealthServer(grpcSrv, healthSrv)
	t.Cleanup(grpcSrv.Stop)
	pair := newTransportPair(t, http.HandlerFunc(grpcSrv.ServeHTTP))

	t.Run("unary", func(t *testing.T) {
		got := pair.compare(t, func(ctx context.Context, cc grpc.ClientConnInterface) outcome {
			var out outcome
			resp, err := healthpb.NewHealthClient(cc).Check(ctx, &healthpb.HealthCheckRequest{})
			out.setError(err)
			if err == nil {
				out.Messages = []string{prototext.MarshalOptions{}.Format(resp)}
			}
			return out
		})
		if got.Code != codes.OK || len(got.Messages) != 1 {
			t.Fatalf("want one SERVING response, got %+v", got)
		}
	})
	t.Run("unary error", func(t *testing.T) {
		got := pair.compare(t, func(ctx context.Context, cc grpc.ClientConnInterface) outcome {
			var out outcome
			_, err := healthpb.NewHealthClient(cc).Check(ctx, &healthpb.HealthCheckRequest{Service: "no.such.Service"})
			out.setError(err)
			return out
		})
		if got.Code != codes.NotFound {
			t.Fatalf("want NotFound, got %+v", got)
		}
	})
	t.Run("stream read, then cancelled", func(t *testing.T) {
		got := pair.compare(t, func(ctx context.Context, cc grpc.ClientConnInterface) outcome {
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			var out outcome
			stream, err := healthpb.NewHealthClient(cc).Watch(ctx, &healthpb.HealthCheckRequest{})
			if err != nil {
				out.setError(err)
				return out
			}
			if msg, err := stream.Recv(); err == nil {
				out.Messages = append(out.Messages, prototext.MarshalOptions{}.Format(msg))
			}
			cancel()
			_, err = stream.Recv()
			out.setError(err)
			return out
		})
		if got.Code != codes.Canceled || len(got.Messages) != 1 {
			t.Fatalf("want one message then Canceled, got %+v", got)
		}
	})
}

// The REST caller's view: the same REST request through the real grpc-gateway
// handlers must produce the same HTTP response over either transport.
func TestDirectTransportGivesRESTCallersTheSameResponse(t *testing.T) {
	stub := &scriptedGateway{cancelled: make(chan struct{}, 4)}
	pair := newTransportPair(t, scriptedTree(stub))

	var muxes [2]*runtime.ServeMux
	for i, cc := range pair.conns {
		muxes[i] = runtime.NewServeMux()
		if err := gatewayv1.RegisterGatewayServiceHandlerClient(context.Background(), muxes[i], gatewayv1.NewGatewayServiceClient(cc)); err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	type restResponse struct {
		Status int
		Header map[string][]string
		Body   string
	}
	do := func(mux *runtime.ServeMux, method, path, body string) restResponse {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer rest-key")
		req.Header.Set("Grpc-Metadata-X-Scenario", strings.TrimPrefix(path, "/v1/gateway/models?s="))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		header := map[string][]string{}
		for key, values := range rec.Header() {
			if key == "Grpc-Metadata-Date" {
				values = []string{"<date>"}
			}
			sorted := append([]string(nil), values...)
			sort.Strings(sorted)
			header[key] = sorted
		}
		return restResponse{rec.Code, header, rec.Body.String()}
	}
	requests := []struct{ name, method, path, body string }{
		{"chat, three messages", "POST", "/v1/chat/completions", `{"model":"three-messages","messages":[{"name":"hi"}]}`},
		{"chat, no messages", "POST", "/v1/chat/completions", `{"model":"no-messages"}`},
		{"chat, refused", "POST", "/v1/chat/completions", `{"model":"refused-before-any-message"}`},
		{"chat, fails mid-stream", "POST", "/v1/chat/completions", `{"model":"fails-after-two-messages"}`},
		{"chat, handler panics", "POST", "/v1/chat/completions", `{"model":"panics-before-any-message"}`},
		{"chat, malformed JSON", "POST", "/v1/chat/completions", `{"model":`},
		{"models", "GET", "/v1/gateway/models", ``},
		{"models, not found", "GET", "/v1/gateway/models?s=not-found", ``},
		{"models, handler panics", "GET", "/v1/gateway/models?s=panic", ``},
	}
	for _, r := range requests {
		t.Run(r.name, func(t *testing.T) {
			viaHTTP2 := do(muxes[0], r.method, r.path, r.body)
			viaDirect := do(muxes[1], r.method, r.path, r.body)
			if !reflect.DeepEqual(viaHTTP2, viaDirect) {
				t.Fatalf("REST responses differ\nhttp2:  %+v\ndirect: %+v", viaHTTP2, viaDirect)
			}
			for i := range pair.stats {
				pair.stats[i].take(t)
			}
			served := pair.requests.take()
			if len(served) == 2 && !reflect.DeepEqual(served[0], served[1]) {
				t.Fatalf("the handler was served different requests\nhttp2:  %+v\ndirect: %+v", served[0], served[1])
			}
		})
	}
}

// Calls made before Serve wait for it, as a dial waits on a listener.
func TestDirectCallWaitsForServe(t *testing.T) {
	loopback := NewLoopback()
	cc := loopback.ClientConn(equivalenceEndpoint)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := gatewayv1.NewGatewayServiceClient(cc).ListModels(ctx, &gatewayv1.ListModelsRequest{})
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("a call nobody serves returned %v, want DeadlineExceeded", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := gatewayv1.NewGatewayServiceClient(cc).ListModels(context.Background(), &gatewayv1.ListModelsRequest{})
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	stop := loopback.Serve(scriptedTree(&scriptedGateway{}))
	defer stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("call made before Serve failed once served: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("call made before Serve never completed")
	}
}

// Many calls at once, parked and active: every one is answered, and the race
// detector sees the handler and caller sides working concurrently.
func TestDirectTransportUnderConcurrency(t *testing.T) {
	loopback := NewLoopback()
	stop := loopback.Serve(scriptedTree(&scriptedGateway{cancelled: make(chan struct{}, 4)}))
	defer stop()
	client := gatewayv1.NewGatewayServiceClient(loopback.ClientConn(equivalenceEndpoint))

	const calls = 500
	var wg sync.WaitGroup
	errs := make(chan error, calls)
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if i%2 == 0 {
				_, err := client.ListModels(ctx, &gatewayv1.ListModelsRequest{})
				errs <- err
				return
			}
			stream, err := client.ChatCompletion(ctx, &gatewayv1.ChatCompletionRequest{Model: "three-messages"})
			if err != nil {
				errs <- err
				return
			}
			n := 0
			for {
				if _, err := stream.Recv(); err == io.EOF {
					break
				} else if err != nil {
					errs <- err
					return
				}
				n++
			}
			if n != 3 {
				errs <- fmt.Errorf("stream %d got %d messages, want 3", i, n)
				return
			}
			errs <- nil
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent call failed: %v", err)
		}
	}
}

// Metadata a REST caller can attach (grpc-gateway forwards Grpc-Metadata-*
// headers) that an HTTP/2 server treats specially: refused, normalised, or
// held to a declared length. The handlers must see exactly what the HTTP/2
// path let through, and the caller must get the same outcome.
func TestDirectTransportMatchesHTTP2ForSpecialRequestHeaders(t *testing.T) {
	stub := &scriptedGateway{cancelled: make(chan struct{}, 4)}
	pair := newTransportPair(t, scriptedTree(stub))
	cases := []struct {
		name  string
		pairs []string
	}{
		{"connection", []string{"connection", "close"}},
		{"keep-alive", []string{"keep-alive", "timeout=5"}},
		{"proxy-connection", []string{"proxy-connection", "keep-alive"}},
		{"transfer-encoding", []string{"transfer-encoding", "chunked"}},
		{"upgrade", []string{"upgrade", "websocket"}},
		{"expect 100-continue", []string{"expect", "100-continue"}},
		{"another expectation", []string{"expect", "something-else"}},
		{"two cookies", []string{"cookie", "a=1", "cookie", "b=2"}},
		{"announced trailers", []string{"trailer", "X-Checksum, Content-Length, x-other"}},
		{"a host header", []string{"host", "other-tenant.invalid"}},
		{"content-length shorter than the request", []string{"content-length", "1"}},
		{"content-length longer than the request", []string{"content-length", "100000"}},
		{"content-length that is not a number", []string{"content-length", "abc"}},
		{"content-length equal to the request", []string{"content-length", "5"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pair.compare(t, modelsCall(tc.pairs...))
		})
	}
	t.Run("grpc-accept-encoding", func(t *testing.T) {
		// The one deliberate difference. Connect compresses a response for a
		// client that accepts gzip; over HTTP/2 a caller that adds the
		// header itself gets a compressed message grpc-go can only read if
		// a gzip codec is linked in (this test binary has none, so the call
		// fails). The direct transport never offers compression and drops
		// the header, so the call succeeds either way.
		loopback := NewLoopback()
		stop := loopback.Serve(scriptedTree(stub))
		defer stop()
		ctx := metadata.AppendToOutgoingContext(context.Background(), "grpc-accept-encoding", "gzip")
		got := chatCall("three-messages")(ctx, loopback.ClientConn(equivalenceEndpoint))
		if got.Code != codes.OK || len(got.Messages) != 3 {
			t.Fatalf("want three messages and OK, got %+v", got)
		}
		if got := modelsCall("grpc-accept-encoding", "gzip")(context.Background(), loopback.ClientConn(equivalenceEndpoint)); got.Code != codes.OK {
			t.Fatalf("unary: want OK, got %+v", got)
		}
	})
}

// grpc-go gives an endpoint that is only a port the authority localhost:port.
func TestDirectTransportUsesGRPCsAuthority(t *testing.T) {
	if got := grpcAuthority(":8089"); got != "localhost:8089" {
		t.Fatalf("grpcAuthority(:8089) = %q", got)
	}
	if got := grpcAuthority("gate\"way:1"); got != "gate%22way:1" {
		t.Fatalf("grpcAuthority escapes to %q", got)
	}
	stub := &scriptedGateway{}
	requests := &recordRequests{next: scriptedTree(stub)}
	h2 := NewLoopback()
	defer h2.Serve(requests)()
	conn, err := grpc.NewClient(h2.Target(":8089"), h2.DialOptions()...)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer conn.Close()
	direct := NewLoopback()
	defer direct.Serve(requests)()
	var hosts []string
	for _, cc := range []grpc.ClientConnInterface{conn, direct.ClientConn(":8089")} {
		modelsCall()(context.Background(), cc)
		for _, req := range requests.take() {
			hosts = append(hosts, req.Host)
		}
	}
	if len(hosts) != 2 || hosts[0] != hosts[1] || hosts[0] != "localhost:8089" {
		t.Fatalf("hosts = %v, want localhost:8089 on both transports", hosts)
	}
}

// A unary call answered with an OK status and no message is a cardinality
// violation; a stream answered the same way is an empty stream.
func TestDirectTransportMatchesHTTP2ForAnOKWithNoMessage(t *testing.T) {
	pair := newTransportPair(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Grpc-Status", "0")
	}))
	if got := pair.compare(t, modelsCall()); got.Code != codes.Internal || !strings.Contains(got.Message, "cardinality") {
		t.Fatalf("unary: want a cardinality violation, got %+v", got)
	}
	if got := pair.compare(t, chatCall("any")); got.Code != codes.OK {
		t.Fatalf("stream: want OK, got %+v", got)
	}
}

// A caller must not wait on a handler that ignores its context: over HTTP/2 a
// refused response or an ended context is reported at once.
func TestDirectCallerDoesNotWaitOnASlowHandler(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Scenario") == "refuse-then-linger" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.(http.Flusher).Flush()
		}
		<-release // ignores the request's context
	})
	pair := newTransportPair(t, slow)
	for i, cc := range pair.conns {
		name := pair.names[i]
		t.Run(name+"/unary with a deadline", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			start := time.Now()
			_, err := gatewayv1.NewGatewayServiceClient(cc).ListModels(ctx, &gatewayv1.ListModelsRequest{})
			if status.Code(err) != codes.DeadlineExceeded || time.Since(start) > 2*time.Second {
				t.Fatalf("got %v after %v, want DeadlineExceeded at the deadline", err, time.Since(start))
			}
		})
		t.Run(name+"/stream whose headers are refused", func(t *testing.T) {
			ctx := metadata.AppendToOutgoingContext(context.Background(), "x-scenario", "refuse-then-linger")
			start := time.Now()
			stream, err := gatewayv1.NewGatewayServiceClient(cc).ChatCompletion(ctx, &gatewayv1.ChatCompletionRequest{})
			if err == nil {
				_, err = stream.Recv()
			}
			if status.Code(err) != codes.Unauthenticated || time.Since(start) > 2*time.Second {
				t.Fatalf("got %v after %v, want Unauthenticated at once", err, time.Since(start))
			}
		})
		pair.stats[i].take(t)
		pair.requests.take()
	}
}

// A client-streaming call the caller abandons cuts off the handler's request
// body with the error an HTTP/2 server gives it, after what was already sent.
func TestDirectTransportAbandonedRequestBody(t *testing.T) {
	type readResult struct {
		n   int
		err string
	}
	results := make(chan readResult, 2)
	reader := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/grpc")
		w.(http.Flusher).Flush()
		b, err := io.ReadAll(r.Body)
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		results <- readResult{len(b), msg}
	})
	// No request recorder in front: it would read the body first.
	h2 := NewLoopback()
	defer h2.Serve(reader)()
	conn, err := grpc.NewClient(h2.Target(equivalenceEndpoint), h2.DialOptions()...)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer conn.Close()
	direct := NewLoopback()
	defer direct.Serve(reader)()
	names := []string{"http2", "direct"}
	var got [2]readResult
	for i, cc := range []grpc.ClientConnInterface{conn, direct.ClientConn(equivalenceEndpoint)} {
		ctx, cancel := context.WithCancel(context.Background())
		stream, err := cc.NewStream(ctx, &grpc.StreamDesc{ClientStreams: true, ServerStreams: true}, "/test.Upload/Stream")
		if err != nil {
			t.Fatalf("%s: new stream: %v", names[i], err)
		}
		if err := stream.SendMsg(&healthpb.HealthCheckRequest{Service: "part one"}); err != nil {
			t.Fatalf("%s: send: %v", names[i], err)
		}
		time.Sleep(50 * time.Millisecond)
		cancel()
		select {
		case got[i] = <-results:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: the handler's body read never ended after the caller left", names[i])
		}
		_ = stream.RecvMsg(&healthpb.HealthCheckResponse{})
	}
	if got[0] != got[1] {
		t.Fatalf("handler's read differs\nhttp2:  %+v\ndirect: %+v", got[0], got[1])
	}
	if got[1].err == "" || got[1].n == 0 {
		t.Fatalf("want the sent bytes then an error, got %+v", got[1])
	}
}
