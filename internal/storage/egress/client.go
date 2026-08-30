// Package egress provides the HTTP boundary for customer-configured storage
// endpoints in managed Everstack gateways. It is deliberately storage-specific:
// self-hosted deployments may need private MinIO endpoints, while a managed
// gateway must treat every tenant-supplied endpoint and DNS answer as untrusted.
package egress

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

var (
	ErrEndpointDenied = errors.New("managed storage endpoint is denied")
	ErrRedirectDenied = errors.New("managed storage redirects are denied")
)

const (
	managedDialTimeout = 10 * time.Second
	maxDNSAnswers      = 16
)

type Resolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type Dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

// Config supplies process-specific control-plane destinations in addition to
// the built-in private, local, metadata, special-use, and cluster ranges.
// Resolver and Dialer are injectable only for deterministic policy tests.
type Config struct {
	DeniedHosts []string
	DeniedCIDRs []string
	Resolver    Resolver
	Dialer      Dialer
}

type policy struct {
	expectedHost   string
	deniedHosts    []string
	deniedPrefixes []netip.Prefix
	resolver       Resolver
	dialer         Dialer
}

type guardedTransport struct {
	base   *http.Transport
	policy *policy
}

func (t *guardedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if t == nil || t.base == nil || t.policy == nil || request == nil || request.URL == nil {
		return nil, fmt.Errorf("%w: invalid managed storage request", ErrEndpointDenied)
	}
	if err := t.policy.validateRequest(request); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(request)
}

// NewManagedClient creates an HTTP client for storage calls made by a managed
// gateway. An empty endpoint selects the AWS SDK's standard public endpoint;
// any explicit endpoint must be a credential-free HTTPS URL.
func NewManagedClient(endpoint string, config Config) (*http.Client, error) {
	endpointURL, err := validateConfiguredEndpoint(endpoint)
	if err != nil {
		return nil, err
	}

	deniedPrefixes, err := parseDeniedPrefixes(config.DeniedCIDRs)
	if err != nil {
		return nil, err
	}
	deniedHosts := normalizeDeniedHosts(config.DeniedHosts)
	resolver := config.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	dialer := config.Dialer
	if dialer == nil {
		dialer = &net.Dialer{Timeout: managedDialTimeout, KeepAlive: 30 * time.Second}
	}

	endpointHost := ""
	if endpointURL != nil {
		endpointHost = normalizeHostname(endpointURL.Hostname())
	}
	connectionPolicy := &policy{
		expectedHost:   endpointHost,
		deniedHosts:    deniedHosts,
		deniedPrefixes: deniedPrefixes,
		resolver:       resolver,
		dialer:         dialer,
	}
	if endpointHost != "" {
		if err := connectionPolicy.validateHost(endpointHost); err != nil {
			return nil, err
		}
	}

	base := &http.Transport{
		Proxy:                 nil,
		DialContext:           connectionPolicy.dialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return &http.Client{
		Transport: &guardedTransport{base: base, policy: connectionPolicy},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return ErrRedirectDenied
		},
	}, nil
}

func validateConfiguredEndpoint(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" {
		return nil, fmt.Errorf("%w: explicit endpoints require HTTPS", ErrEndpointDenied)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return nil, fmt.Errorf("%w: endpoint credentials, query, and fragment are forbidden", ErrEndpointDenied)
	}
	if parsed.Opaque != "" {
		return nil, fmt.Errorf("%w: opaque endpoint URL is forbidden", ErrEndpointDenied)
	}
	return parsed, nil
}

func (p *policy) validateURL(value *url.URL) error {
	if value == nil || value.Scheme != "https" || value.Host == "" || value.Hostname() == "" || value.User != nil {
		return fmt.Errorf("%w: provider requests require HTTPS", ErrEndpointDenied)
	}
	host := normalizeHostname(value.Hostname())
	if p.expectedHost != "" && host != p.expectedHost && !strings.HasSuffix(host, "."+p.expectedHost) {
		return fmt.Errorf("%w: provider request changed endpoint host", ErrEndpointDenied)
	}
	return p.validateHost(host)
}

