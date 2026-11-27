package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// Caller authentication for the agent's control plane.
//
// The agent runs with hostNetwork on the KVM node, so its gRPC and
// metrics listeners are reachable by anything that can reach the node:
// every pod on the cluster, every peer on the node's overlay networks,
// and (absent a host firewall) the guests it runs. Every RPC can create,
// exec into or destroy a sandbox, so each call must prove it comes from
// the gateway. A shared bearer token from a Kubernetes Secret is checked
// on every FirecrackerAgent RPC and on the /logs/ SSE endpoint.
//
// Environment:
//
//	FC_AUTH_TOKEN                  shared secret callers present as
//	                               "authorization: Bearer <token>"
//	FC_AUTH_ALLOW_UNAUTHENTICATED  "true" accepts calls that carry no
//	                               valid token (logged, rate limited).
//	                               Rollout aid only; default false.
//
// With no token and the flag unset the agent refuses to start: an
// operator must opt in to an open control plane explicitly.

const (
	authTokenEnv       = "FC_AUTH_TOKEN"
	authAllowUnauthEnv = "FC_AUTH_ALLOW_UNAUTHENTICATED"

	// healthServicePrefix is exempt: kubelet's gRPC probes and the
	// gateway's client-side health checks send no credentials.
	healthServicePrefix = "/grpc.health.v1.Health/"

	bearerPrefix = "Bearer "

	// unauthWarnInterval bounds how often allow-mode logs an
	// unauthenticated caller, so a chatty legacy gateway cannot flood
	// the log while still leaving a trail the operator can grep for
	// before flipping enforcement on.
	unauthWarnInterval = 10 * time.Second
)

var errNoAuthConfigured = errors.New(
	"firecracker-agent: " + authTokenEnv + " is not set; refusing to serve an unauthenticated control plane " +
		"(set " + authAllowUnauthEnv + "=true to opt in during rollout)")

type authConfig struct {
	token                string
	allowUnauthenticated bool
}

// loadAuthConfig reads the auth settings from the environment and
// applies the fail-closed rule.
func loadAuthConfig() (authConfig, error) {
	cfg := authConfig{token: strings.TrimSpace(os.Getenv(authTokenEnv))}
	if v := strings.TrimSpace(os.Getenv(authAllowUnauthEnv)); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return authConfig{}, errors.New("firecracker-agent: " + authAllowUnauthEnv + " must be a boolean")
		}
		cfg.allowUnauthenticated = b
	}
	if cfg.token == "" && !cfg.allowUnauthenticated {
		return authConfig{}, errNoAuthConfigured
	}
	return cfg, nil
}

// describe is safe to log: it never includes the token.
func (c authConfig) describe() string {
	switch {
	case c.token != "" && !c.allowUnauthenticated:
		return "bearer token required"
	case c.token != "" && c.allowUnauthenticated:
		return "bearer token configured, unauthenticated calls ALLOWED (rollout mode)"
	default:
		return "NO token configured, unauthenticated calls ALLOWED"
	}
}

type authenticator struct {
	// tokenDigest is sha256(token); comparing digests makes the compare
	// constant time regardless of the presented length.
	tokenDigest          []byte
	allowUnauthenticated bool

	// Allow-mode reporting: unauthenticated calls are counted per peer
	// host and per method, and one summary line is logged per
	// warnInterval naming every caller seen in that window. The
	// operator reads that list to find which callers still lack the
	// token before flipping enforcement on.
	warnInterval   time.Duration
	warnMu         sync.Mutex
	pendingPeers   map[string]int
	pendingMethods map[string]int
	pendingCalls   int
	flushTimer     *time.Timer
}

func newAuthenticator(cfg authConfig) *authenticator {
	a := &authenticator{
		allowUnauthenticated: cfg.allowUnauthenticated,
		warnInterval:         unauthWarnInterval,
		pendingPeers:         map[string]int{},
		pendingMethods:       map[string]int{},
	}
	if cfg.token != "" {
		sum := sha256.Sum256([]byte(cfg.token))
		a.tokenDigest = sum[:]
	}
	return a
}

