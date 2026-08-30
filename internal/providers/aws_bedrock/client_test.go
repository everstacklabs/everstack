package aws_bedrock

import (
	"testing"

	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

func TestResolveRegion(t *testing.T) {
	if got := resolveRegion("https://bedrock-runtime.us-west-2.amazonaws.com"); got != "us-west-2" {
		t.Fatalf("expected us-west-2, got %s", got)
	}
}

func TestTextFromContent(t *testing.T) {
	got := textFromContent([]gateway.ContentPart{gateway.Text("hi"), gateway.Text(" there")})
	if got != "hi there" {
		t.Fatalf("unexpected text: %q", got)
	}
}