func (p *policy) validateRequest(request *http.Request) error {
	if request == nil || request.URL == nil {
		return fmt.Errorf("%w: invalid managed storage request", ErrEndpointDenied)
	}
	if err := p.validateURL(request.URL); err != nil {
		return err
	}
	if request.Host == "" {
		return nil
	}

	authority, err := url.Parse("//" + request.Host)
	if err != nil || authority.Host == "" || authority.Hostname() == "" || authority.User != nil || authority.Path != "" {
		return fmt.Errorf("%w: invalid provider request authority", ErrEndpointDenied)
	}
	if normalizeHostname(authority.Hostname()) != normalizeHostname(request.URL.Hostname()) {
		return fmt.Errorf("%w: provider request changed HTTP host", ErrEndpointDenied)
	}
	return nil
}

func (p *policy) validateHost(host string) error {
	host = normalizeHostname(host)
	if host == "" {
		return fmt.Errorf("%w: endpoint host is empty", ErrEndpointDenied)
	}
	if address, err := netip.ParseAddr(host); err == nil {
		if isDeniedAddress(address, p.deniedPrefixes) {
			return fmt.Errorf("%w: endpoint address is not public", ErrEndpointDenied)
		}
		return nil
	}
	if strings.Contains(host, ":") || !validDNSHostname(host) {
		return fmt.Errorf("%w: endpoint hostname is not public", ErrEndpointDenied)
	}
	for _, suffix := range []string{"localhost", "local", "internal", "svc", "cluster.local"} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return fmt.Errorf("%w: endpoint hostname is internal", ErrEndpointDenied)
		}
	}
	for _, denied := range p.deniedHosts {
		if host == denied || strings.HasSuffix(host, "."+denied) {
			return fmt.Errorf("%w: endpoint hostname is reserved", ErrEndpointDenied)
		}
	}
	return nil
}

