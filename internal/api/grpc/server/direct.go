package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"net/url"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"golang.org/x/net/http/httpguts"
	"golang.org/x/net/http2"
	spb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/protoadapt"
)

// The direct transport answers a REST gateway call by running this process's
// handler tree on the request a gRPC client would have sent, without an
// HTTP/2 connection in between.
//
// The in-memory Loopback already removed the network from that call, but both
// ends still spoke HTTP/2 to each other: grpc-go framed, HPACK-encoded and
// flow-controlled the request, an HTTP/2 server decoded it again, and each
// call cost several goroutines. A CPU profile at 1000 chat requests per second
// put about a fifth of the gateway's CPU in those two libraries talking to
// each other.
//
// What the handlers receive is unchanged: the same method, path, Host, peer
// address, protocol version and headers, a gRPC-framed body, and a context
// that carries nothing from the caller. What the caller gets back is what
// grpc-go would have produced from the same response: the same header and
// trailer metadata, the same status for a gRPC error, the same status for a
// plain HTTP rejection from a middleware, and the same error when a handler
// panics. direct_equivalence_test.go runs both transports against the same
// handlers and compares the two sides exactly.
//
// That includes what the HTTP/2 server does to a request before any handler
// sees it (a caller's metadata becomes request headers): connection-specific
// headers are answered with a 400, Expect, Cookie and Trailer are normalised,
// and a declared Content-Length is enforced. The one deliberate difference is
// compression: it is never offered, and a grpc-accept-encoding in the
// caller's metadata is dropped, so no response arrives compressed.

const (
	// directMaxRecvBytes is grpc-go's default limit on a received message.
	// The REST gateway's client never raised it, so it applies here too.
	directMaxRecvBytes = 4 * 1024 * 1024

	// directStreamBuffer is how many response messages may wait for the
	// caller before the handler's next write blocks.
	directStreamBuffer = 16

	// directSniffLen is how much of a body net/http looks at to guess a
	// missing Content-Type.
	directSniffLen = 512

	grpcFramePrefixLen = 5
)

// errDirectCallEnded is what a handler's write returns once the caller is gone.
var errDirectCallEnded = errors.New("loopback: the caller ended the call")

// DirectOption configures a direct client connection.
type DirectOption func(*directConn)

// WithDirectStatsHandler reports each call to h the way grpc-go would report
// it to a stats handler installed with grpc.WithStatsHandler.
func WithDirectStatsHandler(h stats.Handler) DirectOption {
	return func(c *directConn) { c.stats = h }
}

// WithDirectUnaryInterceptor runs unary calls through interceptor, as
// grpc.WithChainUnaryInterceptor would. The interceptor is handed a nil
// *grpc.ClientConn: there is no connection.
func WithDirectUnaryInterceptor(interceptor grpc.UnaryClientInterceptor) DirectOption {
	return func(c *directConn) { c.unary = interceptor }
}

// ClientConn returns a gRPC client connection whose calls run the handler
// passed to Serve directly. endpoint becomes the Host the handlers see, as it
// is the :authority of a call made through DialOptions.
func (l *Loopback) ClientConn(endpoint string, opts ...DirectOption) grpc.ClientConnInterface {
	c := &directConn{loopback: l, authority: grpcAuthority(endpoint)}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

type directConn struct {
	loopback  *Loopback
	authority string
	stats     stats.Handler
	unary     grpc.UnaryClientInterceptor
}

var directUnaryDesc = &grpc.StreamDesc{}

// Invoke makes a unary call.
func (c *directConn) Invoke(ctx context.Context, method string, args, reply any, opts ...grpc.CallOption) error {
	if c.unary != nil {
		return c.unary(ctx, method, args, reply, nil, c.invoke, opts...)
	}
	return c.invoke(ctx, method, args, reply, nil, opts...)
}

func (c *directConn) invoke(ctx context.Context, method string, args, reply any, _ *grpc.ClientConn, opts ...grpc.CallOption) error {
	call, err := c.newCall(ctx, directUnaryDesc, method, opts)
	if err != nil {
		return err
	}
	if err := call.SendMsg(args); err != nil {
		return err
	}
	return call.RecvMsg(reply)
}

// NewStream starts a streaming call.
//
// Every call's handler runs on its own goroutine, started once the request is
// complete (or at once for a call whose request is itself a stream), so the
// caller can return as soon as its context ends whatever the handler does.
func (c *directConn) NewStream(ctx context.Context, desc *grpc.StreamDesc, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	call, err := c.newCall(ctx, desc, method, opts)
	if err != nil {
		return nil, err
	}
	if desc.ClientStreams {
		call.start()
	}
	return call, nil
}

// directCall is one call: the grpc.ClientStream the caller holds.
type directCall struct {
	conn   *directConn
	ctx    context.Context // the caller's context, after the stats handler tagged it
	desc   *grpc.StreamDesc
	method string
	opts   []grpc.CallOption
	begin  time.Time
	header http.Header

	handler http.Handler
	res     *directResponse
	cancel  context.CancelFunc // ends the handler's context

	mu        sync.Mutex
	stopWatch func() bool        // stops watching the caller's context
	request   []byte             // framed request of a call with a single request message
	body      *directRequestPipe // request of a client-streaming call
	sendDone  bool
	started   bool
	recvDone  bool // a message was already returned on a non-server-streaming call
	received  bool // any message was returned
	finished  bool // finish has run; no request pipe may be created after it
	finalOnce sync.Once
}

func (c *directConn) newCall(ctx context.Context, desc *grpc.StreamDesc, method string, opts []grpc.CallOption) (*directCall, error) {
	// As in grpc-go, a call with metadata it refuses, or whose context has
	// already ended, fails before anything (a stats handler included) hears
	// of it.
	if err := checkOutgoing(ctx); err != nil {
		return nil, err
	}
	handler, err := c.loopback.servedHandler(ctx)
	if err != nil {
		return nil, err
	}

	begin := time.Now()
	if c.stats != nil {
		ctx = c.stats.TagRPC(ctx, &stats.RPCTagInfo{FullMethodName: method, FailFast: true})
		c.stats.HandleRPC(ctx, &stats.Begin{
			Client:         true,
			BeginTime:      begin,
			FailFast:       true,
			IsClientStream: desc.ClientStreams,
			IsServerStream: desc.ServerStreams,
		})
	}
	call := &directCall{conn: c, ctx: ctx, desc: desc, method: method, opts: opts, begin: begin, handler: handler}
	// Built after the stats handler, which may have added metadata (a trace
	// parent) to the context it returned.
	header, err := requestHeader(ctx)
	if err != nil {
		return nil, call.finish(err)
	}
	call.header = header
	if name, ok := connectionSpecificHeader(header); ok {
		// An HTTP/2 server answers such a request itself, before any handler.
		call.handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, fmt.Sprintf("request header %q is not valid in HTTP/2", name), http.StatusBadRequest)
		})
	}

	// The handler's context starts from nothing, as a server's does: no value
	// the caller's context holds (a tenant, a user, a trace) can reach the
	// handlers except through the request headers. It ends when the call does.
	reqCtx, cancel := context.WithCancel(directBaseContext)
	call.cancel = cancel
	call.res = newDirectResponse(reqCtx, cancel)
	// A caller that goes away ends the handler's context, as a reset stream
	// does on a real connection, and ends the call even if the caller never
	// reads it to the end.
	call.mu.Lock()
	if desc.ClientStreams {
		// Before the watcher below exists, so that a caller leaving at
		// any point afterwards finds the pipe and cuts it off.
		call.body = newDirectRequestPipe()
	}
	call.stopWatch = context.AfterFunc(ctx, func() {
		cancel()
		call.finish(statusFromContext(ctx))
	})
	call.mu.Unlock()
	// grpc-go writes the request headers when the stream is created, before
	// any message, and reports them then.
	call.outHeader()
	return call, nil
}

