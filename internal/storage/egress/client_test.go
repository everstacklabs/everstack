package egress

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"testing"
)

type sequenceResolver struct {
	mu        sync.Mutex
	responses [][]net.IPAddr
	calls     int
}

func (r *sequenceResolver) LookupIPAddr(_ context.Context, _ string) ([]net.IPAddr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.responses) == 0 {
		return nil, errors.New("no resolver response configured")
	}
	index := r.calls
	if index >= len(r.responses) {
		index = len(r.responses) - 1
	}
	r.calls++
	return append([]net.IPAddr(nil), r.responses[index]...), nil
}

type recordingDialer struct {
	mu        sync.Mutex
	addresses []string
}

func (d *recordingDialer) DialContext(_ context.Context, _, address string) (net.Conn, error) {
	d.mu.Lock()
	d.addresses = append(d.addresses, address)
	d.mu.Unlock()
	client, server := net.Pipe()
	_ = server.Close()
	return client, nil
}

func TestNewManagedClientRequiresSafeExplicitHTTPSURL(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		wantErr  bool
	}{
		{name: "AWS default endpoint", endpoint: ""},
		{name: "public HTTPS endpoint", endpoint: "https://account.r2.cloudflarestorage.com"},
		{name: "public HTTPS endpoint with port and path", endpoint: "https://objects.example.com:9443/s3"},
		{name: "plain HTTP", endpoint: "http://objects.example.com", wantErr: true},
		{name: "unsupported scheme", endpoint: "ftp://objects.example.com", wantErr: true},
		{name: "relative URL", endpoint: "objects.example.com", wantErr: true},
		{name: "credential-bearing URL", endpoint: "https://user:secret@objects.example.com", wantErr: true},
		{name: "query-bearing URL", endpoint: "https://objects.example.com?token=value", wantErr: true},
		{name: "fragment-bearing URL", endpoint: "https://objects.example.com#fragment", wantErr: true},
		{name: "empty hostname label", endpoint: "https://objects..example.com", wantErr: true},
		{name: "underscore hostname", endpoint: "https://objects_internal.example.com", wantErr: true},
		{name: "leading hyphen hostname", endpoint: "https://-objects.example.com", wantErr: true},
		{name: "loopback literal", endpoint: "https://127.0.0.1", wantErr: true},
		{name: "private IPv6 literal", endpoint: "https://[fd00::1]", wantErr: true},
		{name: "public IPv6 literal", endpoint: "https://[2606:4700:4700::1111]"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewManagedClient(test.endpoint, Config{})
			if (err != nil) != test.wantErr {
				t.Fatalf("NewManagedClient(%q) error = %v, wantErr=%v", test.endpoint, err, test.wantErr)
			}
		})
	}
}