func validDNSHostname(host string) bool {
	if len(host) > 253 || !strings.Contains(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
}

func (p *policy) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, managedDialTimeout)
	defer cancel()

	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" {
		return nil, fmt.Errorf("%w: malformed provider dial target", ErrEndpointDenied)
	}
	host = normalizeHostname(host)
	if err := p.validateHost(host); err != nil {
		return nil, err
	}

	if literal, parseErr := netip.ParseAddr(host); parseErr == nil {
		return p.dialer.DialContext(dialCtx, network, net.JoinHostPort(literal.String(), port))
	}

	resolved, err := p.resolver.LookupIPAddr(dialCtx, host)
	if err != nil {
		return nil, errors.New("managed storage endpoint DNS resolution failed")
	}
	if len(resolved) == 0 {
		return nil, errors.New("managed storage endpoint DNS returned no addresses")
	}
	if len(resolved) > maxDNSAnswers {
		return nil, fmt.Errorf("%w: DNS returned too many addresses", ErrEndpointDenied)
	}

	addresses := make([]netip.Addr, 0, len(resolved))
	seen := make(map[netip.Addr]struct{}, len(resolved))
	for _, answer := range resolved {
		resolvedAddress, ok := netip.AddrFromSlice(answer.IP)
		if !ok || answer.Zone != "" || isDeniedAddress(resolvedAddress, p.deniedPrefixes) {
			return nil, fmt.Errorf("%w: DNS returned a non-public address", ErrEndpointDenied)
		}
		resolvedAddress = resolvedAddress.Unmap()
		if _, exists := seen[resolvedAddress]; !exists {
			seen[resolvedAddress] = struct{}{}
			addresses = append(addresses, resolvedAddress)
		}
	}

	var failures []error
	for _, resolvedAddress := range addresses {
		conn, dialErr := p.dialer.DialContext(dialCtx, network, net.JoinHostPort(resolvedAddress.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		failures = append(failures, dialErr)
		if dialCtx.Err() != nil {
			break
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if dialCtx.Err() != nil {
		return nil, errors.New("managed storage endpoint connection timed out")
	}
	return nil, fmt.Errorf("managed storage endpoint connection failed: %w", errors.Join(failures...))
}

var deniedAddressPrefixes = mustPrefixes(
	"0.0.0.0/8",
	"10.0.0.0/8",
	"100.64.0.0/10",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.0.0.0/24",
	"192.0.2.0/24",
	"192.88.99.0/24",
	"192.168.0.0/16",
	"198.18.0.0/15",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"224.0.0.0/4",
	"240.0.0.0/4",
	"64:ff9b::/96",
	"64:ff9b:1::/48",
	"100::/64",
	"2001::/23",
	"2001:db8::/32",
	"2002::/16",
	"fc00::/7",
	"fe80::/10",
	"fec0::/10",
	"ff00::/8",
)

func isDeniedAddress(address netip.Addr, configured []netip.Prefix) bool {
	if !address.IsValid() || address.Zone() != "" {
		return true
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsMulticast() || address.IsUnspecified() {
		return true
	}
	for _, prefix := range deniedAddressPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	for _, prefix := range configured {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func parseDeniedPrefixes(values []string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil || prefix.Addr().Zone() != "" {
			return nil, fmt.Errorf("invalid managed storage denied CIDR")
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

func mustPrefixes(values ...string) []netip.Prefix {
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefixes = append(prefixes, netip.MustParsePrefix(value))
	}
	return prefixes
}

func normalizeHostname(host string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
}

func normalizeDeniedHosts(values []string) []string {
	unique := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = normalizeHostname(value)
		if value != "" {
			unique[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(unique))
	for value := range unique {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

// ConfigFromEnvironment reads explicit destinations owned by the current
// control plane. Kubernetes' API service address is always included when the
// process runs in a cluster. Invalid CIDRs still fail NewManagedClient closed.
func ConfigFromEnvironment() Config {
	config := Config{
		DeniedHosts: splitCSV(os.Getenv("EVS_STORAGE_EGRESS_DENY_HOSTS")),
		DeniedCIDRs: splitCSV(os.Getenv("EVS_STORAGE_EGRESS_DENY_CIDRS")),
	}
	for _, name := range []string{
		"KUBERNETES_SERVICE_HOST",
		"EVS_CLOUD_URL",
		"EVS_AUTH_SERVICE_URL",
		"EVS_SERVICES_AUTH_URL",
		"EVS_SERVICES_AUTH_EXTERNAL_URL",
		"EVS_SERVICES_FUNCTIONS_URL",
		"EVS_SERVICES_LICENSE_URL",
		"EVS_SERVICES_PROMPTS_URL",
		"EVS_SERVICES_ACTIVATION_PLATFORM_URL",
		"EVS_PLATFORM_DSN",
		"EVS_POSTGRES_DSN",
		"EVS_CLICKHOUSE_DSN",
		"EVS_CACHE_REDIS_ADDRESS",
		"EVS_SECRET_MANAGER_VAULT_ADDRESS",
		"EVS_TELEMETRY_OTLP_ENDPOINT",
		"EVS_TELEMETRY_OTEL_COLLECTOR_URL",
		"EVS_TRACING_OTEL_ENDPOINT",
		"EVS_SEARXNG_URL",
	} {
		appendDeniedDestination(&config, os.Getenv(name))
	}
	return config
}

func appendDeniedDestination(config *Config, raw string) {
	if config == nil {
		return
	}
	host := destinationHost(raw)
	if host == "" {
		return
	}
	if address, err := netip.ParseAddr(host); err == nil {
		address = address.Unmap()
		bits := 128
		if address.Is4() {
			bits = 32
		}
		config.DeniedCIDRs = append(config.DeniedCIDRs, netip.PrefixFrom(address, bits).String())
		return
	}
	config.DeniedHosts = append(config.DeniedHosts, normalizeHostname(host))
}

func destinationHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err != nil {
			return ""
		}
		return parsed.Hostname()
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		return strings.Trim(host, "[]")
	}
	if strings.ContainsAny(raw, "/?#@") {
		return ""
	}
	return strings.Trim(raw, "[]")
}

func splitCSV(value string) []string {
	var result []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}