// directBaseContext is the context every handler call derives from. It carries
// the two values an HTTP/2 server puts in a request's context.
var directBaseContext = context.WithValue(
	context.WithValue(context.Background(), http.LocalAddrContextKey, loopbackAddr),
	http.ServerContextKey, new(http.Server),
)

// checkOutgoing refuses a call grpc-go would refuse before starting it: one
// whose context has ended, or whose metadata has a key or value gRPC does not
// allow. Keys are checked lower-cased: grpc-go refuses an upper-case key added
// with AppendToOutgoingContext, and here such a key is sent lower-cased
// instead.
func checkOutgoing(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return statusFromContext(ctx)
	}
	md, _ := metadata.FromOutgoingContext(ctx)
	for key, values := range md {
		if err := validateMetadataPair(key, values...); err != nil {
			return status.Error(codes.Internal, err.Error())
		}
	}
	return nil
}

// requestHeader builds the headers a grpc-go client would send for ctx.
func requestHeader(ctx context.Context) (http.Header, error) {
	md, _ := metadata.FromOutgoingContext(ctx)
	header := make(http.Header, len(md)+4)
	header["Content-Type"] = []string{"application/grpc"}
	header["User-Agent"] = []string{"grpc-go/" + grpc.Version}
	header["Te"] = []string{"trailers"}
	if deadline, ok := ctx.Deadline(); ok {
		timeout := time.Until(deadline)
		if timeout <= 0 {
			return nil, status.Error(codes.DeadlineExceeded, context.DeadlineExceeded.Error())
		}
		header["Grpc-Timeout"] = []string{encodeGRPCTimeout(timeout)}
	}
	for key, values := range md {
		// Compression is never offered (an in-process call has nothing to
		// save), so a caller cannot ask for it either: a handler answering a
		// grpc-accept-encoding with compressed messages would fail the call.
		if isReservedGRPCHeader(key) || key == "grpc-accept-encoding" {
			continue
		}
		canonical := http.CanonicalHeaderKey(key)
		for _, value := range values {
			if strings.HasSuffix(key, "-bin") {
				value = base64.RawStdEncoding.EncodeToString([]byte(value))
			}
			header[canonical] = append(header[canonical], value)
		}
	}
	return header, nil
}

// SendMsg frames one request message.
func (call *directCall) SendMsg(m any) error {
	data, err := marshalGRPCFrame(m)
	if err != nil {
		return call.finish(status.Errorf(codes.Internal, "grpc: error while marshaling: %v", err))
	}
	call.mu.Lock()
	if call.sendDone {
		call.mu.Unlock()
		return status.Error(codes.Internal, "SendMsg called after CloseSend")
	}
	body := call.body
	if body == nil {
		call.request = append(call.request, data...)
	}
	call.mu.Unlock()
	if body != nil {
		if err := body.write(call.ctx, call.res.ctx, data); err != nil {
			// As in grpc-go, a failed send means the call is over; the
			// status is what RecvMsg reports.
			return io.EOF
		}
	}
	if call.conn.stats != nil {
		call.conn.stats.HandleRPC(call.ctx, &stats.OutPayload{
			Client:           true,
			Payload:          m,
			Length:           len(data) - grpcFramePrefixLen,
			CompressedLength: len(data) - grpcFramePrefixLen,
			WireLength:       len(data),
			SentTime:         time.Now(),
		})
	}
	if !call.desc.ClientStreams {
		// The request is complete: this is the only message it has.
		call.closeSend()
	}
	return nil
}

// CloseSend ends the request.
func (call *directCall) CloseSend() error {
	call.closeSend()
	return nil
}

func (call *directCall) closeSend() {
	call.mu.Lock()
	if call.sendDone {
		call.mu.Unlock()
		return
	}
	call.sendDone = true
	body := call.body
	call.mu.Unlock()
	if body != nil {
		body.close()
		return
	}
	call.start()
}

// ensureStarted starts the handler for a caller that reads before it has
// finished sending. The request then has to be a pipe: the handler may answer
// before it reads, and what is sent later still has to reach it.
func (call *directCall) ensureStarted() {
	call.mu.Lock()
	if call.started || call.sendDone || call.body != nil || call.finished {
		call.mu.Unlock()
		return
	}
	call.body = newDirectRequestPipe()
	call.body.buf = call.request
	call.request = nil
	call.mu.Unlock()
	call.start()
}

