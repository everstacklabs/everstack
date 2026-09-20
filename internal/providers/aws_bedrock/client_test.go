package aws_bedrock

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
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

func TestDerefUsageNormalizesBedrockCacheBuckets(t *testing.T) {
	t.Parallel()

	got := derefUsage(&bedrocktypes.TokenUsage{
		InputTokens:           aws.Int32(100),
		OutputTokens:          aws.Int32(30),
		TotalTokens:           aws.Int32(130),
		CacheReadInputTokens:  aws.Int32(50),
		CacheWriteInputTokens: aws.Int32(20),
	})

	if got.PromptTokens != 170 || got.CompletionTokens != 30 || got.TotalTokens != 200 {
		t.Fatalf("usage = %+v, want normalized prompt/completion/total 170/30/200", got)
	}
	if got.PromptDetails == nil {
		t.Fatal("expected cache token details")
	}
	if details := got.PromptDetails; details.CachedTokens != 70 || details.CacheReadTokens != 50 || details.CacheWriteTokens != 20 {
		t.Fatalf("prompt details = %+v, want aggregate/read/write 70/50/20", details)
	}
}