func TestManagedPolicyBlocksInternalAndControlPlaneHostnames(t *testing.T) {
	client, err := NewManagedClient("", Config{DeniedHosts: []string{"api.control.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	guarded := client.Transport.(*guardedTransport)

	denied := []string{
		"localhost",
		"storage.localhost",
		"metadata.google.internal",
		"kubernetes.default.svc",
		"postgres.everstack.svc.cluster.local",
		"printer.local",
		"single-label-host",
		"api.control.example.com",
		"child.api.control.example.com",
	}
	for _, host := range denied {
		if err := guarded.policy.validateHost(host); !errors.Is(err, ErrEndpointDenied) {
			t.Errorf("validateHost(%q) error = %v, want ErrEndpointDenied", host, err)
		}
	}
	if err := guarded.policy.validateHost("bucket.s3.eu-west-1.amazonaws.com"); err != nil {
		t.Fatalf("public AWS hostname rejected: %v", err)
	}
}

func TestManagedDialValidatesEveryResolvedAddressBeforeConnecting(t *testing.T) {
	resolver := &sequenceResolver{responses: [][]net.IPAddr{{
		{IP: net.ParseIP("93.184.216.34")},
		{IP: net.ParseIP("10.20.30.40")},
	}}}
	dialer := &recordingDialer{}
	client, err := NewManagedClient("", Config{Resolver: resolver, Dialer: dialer})
	if err != nil {
		t.Fatal(err)
	}
	guarded := client.Transport.(*guardedTransport)

	_, err = guarded.policy.dialContext(context.Background(), "tcp", "objects.example.com:443")
	if !errors.Is(err, ErrEndpointDenied) {
		t.Fatalf("dialContext() error = %v, want ErrEndpointDenied", err)
	}
	if len(dialer.addresses) != 0 {
		t.Fatalf("dialer was called before all DNS answers passed policy: %v", dialer.addresses)
	}
}

func TestManagedDialBlocksAlternateIPv4EncodingAfterResolution(t *testing.T) {
	resolver := &sequenceResolver{responses: [][]net.IPAddr{{
		{IP: net.ParseIP("127.0.0.1")},
	}}}
	dialer := &recordingDialer{}
	client, err := NewManagedClient("", Config{Resolver: resolver, Dialer: dialer})
	if err != nil {
		t.Fatal(err)
	}
	guarded := client.Transport.(*guardedTransport)

	_, err = guarded.policy.dialContext(context.Background(), "tcp", "127.1:443")
	if !errors.Is(err, ErrEndpointDenied) {
		t.Fatalf("alternate loopback dial error = %v, want ErrEndpointDenied", err)
	}
	if len(dialer.addresses) != 0 {
		t.Fatalf("alternate loopback address reached dialer: %v", dialer.addresses)
	}
}

func TestManagedDialRejectsMixedPublicIPv4AndPrivateIPv6Answers(t *testing.T) {
	resolver := &sequenceResolver{responses: [][]net.IPAddr{{
		{IP: net.ParseIP("93.184.216.34")},
		{IP: net.ParseIP("fd00::10")},
	}}}
	dialer := &recordingDialer{}
	client, err := NewManagedClient("", Config{Resolver: resolver, Dialer: dialer})
	if err != nil {
		t.Fatal(err)
	}
	guarded := client.Transport.(*guardedTransport)

	_, err = guarded.policy.dialContext(context.Background(), "tcp", "objects.example.com:443")
	if !errors.Is(err, ErrEndpointDenied) || len(dialer.addresses) != 0 {
		t.Fatalf("mixed DNS answers error=%v dialed=%v, want fail closed before dial", err, dialer.addresses)
	}
}

func TestManagedDialRejectsExcessiveDNSAnswers(t *testing.T) {
	answers := make([]net.IPAddr, maxDNSAnswers+1)
	for index := range answers {
		answers[index] = net.IPAddr{IP: net.ParseIP("93.184.216.34")}
	}
	resolver := &sequenceResolver{responses: [][]net.IPAddr{answers}}
	dialer := &recordingDialer{}
	client, err := NewManagedClient("", Config{Resolver: resolver, Dialer: dialer})
	if err != nil {
		t.Fatal(err)
	}
	guarded := client.Transport.(*guardedTransport)

	_, err = guarded.policy.dialContext(context.Background(), "tcp", "objects.example.com:443")
	if !errors.Is(err, ErrEndpointDenied) || len(dialer.addresses) != 0 {
		t.Fatalf("excessive DNS answers error=%v dialed=%v, want fail closed", err, dialer.addresses)
	}
}

func TestManagedDialPinsValidatedAddressAndRechecksDNSRebinding(t *testing.T) {
	resolver := &sequenceResolver{responses: [][]net.IPAddr{
		{{IP: net.ParseIP("93.184.216.34")}},
		{{IP: net.ParseIP("169.254.169.254")}},
	}}
	dialer := &recordingDialer{}
	client, err := NewManagedClient("", Config{Resolver: resolver, Dialer: dialer})
	if err != nil {
		t.Fatal(err)
	}
	guarded := client.Transport.(*guardedTransport)

	conn, err := guarded.policy.dialContext(context.Background(), "tcp", "objects.example.com:443")
	if err != nil {
		t.Fatalf("first dialContext() error = %v", err)
	}
	_ = conn.Close()
	if len(dialer.addresses) != 1 || dialer.addresses[0] != "93.184.216.34:443" {
		t.Fatalf("dial addresses = %v, want validated IP rather than hostname", dialer.addresses)
	}

	_, err = guarded.policy.dialContext(context.Background(), "tcp", "objects.example.com:443")
	if !errors.Is(err, ErrEndpointDenied) {
		t.Fatalf("rebound dialContext() error = %v, want ErrEndpointDenied", err)
	}
	if resolver.calls != 2 || len(dialer.addresses) != 1 {
		t.Fatalf("resolver calls=%d dial addresses=%v, want revalidation before second dial", resolver.calls, dialer.addresses)
	}
}

func TestManagedPolicyDeniedAddressRanges(t *testing.T) {
	denied := []string{
		"0.1.2.3",
		"10.0.0.1",
		"100.64.0.1",
		"127.0.0.1",
		"169.254.169.254",
		"172.16.0.1",
		"192.168.0.1",
		"192.0.2.1",
		"198.18.0.1",
		"198.51.100.1",
		"203.0.113.1",
		"224.0.0.1",
		"240.0.0.1",
		"::1",
		"::ffff:10.0.0.1",
		"64:ff9b::a00:1",
		"100::1",
		"2001:db8::1",
		"2002:0a00:0001::",
		"fc00::1",
		"fe80::1",
		"fec0::1",
		"ff02::1",
	}
	for _, value := range denied {
		address := netip.MustParseAddr(value)
		if !isDeniedAddress(address, nil) {
			t.Errorf("isDeniedAddress(%s) = false, want true", address)
		}
	}

	allowed := []string{"1.1.1.1", "8.8.8.8", "93.184.216.34", "2606:4700:4700::1111"}
	for _, value := range allowed {
		address := netip.MustParseAddr(value)
		if isDeniedAddress(address, nil) {
			t.Errorf("isDeniedAddress(%s) = true, want false", address)
		}
	}
}

func TestManagedPolicyAppliesConfiguredControlPlaneCIDRs(t *testing.T) {
	client, err := NewManagedClient("", Config{DeniedCIDRs: []string{"93.184.216.0/24", "2606:4700::/32"}})
	if err != nil {
		t.Fatal(err)
	}
	guarded := client.Transport.(*guardedTransport)
	for _, value := range []string{"93.184.216.34", "2606:4700:4700::1111"} {
		if !isDeniedAddress(netip.MustParseAddr(value), guarded.policy.deniedPrefixes) {
			t.Errorf("configured control-plane address %s was allowed", value)
		}
	}
	if _, err := NewManagedClient("", Config{DeniedCIDRs: []string{"not-a-cidr"}}); err == nil {
		t.Fatal("invalid configured deny CIDR was accepted")
	}
}

func TestManagedClientRejectsRedirectsAndAmbientProxying(t *testing.T) {
	client, err := NewManagedClient("https://objects.example.com", Config{})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodGet, "https://redirect.example.com/object", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CheckRedirect(request, nil); !errors.Is(err, ErrRedirectDenied) {
		t.Fatalf("CheckRedirect() error = %v, want ErrRedirectDenied", err)
	}
	guarded := client.Transport.(*guardedTransport)
	if guarded.base.Proxy != nil {
		t.Fatal("managed storage transport inherited an ambient proxy")
	}

	insecure, err := http.NewRequest(http.MethodGet, "http://objects.example.com/object", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := guarded.policy.validateURL(insecure.URL); !errors.Is(err, ErrEndpointDenied) {
		t.Fatalf("validateURL(http) error = %v, want ErrEndpointDenied", err)
	}
	hostSwitch, err := http.NewRequest(http.MethodGet, "https://metadata.example.net/object", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := guarded.policy.validateURL(hostSwitch.URL); !errors.Is(err, ErrEndpointDenied) {
		t.Fatalf("validateURL(host switch) error = %v, want ErrEndpointDenied", err)
	}

	hostOverride, err := http.NewRequest(http.MethodGet, "https://objects.example.com/object", nil)
	if err != nil {
		t.Fatal(err)
	}
	hostOverride.Host = "metadata.google.internal"
	if err := guarded.policy.validateRequest(hostOverride); !errors.Is(err, ErrEndpointDenied) {
		t.Fatalf("validateRequest(Host override) error = %v, want ErrEndpointDenied", err)
	}
}

type failingResolver struct{ secret string }

func (r failingResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return nil, errors.New(r.secret)
}

func TestManagedDialSanitizesResolverFailure(t *testing.T) {
	const secret = "resolver leaked credential value"
	client, err := NewManagedClient("", Config{Resolver: failingResolver{secret: secret}})
	if err != nil {
		t.Fatal(err)
	}
	guarded := client.Transport.(*guardedTransport)
	_, err = guarded.policy.dialContext(context.Background(), "tcp", "objects.example.com:443")
	if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "objects.example.com") {
		t.Fatalf("resolver failure was not sanitized: %v", err)
	}
}

func TestConfigFromEnvironmentCollectsExplicitControlPlaneDestinations(t *testing.T) {
	t.Setenv("EVS_STORAGE_EGRESS_DENY_HOSTS", "api.control.example.com, metrics.control.example.com")
	t.Setenv("EVS_STORAGE_EGRESS_DENY_CIDRS", "203.0.113.0/24,2001:db8::/32")
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.96.0.1")
	t.Setenv("EVS_CLOUD_URL", "https://app.everstack.ai")
	t.Setenv("EVS_AUTH_SERVICE_URL", "http://everstack-services.everstack.svc.cluster.local:8093")
	t.Setenv("EVS_SERVICES_FUNCTIONS_URL", "https://functions.control.example.com")
	t.Setenv("EVS_PLATFORM_DSN", "postgres://user:secret@platform-db.example.net/everstack")
	t.Setenv("EVS_CACHE_REDIS_ADDRESS", "198.51.100.10:6379")
	t.Setenv("EVS_SECRET_MANAGER_VAULT_ADDRESS", "https://vault.control.example.com")

	config := ConfigFromEnvironment()
	for _, host := range []string{
		"api.control.example.com",
		"metrics.control.example.com",
		"app.everstack.ai",
		"everstack-services.everstack.svc.cluster.local",
		"functions.control.example.com",
		"platform-db.example.net",
		"vault.control.example.com",
	} {
		if !sliceContains(config.DeniedHosts, host) {
			t.Errorf("DeniedHosts = %v, missing %q", config.DeniedHosts, host)
		}
	}
	for _, cidr := range []string{
		"203.0.113.0/24",
		"2001:db8::/32",
		"10.96.0.1/32",
		"198.51.100.10/32",
	} {
		if !sliceContains(config.DeniedCIDRs, cidr) {
			t.Errorf("DeniedCIDRs = %v, missing %q", config.DeniedCIDRs, cidr)
		}
	}
}

func sliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