// httpRequest builds what the handlers are served: the request an HTTP/2
// server makes of the headers and body a grpc-go client sends.
func (call *directCall) httpRequest() *http.Request {
	header := call.header
	// What the server does to every request's headers.
	if httpguts.HeaderValuesContainsToken(header["Expect"], "100-continue") {
		delete(header, "Expect")
	}
	if cookies := header["Cookie"]; len(cookies) > 1 {
		header["Cookie"] = []string{strings.Join(cookies, "; ")}
	}
	var trailer http.Header
	for _, announced := range header["Trailer"] {
		for _, name := range strings.Split(announced, ",") {
			name = textproto.CanonicalMIMEHeaderKey(textproto.TrimString(name))
			switch name {
			case "Transfer-Encoding", "Trailer", "Content-Length":
			default:
				if trailer == nil {
					trailer = make(http.Header)
				}
				trailer[name] = nil
			}
		}
	}
	delete(header, "Trailer")

	contentLength := int64(-1)
	if values, ok := header["Content-Length"]; ok {
		// Not sent by grpc-go itself, but a caller's metadata can carry it,
		// and the server then holds the request body to it.
		contentLength = 0
		if declared, err := strconv.ParseUint(values[0], 10, 63); err == nil {
			contentLength = int64(declared)
		}
	}

	var body io.ReadCloser
	sent := int64(len(call.request))
	switch {
	case call.body != nil:
		body = call.body
	case contentLength >= 0 && sent > contentLength:
		// The server stops at the first DATA frame that would go past the
		// declared length and resets the stream (see serve).
		accepted := min(sent, contentLength/http2MaxDataFrame*http2MaxDataFrame)
		body = io.NopCloser(io.MultiReader(
			bytes.NewReader(call.request[:accepted]),
			errorReader{fmt.Errorf("sender tried to send more than declared Content-Length of %d bytes", contentLength)},
		))
	case contentLength > sent:
		body = io.NopCloser(io.MultiReader(
			bytes.NewReader(call.request),
			errorReader{fmt.Errorf("request declared a Content-Length of %d but only wrote %d bytes", contentLength, sent)},
		))
	default:
		body = io.NopCloser(bytes.NewReader(call.request))
	}
	req := &http.Request{
		Method:        http.MethodPost,
		URL:           &url.URL{Path: call.method},
		Proto:         "HTTP/2.0",
		ProtoMajor:    2,
		ProtoMinor:    0,
		Header:        header,
		Body:          body,
		ContentLength: contentLength,
		Trailer:       trailer,
		Host:          call.conn.authority,
		RemoteAddr:    loopbackAddr.String(),
		RequestURI:    call.method,
	}
	return req.WithContext(call.res.ctx)
}

// http2MaxDataFrame is the DATA frame size grpc-go writes a request in.
const http2MaxDataFrame = 16384

