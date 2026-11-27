package logging

import (
	"context"
	"errors"
	"io"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/sirupsen/logrus"
)

// loggedLines captures what the package logger emits. The logger package logs
// through logrus's standard logger unless a process configures its own.
type loggedLines struct {
	entries []*logrus.Entry
}

func (h *loggedLines) Levels() []logrus.Level { return logrus.AllLevels }
func (h *loggedLines) Fire(e *logrus.Entry) error {
	h.entries = append(h.entries, e)
	return nil
}

func captureLogs(t *testing.T, level logrus.Level) *loggedLines {
	t.Helper()
	std := logrus.StandardLogger()
	prevOut, prevLevel, prevHooks := std.Out, std.GetLevel(), std.ReplaceHooks(make(logrus.LevelHooks))
	lines := &loggedLines{}
	std.SetOutput(io.Discard)
	std.SetLevel(level)
	std.AddHook(lines)
	t.Cleanup(func() {
		std.SetOutput(prevOut)
		std.SetLevel(prevLevel)
		std.ReplaceHooks(prevHooks)
	})
	return lines
}

type chatOnly struct {
	fail error
}

func (p *chatOnly) Name() string              { return "openai" }
func (p *chatOnly) SupportsModel(string) bool { return true }
func (p *chatOnly) Chat(context.Context, gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	if p.fail != nil {
		return gw.ChatCompletionResponse{}, p.fail
	}
	return gw.ChatCompletionResponse{
		ID:      "chatcmpl-1",
		Model:   "gpt-4o-mini",
		Choices: []gw.Choice{{Message: gw.NewMessage(gw.RoleAssistant, gw.Text("hello"))}},
		Usage:   gw.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10},
	}, nil
}
func (p *chatOnly) ChatStream(context.Context, gw.ChatCompletionRequest, func(gw.ChatResponseChunk) error) error {
	return errors.New("not used")
}
func (p *chatOnly) Embed(context.Context, gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	return gw.EmbeddingsResponse{}, errors.New("not used")
}

// countingExtractor counts how often response metadata is extracted, which
// only the response log line needs.
type countingExtractor struct {
	MetadataExtractor
	responses int
}

func (c *countingExtractor) ExtractFromResponse(r gw.ChatCompletionResponse) ProviderMetadata {
	c.responses++
	return c.MetadataExtractor.ExtractFromResponse(r)
}

func chatRequest() gw.ChatCompletionRequest {
	return gw.ChatCompletionRequest{
		Model:    "gpt-4o-mini",
		Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.Text("what is the capital of France?"))},
	}
}

func messages(lines *loggedLines) []string {
	out := make([]string, len(lines.entries))
	for i, e := range lines.entries {
		out[i] = e.Message
	}
	return out
}

// With Info logging on, a chat call records the request and the response, each
// with its structured payload. These are the operational logs shown to users.
func TestChatLogsRequestAndResponseAtInfo(t *testing.T) {
	lines := captureLogs(t, logrus.InfoLevel)
	extractor := &countingExtractor{MetadataExtractor: SelectExtractor("openai")}

	resp, err := NewMiddleware(&chatOnly{}, extractor).Chat(context.Background(), chatRequest())
	if err != nil || resp.ID != "chatcmpl-1" {
		t.Fatalf("Chat = (%+v, %v)", resp, err)
	}

	got := messages(lines)
	if len(got) != 2 || got[0] != "provider request issued" || got[1] != "provider response received" {
		t.Fatalf("logged %q, want the request line then the response line", got)
	}
	for _, e := range lines.entries {
		if payload, _ := e.Data["log_payload"].(string); payload == "" {
			t.Fatalf("line %q has no payload", e.Message)
		}
	}
	if lines.entries[1].Data["completion_tokens"] != 3 {
		t.Fatalf("response line completion_tokens = %v, want 3", lines.entries[1].Data["completion_tokens"])
	}
}

// With Info logging off those lines are never written, so the work that only
// exists to fill them is skipped. The caller sees the same response.
func TestChatSkipsInfoPayloadWorkWhenInfoIsOff(t *testing.T) {
	lines := captureLogs(t, logrus.WarnLevel)
	extractor := &countingExtractor{MetadataExtractor: SelectExtractor("openai")}

	resp, err := NewMiddleware(&chatOnly{}, extractor).Chat(context.Background(), chatRequest())
	if err != nil || resp.ID != "chatcmpl-1" || resp.Usage.TotalTokens != 10 {
		t.Fatalf("Chat = (%+v, %v)", resp, err)
	}
	if len(lines.entries) != 0 {
		t.Fatalf("logged %q at warn level", messages(lines))
	}
	if extractor.responses != 0 {
		t.Fatalf("response metadata was extracted %d times for a line that is not logged", extractor.responses)
	}
}

// A failed call is an Error line and is logged at any level that matters,
// with its payload.
func TestChatStillLogsFailuresWhenInfoIsOff(t *testing.T) {
	lines := captureLogs(t, logrus.WarnLevel)
	upstream := errors.New("upstream 503")

	_, err := NewMiddleware(&chatOnly{fail: upstream}, SelectExtractor("openai")).Chat(context.Background(), chatRequest())
	if !errors.Is(err, upstream) {
		t.Fatalf("err = %v, want the upstream error", err)
	}
	got := messages(lines)
	if len(got) != 1 || got[0] != "provider request failed" {
		t.Fatalf("logged %q, want the failure line", got)
	}
	if payload, _ := lines.entries[0].Data["log_payload"].(string); payload == "" {
		t.Fatalf("failure line has no payload")
	}
}
