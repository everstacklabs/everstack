package deviceauth

import (
	"net/http"
	"testing"
)

func TestVerificationURIResolution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		configured string
		header     http.Header
		want       string
	}{
		{
			name:       "configured public URL wins",
			configured: "https://configured.example.com/base/",
			header: http.Header{
				"X-Forwarded-Host":  []string{"ignored.example.com"},
				"X-Forwarded-Proto": []string{"https"},
			},
			want: "https://configured.example.com/base/device",
		},
		{
			name:       "forwarded tenant host replaces configured localhost",
			configured: "http://localhost:8089",
			header: http.Header{
				"X-Forwarded-Host":  []string{"everstack-test-0185df.dev.eu-gra-1.everstack.ai"},
				"X-Forwarded-Proto": []string{"https"},
			},
			want: "https://everstack-test-0185df.dev.eu-gra-1.everstack.ai/device",
		},
		{
			name: "forwarded host and protocol",
			header: http.Header{
				"X-Forwarded-Host":  []string{"instance.example.com"},
				"X-Forwarded-Proto": []string{"https"},
			},
			want: "https://instance.example.com/device",
		},
		{
			name:   "origin fallback",
			header: http.Header{"Origin": []string{"http://localhost:8089"}},
			want:   "http://localhost:8089/device",
		},
		{
			name: "invalid forwarded protocol defaults to HTTPS",
			header: http.Header{
				"X-Forwarded-Host":  []string{"instance.example.com"},
				"X-Forwarded-Proto": []string{"javascript"},
			},
			want: "https://instance.example.com/device",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := VerificationURI(test.configured, test.header); got != test.want {
				t.Fatalf("VerificationURI() = %q, want %q", got, test.want)
			}
		})
	}
}