// declaredLengthExceeded reports whether the request is longer than the
// Content-Length it declared, which an HTTP/2 server answers by resetting the
// stream.
func (call *directCall) declaredLengthExceeded() bool {
	values, ok := call.header["Content-Length"]
	if !ok || call.body != nil {
		return false
	}
	declared, err := strconv.ParseUint(values[0], 10, 63)
	if err != nil {
		declared = 0
	}
	return uint64(len(call.request)) > declared
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

// connectionSpecificHeader returns the first header HTTP/2 does not allow in
// a request, in the order an HTTP/2 server checks them.
func connectionSpecificHeader(h http.Header) (string, bool) {
	for _, name := range []string{"Connection", "Keep-Alive", "Proxy-Connection", "Transfer-Encoding", "Upgrade"} {
		if _, ok := h[name]; ok {
			return name, true
		}
	}
	if te := h["Te"]; len(te) > 1 || (len(te) == 1 && te[0] != "trailers" && te[0] != "") {
		return "Te", true
	}
	return "", false
}

// start runs the handler on its own goroutine.
func (call *directCall) start() {
	call.mu.Lock()
	if call.started {
		call.mu.Unlock()
		return
	}
	call.started = true
	call.mu.Unlock()

	go call.serve(call.httpRequest())
}

func (call *directCall) outHeader() {
	if call.conn.stats == nil {
		return
	}
	ctx := peer.NewContext(call.ctx, &peer.Peer{Addr: loopbackAddr})
	call.conn.stats.HandleRPC(ctx, &stats.OutHeader{
		Client:     true,
		FullMethod: call.method,
		RemoteAddr: loopbackAddr,
		LocalAddr:  loopbackAddr,
	})
}

// serve runs the handler and then closes the response, recovering a panic the
// way an HTTP server does so one bad request cannot take the process down.
func (call *directCall) serve(req *http.Request) {
	res := call.res
	defer func() {
		if recovered := recover(); recovered != nil {
			if recovered != http.ErrAbortHandler {
				logger.WithFields("method", call.method, "panic", fmt.Sprint(recovered), "stack", string(debug.Stack())).
					Error("loopback: panic serving an in-process call")
			}
			// An HTTP/2 server resets the stream; a gRPC client reports that as:
			res.abort(status.New(codes.Internal, "stream terminated by RST_STREAM with error code: INTERNAL_ERROR"))
		}
	}()
	if call.declaredLengthExceeded() {
		// The server has reset the stream: the client has its status, and
		// nothing the handler writes reaches it.
		res.reset(status.New(codes.Internal, "stream terminated by RST_STREAM with error code: PROTOCOL_ERROR"))
	}
	call.handler.ServeHTTP(res, req)
	res.end()
}

// Header returns the response's header metadata once the handler has sent it.
// Like grpc-go, it returns nil metadata and no error when the call failed
// before any headers: the error is what RecvMsg returns.
func (call *directCall) Header() (metadata.MD, error) {
	call.ensureStarted()
	select {
	case <-call.res.headerReady:
	case <-call.ctx.Done():
		call.finish(statusFromContext(call.ctx))
		return nil, nil
	}
	md, rejected := call.res.header()
	switch {
	case rejected != nil:
		call.finish(rejected.Err())
		return nil, nil
	case md == nil:
		// A headers-only response: its status is known once it has ended.
		select {
		case <-call.res.done:
		case <-call.ctx.Done():
			call.finish(statusFromContext(call.ctx))
			return nil, nil
		}
		call.finish(call.res.result())
		return nil, nil
	}
	return md.Copy(), nil
}

// Trailer returns the response's trailer metadata. Valid once RecvMsg has
// returned a non-nil error (io.EOF included).
func (call *directCall) Trailer() metadata.MD {
	return call.res.trailer().Copy()
}

// Context returns the call's context.
func (call *directCall) Context() context.Context { return call.ctx }

// RecvMsg reads the next response message into m. At the end of the stream it
// returns io.EOF for a successful call and the call's status otherwise.
func (call *directCall) RecvMsg(m any) error {
	res := call.res
	call.ensureStarted()
	if !call.desc.ServerStreams {
		call.mu.Lock()
		done := call.recvDone
		call.mu.Unlock()
		if done {
			return call.finish(io.EOF)
		}
	}
	select {
	case msg, ok := <-res.msgs:
		if !ok {
			err := res.result()
			call.mu.Lock()
			received := call.received
			call.mu.Unlock()
			if err == io.EOF && !call.desc.ServerStreams && !received {
				err = status.Error(codes.Internal, "cardinality violation: received no response message from non-server-streaming RPC")
			}
			return call.finish(err)
		}
		if msg.fail != nil {
			return call.finish(msg.fail.Err())
		}
		if err := call.decode(msg.data, m); err != nil {
			return call.finish(err)
		}
		call.mu.Lock()
		call.received = true
		call.mu.Unlock()
		if call.desc.ServerStreams {
			return nil
		}
		// A call with a single response is only successful if the stream
		// then ends cleanly, so wait for its status before returning.
		call.mu.Lock()
		call.recvDone = true
		call.mu.Unlock()
		select {
		case extra, ok := <-res.msgs:
			if ok && extra.fail == nil {
				return call.finish(status.Error(codes.Internal, "grpc: client streaming protocol violation: get <nil>, want <EOF>"))
			}
			if ok {
				return call.finish(extra.fail.Err())
			}
			if err := res.result(); err != io.EOF {
				return call.finish(err)
			}
			call.finish(io.EOF)
			return nil
		case <-call.ctx.Done():
			return call.finish(statusFromContext(call.ctx))
		}
	case <-res.rejected:
		// The client refused the response headers and reset the stream: the
		// status is known now, whatever the handler goes on doing.
		_, rejected := res.header()
		return call.finish(rejected.Err())
	case <-call.ctx.Done():
		return call.finish(statusFromContext(call.ctx))
	}
}

func (call *directCall) decode(data []byte, m any) error {
	if err := unmarshalGRPC(data, m); err != nil {
		return status.Errorf(codes.Internal, "grpc: failed to unmarshal the received message: %v", err)
	}
	if call.conn.stats != nil {
		call.conn.stats.HandleRPC(call.ctx, &stats.InPayload{
			Client:           true,
			Payload:          m,
			Length:           len(data),
			CompressedLength: len(data),
			WireLength:       len(data) + grpcFramePrefixLen,
			RecvTime:         time.Now(),
		})
	}
	return nil
}

// finish ends the call once: it releases the handler's context, applies the
// Header and Trailer call options, and reports the call to the stats handler.
// It returns err so call sites can return through it.
func (call *directCall) finish(err error) error {
	call.finalOnce.Do(func() {
		call.mu.Lock()
		call.finished = true
		stopWatch, body := call.stopWatch, call.body
		call.mu.Unlock()
		if stopWatch != nil {
			stopWatch()
		}
		if body != nil {
			// A request still being read is cut off, as a reset stream cuts
			// off a request body.
			body.abandon()
		}
		if call.cancel != nil {
			call.cancel()
		}
		if call.res != nil {
			for _, opt := range call.opts {
				switch o := opt.(type) {
				case grpc.HeaderCallOption:
					if md, ok := call.res.headerIfReady(); ok {
						*o.HeaderAddr = md.Copy()
					}
				case grpc.TrailerCallOption:
					*o.TrailerAddr = call.res.trailerIfEnded().Copy()
				case grpc.PeerCallOption:
					*o.PeerAddr = peer.Peer{Addr: loopbackAddr}
				}
			}
		}
		if call.conn.stats != nil {
			var rpcErr error
			if err != nil && err != io.EOF {
				rpcErr = err
			}
			call.conn.stats.HandleRPC(call.ctx, &stats.End{
				Client:    true,
				BeginTime: call.begin,
				EndTime:   time.Now(),
				Error:     rpcErr,
			})
		}
	})
	return err
}

// directMessage is one response message, or the failure that ended the stream.
type directMessage struct {
	data []byte
	fail *status.Status
}

// directResponse is the http.ResponseWriter the handlers write to.
//
// It reproduces what an HTTP/2 server would put on the wire for the same
// handler calls, and reads that the way a gRPC client reads it. The server
// buffers a handler's body in 4 KB and sends nothing, headers included, until
// the handler flushes, overflows that buffer or returns; what it sends then
// depends on which of those happened first (a handler that returns first gets
// a Content-Length, and one that wrote nothing gets a single headers-only
// response). So bytes are only handed to the caller once the server would
// have sent them, and a panic discards whatever was still buffered.
type directResponse struct {
	ctx    context.Context
	cancel context.CancelFunc

	handlerHeader http.Header

	msgs     chan directMessage
	done     chan struct{} // closed once the response has ended
	rejected chan struct{} // closed if the client refuses the response headers

	// Handler side only.
	wroteHeader bool
	handlerDone bool
	code        int
	snapshot    http.Header // headers as they were when the status was fixed
	grpcBody    bool        // the snapshot says the body is gRPC frames
	head        []byte      // start of the body, to guess a missing Content-Type
	buf         []byte      // written, not yet sent
	declared    []string    // trailer names, canonical and sorted
	failed      bool        // the caller is gone or refused the response: writes fail
	// frame parser, over the bytes sent
	prefix    [grpcFramePrefixLen]byte
	prefixLen int
	inPayload bool
	payload   []byte
	need      int

	headerReady chan struct{} // closed once the headers are known or the call ended

	mu           sync.Mutex
	headersSent  bool
	headerMD     metadata.MD
	headerErr    *status.Status // the headers were not an acceptable gRPC response
	headerState  directHeaderState
	trailersOnly bool
	clientFailed bool // the caller rejected a message and stopped reading
	ended        bool
	final        *status.Status // nil for success
	trailerMD    metadata.MD
}

// directHeaderState is what a header block said besides metadata.
type directHeaderState struct {
	grpcStatus  codes.Code
	grpcMessage string
	details     []string
}

// directChunkSize is the HTTP/2 server's per-response write buffer.
const directChunkSize = 4 << 10

func newDirectResponse(ctx context.Context, cancel context.CancelFunc) *directResponse {
	return &directResponse{
		ctx:           ctx,
		cancel:        cancel,
		handlerHeader: make(http.Header, 8),
		headerReady:   make(chan struct{}),
		msgs:          make(chan directMessage, directStreamBuffer),
		done:          make(chan struct{}),
		rejected:      make(chan struct{}),
	}
}

// Header returns the header map the handlers set response headers on.
func (r *directResponse) Header() http.Header { return r.handlerHeader }

// WriteHeader fixes the response's status and headers, as on any
// http.ResponseWriter. Later changes to the header map only matter for
// trailers.
func (r *directResponse) WriteHeader(code int) {
	if code < 100 || code > 999 {
		panic(fmt.Sprintf("invalid WriteHeader code %v", code))
	}
	if r.wroteHeader {
		return
	}
	if code >= 100 && code <= 199 {
		// An informational response, which a gRPC client skips.
		return
	}
	r.wroteHeader = true
	r.code = code
	r.snapshot = r.handlerHeader.Clone()
	r.grpcBody = snapshotIsGRPC(r.snapshot)
}

// Write takes response body bytes.
func (r *directResponse) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	if !bodyAllowedForStatus(r.code) {
		return 0, http.ErrBodyNotAllowed
	}
	if r.failed {
		return 0, errDirectCallEnded
	}
	if len(p) == 0 {
		return 0, nil
	}
	if keep := directSniffLen - len(r.head); keep > 0 {
		if keep > len(p) {
			keep = len(p)
		}
		r.head = append(r.head, p[:keep]...)
	}
	written := len(p)
	// The server's buffered writer: a write that does not fit fills the
	// buffer and sends it; a large write into an empty buffer is sent as is.
	for len(p) > directChunkSize-len(r.buf) {
		if len(r.buf) == 0 {
			if err := r.sendChunk(p); err != nil {
				return 0, err
			}
			return written, nil
		}
		take := directChunkSize - len(r.buf)
		r.buf = append(r.buf, p[:take]...)
		p = p[take:]
		if err := r.flushBuffer(); err != nil {
			return 0, err
		}
	}
	r.buf = append(r.buf, p...)
	return written, nil
}

