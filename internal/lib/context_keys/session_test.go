package contextkeys

import (
	"context"
	"testing"

	"github.com/everstacklabs/everstack/internal/api/common"
	"google.golang.org/grpc/metadata"
)

func TestExtractSessionID_FromContextValue(t *testing.T) {
	ctx := WithSessionID(context.Background(), "sess_ctx")
	if got := ExtractSessionID(ctx); got != "sess_ctx" {
		t.Fatalf("ExtractSessionID = %q, want sess_ctx", got)
	}
}

func TestExtractSessionID_FromHeaders(t *testing.T) {
	cases := []struct {
		name   string
		header string
	}{
		{"x-session-id", common.XSessionID},
		{"x-mf-session-id", common.EverstackSessionID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			md := metadata.New(map[string]string{tc.header: "sess_hdr"})
			ctx := metadata.NewIncomingContext(context.Background(), md)
			if got := ExtractSessionID(ctx); got != "sess_hdr" {
				t.Fatalf("ExtractSessionID = %q, want sess_hdr", got)
			}
		})
	}
}

func TestExtractSessionID_EmptyWhenAbsent(t *testing.T) {
	if got := ExtractSessionID(context.Background()); got != "" {
		t.Fatalf("ExtractSessionID = %q, want empty (no generated fallback)", got)
	}
}

func TestExtractSessionID_ContextWins(t *testing.T) {
	md := metadata.New(map[string]string{common.XSessionID: "from_header"})
	ctx := metadata.NewIncomingContext(context.Background(), md)
	ctx = WithSessionID(ctx, "from_context")
	if got := ExtractSessionID(ctx); got != "from_context" {
		t.Fatalf("ExtractSessionID = %q, want from_context (context takes priority)", got)
	}
}

func TestWithSessionID_EmptyNoop(t *testing.T) {
	parent := context.Background()
	if got := WithSessionID(parent, ""); got != parent {
		t.Fatal("WithSessionID with empty id should return the parent context unchanged")
	}
}

func TestExtractThreadID_FromHeaders(t *testing.T) {
	cases := []struct {
		name   string
		header string
	}{
		{"x-thread-id", common.XThreadID},
		{"x-mf-thread-id", common.EverstackThreadID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			md := metadata.New(map[string]string{tc.header: "thread_hdr"})
			ctx := metadata.NewIncomingContext(context.Background(), md)
			if got := ExtractThreadID(ctx); got != "thread_hdr" {
				t.Fatalf("ExtractThreadID = %q, want thread_hdr", got)
			}
		})
	}
}

func TestExtractThreadID_ContextWinsAndEmptyDefault(t *testing.T) {
	if got := ExtractThreadID(context.Background()); got != "" {
		t.Fatalf("ExtractThreadID = %q, want empty when absent", got)
	}
	ctx := WithThreadID(context.Background(), "thread_ctx")
	if got := ExtractThreadID(ctx); got != "thread_ctx" {
		t.Fatalf("ExtractThreadID = %q, want thread_ctx", got)
	}
}