// presentedTokenMatches reports whether the given Authorization header
// value carries the configured token.
func (a *authenticator) presentedTokenMatches(header string) bool {
	if a.tokenDigest == nil {
		return false
	}
	if !strings.HasPrefix(header, bearerPrefix) {
		return false
	}
	presented := sha256.Sum256([]byte(strings.TrimSpace(strings.TrimPrefix(header, bearerPrefix))))
	return subtle.ConstantTimeCompare(presented[:], a.tokenDigest) == 1
}

// authorize decides for one call. source and method are only used for
// the allow-mode log line.
func (a *authenticator) authorize(header, source, method string) error {
	if a.presentedTokenMatches(header) {
		return nil
	}
	if a.allowUnauthenticated {
		a.warnUnauthenticated(source, method)
		return nil
	}
	return status.Error(codes.Unauthenticated, "firecracker-agent: missing or invalid bearer token")
}

// warnUnauthenticated records one accepted-without-token call. The
// first call in a quiet period arms a timer; when it fires, every call
// recorded since is reported in a single line.
func (a *authenticator) warnUnauthenticated(source, method string) {
	a.warnMu.Lock()
	defer a.warnMu.Unlock()
	a.pendingPeers[peerHost(source)]++
	a.pendingMethods[shortMethod(method)]++
	a.pendingCalls++
	if a.flushTimer == nil {
		a.flushTimer = time.AfterFunc(a.warnInterval, func() {
			if line, ok := a.flushUnauthenticated(); ok {
				log.Print(line)
			}
		})
	}
}

// flushUnauthenticated drains the pending window into one summary
// line. ok is false when nothing was recorded.
func (a *authenticator) flushUnauthenticated() (string, bool) {
	a.warnMu.Lock()
	defer a.warnMu.Unlock()
	if a.flushTimer != nil {
		a.flushTimer.Stop()
		a.flushTimer = nil
	}
	if a.pendingCalls == 0 {
		return "", false
	}
	peers := sortedKeys(a.pendingPeers)
	methods := sortedKeys(a.pendingMethods)
	line := fmt.Sprintf(
		"firecracker-agent: WARN unauthenticated calls accepted (allow mode) calls=%d peers=%s methods=%s window=%s",
		a.pendingCalls, strings.Join(peers, ","), strings.Join(methods, ","), a.warnInterval,
	)
	a.pendingPeers = map[string]int{}
	a.pendingMethods = map[string]int{}
	a.pendingCalls = 0
	return line, true
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// peerHost strips the ephemeral port so one caller is one entry.
func peerHost(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

// shortMethod reduces "/pkg.Service/Method" to "Method"; HTTP paths are
// kept as they are.
func shortMethod(method string) string {
	if i := strings.LastIndex(method, "/"); i >= 0 && strings.Contains(method[:i], ".") {
		return method[i+1:]
	}
	return method
}

func headerFromMetadata(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get("authorization")
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

func peerAddr(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		return p.Addr.String()
	}
	return "unknown"
}

func (a *authenticator) unaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !strings.HasPrefix(info.FullMethod, healthServicePrefix) {
			if err := a.authorize(headerFromMetadata(ctx), peerAddr(ctx), info.FullMethod); err != nil {
				return nil, err
			}
		}
		return handler(ctx, req)
	}
}

func (a *authenticator) streamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if !strings.HasPrefix(info.FullMethod, healthServicePrefix) {
			ctx := ss.Context()
			if err := a.authorize(headerFromMetadata(ctx), peerAddr(ctx), info.FullMethod); err != nil {
				return err
			}
		}
		return handler(srv, ss)
	}
}

// requireHTTP gates an HTTP handler on the same bearer token. Used for
// the /logs/ SSE endpoint on the metrics listener; /metrics and /healthz
// are deliberately left open.
func (a *authenticator) requireHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := a.authorize(r.Header.Get("Authorization"), r.RemoteAddr, r.URL.Path); err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="firecracker-agent"`)
			http.Error(w, "missing or invalid bearer token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// serverInterceptors is the full chain in the order it must run: auth
// first so an unauthenticated caller learns nothing about readiness,
// then the readiness gate, then the not-found mapping.
func serverInterceptors(auth *authenticator, ready *readiness) []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(auth.unaryInterceptor(), notReadyUnaryInterceptor(ready), vmNotFoundUnaryInterceptor()),
		grpc.ChainStreamInterceptor(auth.streamInterceptor(), notReadyStreamInterceptor(ready), vmNotFoundStreamInterceptor()),
	}
}