// Flush sends what is buffered, and the headers if they have not gone yet.
func (r *directResponse) Flush() {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	if len(r.buf) > 0 {
		_ = r.flushBuffer()
		return
	}
	_ = r.sendChunk(nil)
}

func (r *directResponse) flushBuffer() error {
	chunk := r.buf
	r.buf = nil
	return r.sendChunk(chunk)
}

// sendChunk is the server sending chunk, preceded by the headers if this is
// the first thing it sends.
func (r *directResponse) sendChunk(chunk []byte) error {
	if r.failed {
		return errDirectCallEnded
	}
	r.mu.Lock()
	sent := r.headersSent
	r.mu.Unlock()
	if !sent {
		r.sendHeaders(len(chunk))
		if r.failed {
			return errDirectCallEnded
		}
	}
	if !r.grpcBody || len(chunk) == 0 {
		// A gRPC client does not read the body of a response that is not gRPC.
		return nil
	}
	return r.receive(chunk)
}

// receive is the caller reading sent bytes as gRPC frames.
func (r *directResponse) receive(p []byte) error {
	for len(p) > 0 {
		if !r.inPayload {
			n := copy(r.prefix[r.prefixLen:], p)
			r.prefixLen += n
			p = p[n:]
			if r.prefixLen < grpcFramePrefixLen {
				return nil
			}
			r.prefixLen = 0
			size := binary.BigEndian.Uint32(r.prefix[1:])
			switch {
			case r.prefix[0]&1 != 0:
				// The request offers no compression, so a compressed
				// message is one the client cannot read.
				return r.fail(status.New(codes.Internal, "grpc: Decompressor is not installed for a compressed message"))
			case size > directMaxRecvBytes:
				return r.fail(status.Newf(codes.ResourceExhausted, "grpc: received message larger than max (%d vs. %d)", size, directMaxRecvBytes))
			}
			r.inPayload = true
			r.need = int(size)
			r.payload = make([]byte, 0, size)
		}
		take := r.need - len(r.payload)
		if take > len(p) {
			take = len(p)
		}
		r.payload = append(r.payload, p[:take]...)
		p = p[take:]
		if len(r.payload) == r.need {
			msg := directMessage{data: r.payload}
			r.inPayload, r.payload = false, nil
			if err := r.deliver(msg); err != nil {
				return err
			}
		}
	}
	return nil
}

// deliver hands one message to the caller.
func (r *directResponse) deliver(msg directMessage) error {
	select {
	case r.msgs <- msg:
		return nil
	case <-r.ctx.Done():
		r.failed = true
		return errDirectCallEnded
	}
}

// fail is the caller refusing a message: it receives st after the messages
// already delivered, stops reading, and resets the stream, which ends the
// handler's context.
func (r *directResponse) fail(st *status.Status) error {
	r.mu.Lock()
	r.clientFailed = true
	r.mu.Unlock()
	_ = r.deliver(directMessage{fail: st})
	r.failed = true
	r.cancel()
	return errDirectCallEnded
}

// sendHeaders is the server sending the response headers, before a first
// chunk of chunkLen bytes (zero for none). At the end of the handler it can
// also be the whole response.
func (r *directResponse) sendHeaders(chunkLen int) {
	if r.handlerDone {
		r.promoteTrailers()
	}
	block, endStream := r.firstHeaderBlock(chunkLen)
	md, state, rejected := readResponseHeaders(block, r.code)

	r.mu.Lock()
	r.headersSent = true
	r.headerErr = rejected
	r.headerState = state
	r.trailersOnly = endStream
	if rejected == nil {
		if endStream {
			// A headers-only response: the client reads the one block as
			// the trailers and reports no headers.
			r.trailerMD = md
		} else {
			r.headerMD = md
		}
	}
	r.mu.Unlock()
	close(r.headerReady)

	if rejected != nil && !endStream {
		// The client resets a stream whose headers it refuses.
		r.failed = true
		close(r.rejected)
		r.cancel()
	}
}

