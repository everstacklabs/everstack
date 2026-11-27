package httpclient_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/httpclient"
	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"

	// The adapters whose constructors wrap the client they are handed.
	_ "github.com/everstacklabs/everstack/internal/providers/anthropic"
	_ "github.com/everstacklabs/everstack/internal/providers/azure_openai"
	_ "github.com/everstacklabs/everstack/internal/providers/openai"
	_ "github.com/everstacklabs/everstack/internal/providers/vertex_ai"
)

// instrumentedDepth counts InstrumentedTransport layers above the base.
func instrumentedDepth(rt http.RoundTripper) int {
	depth := 0
	for {
		it, ok := rt.(*attrs.InstrumentedTransport)
		if !ok {
			return depth
		}
		depth++
		rt = it.Base()
	}
}

func buildWrappingProviders(t *testing.T) {
	t.Helper()
	inputs := map[string]factory.AggregatedInput{
		"openai":       {Provider: "openai", APIKey: "sk-test", Models: []string{"gpt-4o"}},
		"anthropic":    {Provider: "anthropic", APIKey: "sk-ant-test", Models: []string{"claude-x"}},
		"azure-openai": {Provider: "azure-openai", APIKey: "az-test", BaseURL: "https://example.invalid", Models: []string{"gpt-4o"}},
		// Vertex fails to build without real credentials, but its
		// constructor wraps the client before it parses them, which is
		// exactly the path that used to mutate the shared client.
		"vertex-ai": {Provider: "vertex-ai", APIKey: "not-json", Models: []string{"gemini-x"}},
	}
	for name, in := range inputs {
		f, ok := factory.Default.Get(name)
		if !ok {
			t.Fatalf("factory %q not registered", name)
		}
		_, _ = f(in)
	}
}

// Building providers through the real factories many times must not grow
// the instrumented chain on the shared provider client: in shared mode a
// tenant bundle is rebuilt every 15 seconds, so a per-construction layer
// made per-request overhead grow with uptime.
func TestProviderConstructionDoesNotGrowTransportChain(t *testing.T) {
	buildWrappingProviders(t)
	first := instrumentedDepth(httpclient.Default().Transport)
	for i := 0; i < 50; i++ {
		buildWrappingProviders(t)
	}
	if got := instrumentedDepth(httpclient.Default().Transport); got != first || got > 1 {
		t.Fatalf("instrumented chain depth on the shared client = %d after 51 builds (was %d after 1); want constant and at most 1", got, first)
	}
	if _, ok := httpclient.Default().Transport.(*http.Transport); !ok {
		t.Fatalf("Default() transport = %T, want the bare tuned *http.Transport", httpclient.Default().Transport)
	}
}

// Wrapping the same client repeatedly (an adapter handed a caller-owned
// HTTPClient) stays one layer deep and takes the latest provider name.
func TestInstrumentedWrapIsIdempotent(t *testing.T) {
	cli := &http.Client{Transport: http.DefaultTransport}
	for i := 0; i < 50; i++ {
		cli.Transport = attrs.NewInstrumentedTransport(cli.Transport, "openai")
	}
	if got := instrumentedDepth(cli.Transport); got != 1 {
		t.Fatalf("depth after 50 wraps = %d, want 1", got)
	}
	if base := cli.Transport.(*attrs.InstrumentedTransport).Base(); base != http.DefaultTransport {
		t.Fatalf("base = %T, want http.DefaultTransport", base)
	}
}

// Provider construction concurrent with requests on the shared client. Run
// with -race: the old shared client had its Transport field written by every
// construction while in-flight requests read it.
func TestProviderConstructionConcurrentWithRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			buildWrappingProviders(t)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			resp, err := httpclient.Default().Get(srv.URL)
			if err == nil {
				resp.Body.Close()
			}
		}
	}()
	wg.Wait()
}
