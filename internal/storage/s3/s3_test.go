package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	storageegress "github.com/everstacklabs/everstack/internal/storage/egress"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestNormalizeBaseEndpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		endpoint string
		bucket   string
		want     string
	}{
		{
			name:     "strips direct bucket suffix",
			endpoint: "https://example.com/my-bucket",
			bucket:   "my-bucket",
			want:     "https://example.com",
		},
		{
			name:     "strips bucket suffix with path prefix",
			endpoint: "https://example.com/storage/my-bucket",
			bucket:   "my-bucket",
			want:     "https://example.com/storage",
		},
		{
			name:     "preserves query string while stripping bucket",
			endpoint: "https://example.com/my-bucket?x=1",
			bucket:   "my-bucket",
			want:     "https://example.com?x=1",
		},
		{
			name:     "does not modify when bucket not in path",
			endpoint: "https://example.com/storage",
			bucket:   "my-bucket",
			want:     "https://example.com/storage",
		},
		{
			name:     "does not modify malformed url",
			endpoint: "://bad-url",
			bucket:   "my-bucket",
			want:     "://bad-url",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := normalizeBaseEndpoint(tt.endpoint, tt.bucket)
			if got != tt.want {
				t.Fatalf("normalizeBaseEndpoint() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewManagedEgressRejectsUnsafeEndpointAndCustomClient(t *testing.T) {
	_, err := New(context.Background(), Config{
		Endpoint:             "http://127.0.0.1:9000",
		Region:               "us-east-1",
		Bucket:               "bucket",
		AccessKeyID:          "access",
		SecretAccessKey:      "secret",
		EnforceManagedEgress: true,
	})
	if !errors.Is(err, storageegress.ErrEndpointDenied) {
		t.Fatalf("New() unsafe managed endpoint error = %v, want ErrEndpointDenied", err)
	}

	_, err = New(context.Background(), Config{
		Region:               "us-east-1",
		Bucket:               "bucket",
		AccessKeyID:          "access",
		SecretAccessKey:      "secret",
		HTTPClient:           http.DefaultClient,
		EnforceManagedEgress: true,
	})
	if err == nil {
		t.Fatal("New() accepted a caller-supplied HTTP client while managed egress was enabled")
	}
}

func TestProviderRequestLoggingIsDisabledByDefault(t *testing.T) {
	var logs bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store, err := New(context.Background(), Config{
		Endpoint:        server.URL,
		Region:          "us-east-1",
		Bucket:          "bucket",
		AccessKeyID:     "access-key-value",
		SecretAccessKey: "secret-key-value",
		ForcePathStyle:  true,
		HTTPClient:      server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := store.Verify(context.Background(), "bucket"); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if strings.Contains(logs.String(), "s3 HTTP request") {
		t.Fatalf("default provider client logged request details: %s", logs.String())
	}
}

func TestSensitiveProviderHeadersAreNeverReturnedToClients(t *testing.T) {
	for _, header := range []string{"Authorization", "proxy-authorization", "X-Amz-Security-Token"} {
		if !isSensitiveResponseHeader(header) {
			t.Errorf("isSensitiveResponseHeader(%q) = false", header)
		}
	}
	if isSensitiveResponseHeader("Content-Type") {
		t.Fatal("Content-Type must remain available for signed uploads")
	}
}

func TestProviderDebugLoggingAndErrorsRedactSensitiveTraffic(t *testing.T) {
	const (
		accessKey = "access-key-value"
		secretKey = "secret-key-value"
		signature = "signed-query-value"
	)

	var logs bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("provider request failed: " + req.URL.String() + " Authorization: Bearer " + secretKey + " X-Amz-Signature=" + signature)
	})}
	store, err := New(context.Background(), Config{
		Endpoint:        "https://storage.example/" + accessKey,
		Region:          "us-east-1",
		Bucket:          "bucket",
		AccessKeyID:     accessKey,
		SecretAccessKey: secretKey,
		ForcePathStyle:  true,
		HTTPClient:      httpClient,
		DebugHTTP:       true,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	exporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	ctx, span := tracerProvider.Tracer("storage-redaction-test").Start(context.Background(), "storage.verify")
	err = store.Verify(ctx, "bucket")
	if err == nil {
		t.Fatal("Verify() error = nil")
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
	span.End()

	combined := logs.String() + "\n" + err.Error() + "\n" + fmt.Sprintf("%+v", exporter.GetSpans())
	for _, forbidden := range []string{accessKey, secretKey, signature, "Authorization:", "X-Amz-Signature="} {
		if strings.Contains(combined, forbidden) {
			t.Errorf("provider diagnostics expose %q: %s", forbidden, combined)
		}
	}
	if !strings.Contains(logs.String(), "s3 HTTP request") {
		t.Fatalf("explicit debug mode did not log safe request metadata: %s", logs.String())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