// firstHeaderBlock is the header block the server sends before a first chunk
// of chunkLen bytes, and whether that block also ends the response.
func (r *directResponse) firstHeaderBlock(chunkLen int) (http.Header, bool) {
	block := make(http.Header, len(r.snapshot)+3)
	for key, values := range r.snapshot {
		block[key] = values
	}
	contentLength := ""
	if values, ok := block["Content-Length"]; ok {
		delete(block, "Content-Length")
		if len(values) > 0 {
			if _, err := strconv.ParseUint(values[0], 10, 63); err == nil {
				contentLength = values[0]
			}
		}
	}
	if contentLength == "" && r.handlerDone && bodyAllowedForStatus(r.code) {
		contentLength = strconv.Itoa(chunkLen)
	}
	_, hasContentType := block["Content-Type"]
	contentEncoding := ""
	if values := block["Content-Encoding"]; len(values) > 0 {
		contentEncoding = values[0]
	}
	contentType := ""
	if contentEncoding == "" && !hasContentType && bodyAllowedForStatus(r.code) && chunkLen > 0 {
		head := r.head
		if len(head) > chunkLen {
			head = head[:chunkLen]
		}
		contentType = http.DetectContentType(head)
	}
	date := ""
	if _, ok := block["Date"]; !ok {
		date = time.Now().UTC().Format(http.TimeFormat)
	}
	for _, announced := range r.snapshot["Trailer"] {
		for _, name := range strings.Split(announced, ",") {
			if name = strings.TrimSpace(name); name != "" {
				r.declareTrailer(name)
			}
		}
	}
	delete(block, "Connection")
	// The server adds these after the handler's own headers.
	if contentType != "" {
		block["Content-Type"] = append(block["Content-Type"], contentType)
	}
	if contentLength != "" {
		block["Content-Length"] = []string{contentLength}
	}
	if date != "" {
		block["Date"] = append(block["Date"], date)
	}
	endStream := r.handlerDone && len(r.declared) == 0 && chunkLen == 0
	return block, endStream
}

func (r *directResponse) declareTrailer(name string) {
	key := http.CanonicalHeaderKey(name)
	if !httpguts.ValidTrailerHeader(key) {
		return
	}
	for _, existing := range r.declared {
		if existing == key {
			return
		}
	}
	r.declared = append(r.declared, key)
	sort.Strings(r.declared)
}

// promoteTrailers declares the trailers set with http.TrailerPrefix.
func (r *directResponse) promoteTrailers() {
	for key, values := range r.handlerHeader {
		if !strings.HasPrefix(key, http.TrailerPrefix) {
			continue
		}
		name := strings.TrimPrefix(key, http.TrailerPrefix)
		r.declareTrailer(name)
		r.handlerHeader[http.CanonicalHeaderKey(name)] = values
	}
}

// end closes the response after the handler returned.
func (r *directResponse) end() {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	r.handlerDone = true
	r.promoteTrailers()
	if len(r.buf) > 0 {
		_ = r.flushBuffer()
	} else {
		r.mu.Lock()
		sent := r.headersSent
		r.mu.Unlock()
		if !sent {
			r.sendHeaders(0)
		}
	}

	var trailers http.Header
	for _, key := range r.declared {
		if values := r.handlerHeader[key]; len(values) > 0 {
			if trailers == nil {
				trailers = make(http.Header, len(r.declared))
			}
			trailers[key] = values
		}
	}

	r.mu.Lock()
	switch {
	case r.clientFailed:
		// The caller already has its error and read nothing after it.
	case r.headerErr != nil:
		r.final = r.headerErr
	case r.trailersOnly:
		r.final = statusWithDetails(r.headerState.grpcStatus, r.headerState.grpcMessage, r.headerState.details)
	case trailers == nil:
		r.final = status.New(codes.Internal, "server closed the stream without sending trailers")
	default:
		md, state, rejected := readResponseHeaders(trailers, 0)
		if rejected != nil {
			r.final = rejected
		} else {
			r.trailerMD = md
			r.final = statusWithDetails(state.grpcStatus, state.grpcMessage, state.details)
		}
	}
	r.ended = true
	r.mu.Unlock()
	r.close()
}

// reset is the stream being reset under a handler that is still running: the
// caller fails with st at once and the handler's writes go nowhere.
func (r *directResponse) reset(st *status.Status) {
	r.mu.Lock()
	if !r.headersSent {
		r.headersSent = true
		r.headerErr = st
		close(r.headerReady)
	}
	r.mu.Unlock()
	r.failed = true
	close(r.rejected)
	r.cancel()
}

// abort closes the response after the handler panicked. The server discards
// what was still buffered and resets the stream; st is what the client
// reports for that.
func (r *directResponse) abort(st *status.Status) {
	r.buf = nil
	r.mu.Lock()
	if !r.headersSent {
		r.headersSent = true
		r.headerErr = st
		close(r.headerReady)
	}
	if !r.clientFailed {
		r.final = st
	}
	r.ended = true
	r.mu.Unlock()
	r.close()
}

func (r *directResponse) close() {
	r.cancel()
	close(r.msgs)
	close(r.done)
}

// header returns the header metadata, or the error that replaced it. Both are
// nil for a headers-only response, whose outcome is only known at the end.
func (r *directResponse) header() (metadata.MD, *status.Status) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.headerMD, r.headerErr
}

func (r *directResponse) headerIfReady() (metadata.MD, bool) {
	select {
	case <-r.headerReady:
		md, _ := r.header()
		return md, md != nil
	default:
		return nil, false
	}
}

func (r *directResponse) trailer() metadata.MD {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.trailerMD
}

func (r *directResponse) trailerIfEnded() metadata.MD {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.ended {
		return nil
	}
	return r.trailerMD
}

// result is the call's outcome once the response has ended: io.EOF for
// success, the status error otherwise.
func (r *directResponse) result() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.ended {
		return status.Error(codes.Internal, "loopback: call read before its handler ended")
	}
	if r.final == nil || r.final.Code() == codes.OK {
		return io.EOF
	}
	return r.final.Err()
}

func snapshotIsGRPC(h http.Header) bool {
	values := h["Content-Type"]
	return len(values) > 0 && validGRPCContentType(values[0])
}

// validGRPCContentType mirrors grpc-go: "application/grpc", optionally followed
// by "+" or ";" and more.
func validGRPCContentType(contentType string) bool {
	const base = "application/grpc"
	if contentType == base {
		return true
	}
	if !strings.HasPrefix(contentType, base) {
		return false
	}
	switch contentType[len(base)] {
	case '+', ';':
		return true
	}
	return false
}

// readResponseHeaders turns one header block into what a grpc-go client makes
// of it: the metadata it exposes, the gRPC status fields it consumes, and,
// when the block is not an acceptable gRPC response, the status it fails the
// call with. code is the HTTP status of a response's first block, and zero for
// a trailers block.
func readResponseHeaders(h http.Header, code int) (metadata.MD, directHeaderState, *status.Status) {
	md := make(metadata.MD, len(h)+1)
	state := directHeaderState{grpcStatus: codes.Unknown}
	// Once response headers have been accepted the peer is speaking gRPC, so
	// trailers are not checked for a content type again.
	isGRPC := code == 0
	contentTypeErr := "malformed header: missing HTTP content-type"
	headerErr := ""

	for key, values := range h {
		// What an HTTP/2 server puts on the wire: lower-case names, nothing
		// that is not a valid header, no connection-specific fields.
		if !httpguts.ValidHeaderFieldName(key) {
			continue
		}
		name := strings.ToLower(key)
		for _, value := range values {
			if !httpguts.ValidHeaderFieldValue(value) {
				continue
			}
			if name == "transfer-encoding" && value != "trailers" {
				continue
			}
			switch name {
			case "content-type":
				if !validGRPCContentType(value) {
					contentTypeErr = fmt.Sprintf("transport: received unexpected content-type %q", value)
					continue
				}
				contentTypeErr = ""
				md[name] = append(md[name], value)
				isGRPC = true
			case "grpc-encoding":
				// Never offered, so never used; grpc-go keeps it out of metadata.
			case "grpc-status":
				parsed, err := strconv.ParseInt(value, 10, 32)
				if err != nil {
					headerErr = fmt.Sprintf("transport: malformed grpc-status: %v", err)
					continue
				}
				state.grpcStatus = codes.Code(uint32(parsed))
			case "grpc-message":
				state.grpcMessage = decodeGRPCMessage(value)
			default:
				if isReservedGRPCHeader(name) && name != "user-agent" {
					continue
				}
				if strings.HasSuffix(name, "-bin") {
					decoded, err := decodeBinHeader(value)
					if err != nil {
						headerErr = fmt.Sprintf("transport: malformed %s: %v", name, err)
						continue
					}
					value = string(decoded)
				}
				md[name] = append(md[name], value)
			}
		}
	}

	if !isGRPC {
		grpcCode, ok := httpStatusToGRPC[code]
		if !ok {
			grpcCode = codes.Unknown
		}
		errs := []string{fmt.Sprintf("unexpected HTTP status code received from server: %d (%s)", code, http.StatusText(code))}
		if contentTypeErr != "" {
			errs = append(errs, contentTypeErr)
		}
		return nil, state, status.New(grpcCode, strings.Join(errs, "; "))
	}
	if headerErr != "" {
		return nil, state, status.New(codes.Internal, headerErr)
	}
	state.details = md["grpc-status-details-bin"]
	return md, state, nil
}

func bodyAllowedForStatus(code int) bool {
	switch {
	case code >= 100 && code <= 199:
		return false
	case code == http.StatusNoContent, code == http.StatusNotModified:
		return false
	}
	return true
}

// httpStatusToGRPC is grpc-go's mapping for a response that is not gRPC.
var httpStatusToGRPC = map[int]codes.Code{
	http.StatusBadRequest:         codes.Internal,
	http.StatusUnauthorized:       codes.Unauthenticated,
	http.StatusForbidden:          codes.PermissionDenied,
	http.StatusNotFound:           codes.Unimplemented,
	http.StatusTooManyRequests:    codes.Unavailable,
	http.StatusBadGateway:         codes.Unavailable,
	http.StatusServiceUnavailable: codes.Unavailable,
	http.StatusGatewayTimeout:     codes.Unavailable,
}

// statusWithDetails mirrors grpc-go's handling of grpc-status-details-bin.
func statusWithDetails(code codes.Code, message string, details []string) *status.Status {
	if len(details) != 1 {
		return status.New(code, message)
	}
	st := &spb.Status{}
	if err := proto.Unmarshal([]byte(details[0]), st); err != nil {
		return status.New(code, message)
	}
	if st.Code == int32(code) {
		return status.FromProto(st)
	}
	return status.Newf(codes.Internal,
		"grpc-status-details-bin mismatch: grpc-status=%v, grpc-message=%q, grpc-status-details-bin=%+v",
		code, message, st)
}

func statusFromContext(ctx context.Context) error {
	switch ctx.Err() {
	case context.DeadlineExceeded:
		return status.Error(codes.DeadlineExceeded, context.DeadlineExceeded.Error())
	case context.Canceled:
		return status.Error(codes.Canceled, context.Canceled.Error())
	}
	return status.Error(codes.Unknown, fmt.Sprint(ctx.Err()))
}

// isReservedGRPCHeader mirrors grpc-go's list of names metadata may not use.
func isReservedGRPCHeader(name string) bool {
	if name != "" && name[0] == ':' {
		return true
	}
	switch name {
	case "content-type", "user-agent", "grpc-message-type", "grpc-encoding",
		"grpc-message", "grpc-status", "grpc-timeout", "te":
		return true
	}
	return false
}

// validateMetadataPair mirrors grpc-go's check on outgoing metadata.
func validateMetadataPair(key string, values ...string) error {
	if key == "" {
		return fmt.Errorf("there is an empty key in the header")
	}
	if key[0] != ':' {
		for i := 0; i < len(key); i++ {
			c := key[i]
			if !(c >= 'a' && c <= 'z') && !(c >= '0' && c <= '9') && c != '.' && c != '-' && c != '_' {
				return fmt.Errorf("header key %q contains illegal characters not in [0-9a-z-_.]", key)
			}
		}
	}
	if strings.HasSuffix(key, "-bin") {
		return nil
	}
	for _, value := range values {
		for i := 0; i < len(value); i++ {
			if value[i] < 0x20 || value[i] > 0x7E {
				return fmt.Errorf("header key %q contains value with non-printable ASCII characters", key)
			}
		}
	}
	return nil
}

func decodeBinHeader(v string) ([]byte, error) {
	if len(v)%4 == 0 {
		return base64.StdEncoding.DecodeString(v)
	}
	return base64.RawStdEncoding.DecodeString(v)
}

// decodeGRPCMessage undoes the percent-encoding of a grpc-message value.
func decodeGRPCMessage(msg string) string {
	if !strings.Contains(msg, "%") {
		return msg
	}
	var sb strings.Builder
	for i := 0; i < len(msg); i++ {
		c := msg[i]
		if c == '%' && i+2 < len(msg) {
			if parsed, err := strconv.ParseUint(msg[i+1:i+3], 16, 8); err == nil {
				sb.WriteByte(byte(parsed))
				i += 2
				continue
			}
		}
		sb.WriteByte(c)
	}
	return sb.String()
}

// encodeGRPCTimeout mirrors grpc-go's grpc-timeout encoding: the largest unit
// that keeps the value within eight digits, rounded up.
func encodeGRPCTimeout(t time.Duration) string {
	const maxValue = 100000000 - 1
	div := func(d, r time.Duration) int64 {
		if d%r > 0 {
			return int64(d/r + 1)
		}
		return int64(d / r)
	}
	if t <= 0 {
		return "0n"
	}
	if d := div(t, time.Nanosecond); d <= maxValue {
		return strconv.FormatInt(d, 10) + "n"
	}
	if d := div(t, time.Microsecond); d <= maxValue {
		return strconv.FormatInt(d, 10) + "u"
	}
	if d := div(t, time.Millisecond); d <= maxValue {
		return strconv.FormatInt(d, 10) + "m"
	}
	if d := div(t, time.Second); d <= maxValue {
		return strconv.FormatInt(d, 10) + "S"
	}
	if d := div(t, time.Minute); d <= maxValue {
		return strconv.FormatInt(d, 10) + "M"
	}
	return strconv.FormatInt(div(t, time.Hour), 10) + "H"
}

// marshalGRPCFrame encodes m as one uncompressed gRPC frame: a zero flag
// byte, the payload length, the payload.
func marshalGRPCFrame(m any) ([]byte, error) {
	var msg proto.Message
	switch v := m.(type) {
	case proto.Message:
		msg = v
	case protoadapt.MessageV1:
		msg = protoadapt.MessageV2Of(v)
	default:
		return nil, fmt.Errorf("proto: failed to marshal, message is %T, want proto.Message", m)
	}
	size := proto.Size(msg)
	buf := make([]byte, grpcFramePrefixLen, grpcFramePrefixLen+size)
	buf, err := proto.MarshalOptions{UseCachedSize: true}.MarshalAppend(buf, msg)
	if err != nil {
		return nil, err
	}
	binary.BigEndian.PutUint32(buf[1:grpcFramePrefixLen], uint32(len(buf)-grpcFramePrefixLen))
	return buf, nil
}

func unmarshalGRPC(data []byte, m any) error {
	switch v := m.(type) {
	case proto.Message:
		return proto.Unmarshal(data, v)
	case protoadapt.MessageV1:
		return proto.Unmarshal(data, protoadapt.MessageV2Of(v))
	}
	return fmt.Errorf("failed to unmarshal, message is %T, want proto.Message", m)
}

// directRequestPipe is the request body of a client-streaming call: frames
// written by SendMsg, read by the handler.
type directRequestPipe struct {
	mu        sync.Mutex
	cond      *sync.Cond
	buf       []byte
	closed    bool // no more frames will be written
	gone      bool // the reader stopped reading
	abandoned bool // the call ended with the request unfinished
}

func newDirectRequestPipe() *directRequestPipe {
	p := &directRequestPipe{}
	p.cond = sync.NewCond(&p.mu)
	return p
}

// write queues one frame. It fails once the call is over on either side.
func (p *directRequestPipe) write(caller, handler context.Context, frame []byte) error {
	if caller.Err() != nil || handler.Err() != nil {
		return errDirectCallEnded
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.gone || p.abandoned {
		return errDirectCallEnded
	}
	p.buf = append(p.buf, frame...)
	p.cond.Signal()
	return nil
}

func (p *directRequestPipe) close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	p.cond.Broadcast()
}

// abandon ends a request whose call is over. What was queued can still be
// read; after it the reader gets the error an HTTP/2 server gives a handler
// whose client went away.
func (p *directRequestPipe) abandon() {
	p.mu.Lock()
	if !p.closed {
		p.abandoned = true
	}
	p.mu.Unlock()
	p.cond.Broadcast()
}

// errDirectRequestCancelled is what an HTTP/2 server's request body returns
// once the client has reset the stream (stream IDs differ on a real
// connection). Connect reads it as a cancellation.
var errDirectRequestCancelled error = http2.StreamError{StreamID: 1, Code: http2.ErrCodeCancel}

// Read blocks until a frame is queued or the request ends.
func (p *directRequestPipe) Read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for len(p.buf) == 0 {
		switch {
		case p.abandoned:
			return 0, errDirectRequestCancelled
		case p.closed || p.gone:
			return 0, io.EOF
		}
		p.cond.Wait()
	}
	n := copy(b, p.buf)
	p.buf = p.buf[n:]
	return n, nil
}

// Close is the handler saying it will read no more.
func (p *directRequestPipe) Close() error {
	p.mu.Lock()
	p.gone = true
	p.mu.Unlock()
	p.cond.Broadcast()
	return nil
}

// grpcAuthority is the :authority grpc-go gives a passthrough target's
// endpoint: localhost for an endpoint that is only a port, and anything an
// authority may not contain percent-encoded.
func grpcAuthority(endpoint string) string {
	if strings.HasPrefix(endpoint, ":") {
		return "localhost" + encodeAuthority(endpoint)
	}
	return encodeAuthority(endpoint)
}

func encodeAuthority(authority string) string {
	const upperhex = "0123456789ABCDEF"
	shouldEscape := func(c byte) bool {
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' {
			return false
		}
		switch c {
		case '-', '_', '.', '~', '!', '$', '&', '\'', '(', ')', '*', '+', ',', ';', '=', ':', '[', ']', '@':
			return false
		}
		return true
	}
	var b strings.Builder
	for i := 0; i < len(authority); i++ {
		c := authority[i]
		if shouldEscape(c) {
			b.WriteByte('%')
			b.WriteByte(upperhex[c>>4])
			b.WriteByte(upperhex[c&15])
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
