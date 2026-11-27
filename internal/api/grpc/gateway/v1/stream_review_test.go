package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	httpmw "github.com/everstacklabs/everstack/internal/api/http/middleware"
	"google.golang.org/protobuf/encoding/protojson"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	pb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
)

type reviewedStreamProvider struct {
	*scriptedModel
	chunks   []gw.ChatResponseChunk
	tailErr  error
	returned bool
}

func (p *reviewedStreamProvider) ChatStream(_ context.Context, _ gw.ChatCompletionRequest, on func(gw.ChatResponseChunk) error) error {
	for _, c := range p.chunks {
		if err := on(c); err != nil {
			return err
		}
	}
	p.returned = p.tailErr == nil
	return p.tailErr
}
func reviewCall(index *int, id, args, finish string) gw.ChatResponseChunk {
	return gw.ChatResponseChunk{ID: id, Choices: []gw.ChoiceDelta{{Delta: gw.Message{Role: gw.RoleAssistant, ToolCalls: []gw.ToolCall{{Index: index, ID: id, Type: "function", Function: gw.ToolCallFunction{Name: "f", Arguments: args}}}}, FinishReason: finish}}}
}
func TestReviewFailedAttemptToolsNeverEscape(t *testing.T) {
	for _, mode := range []string{"complete", "indexed complete", "partial", "text before failure", "limit"} {
		t.Run(mode, func(t *testing.T) {
			primary := &scriptedModel{name: "pp", model: "m-primary"}
			fallback := &scriptedModel{name: "pb", model: "m-b"}
			s := attemptServer(t, newAttemptSink(), []validator.FallbackFactorConfig{{Name: "fallback", Priority: 1, Strategy: "priority", Models: models("m-b")}}, primary, fallback)
			idx := 0
			c := reviewCall(nil, "failed", "{}", "tool_calls")
			if mode == "indexed complete" {
				c = reviewCall(&idx, "failed", "{}", "tool_calls")
			}
			if mode == "partial" {
				c = reviewCall(&idx, "failed", `{"x":`, "")
			}
			if mode == "limit" {
				c = reviewCall(nil, "failed", strings.Repeat("x", gw.MaxToolArgumentBytes+1), "")
			}
			a := &reviewedStreamProvider{scriptedModel: primary, chunks: []gw.ChatResponseChunk{c}, tailErr: errors.New("upstream 503")}
			if mode == "text before failure" {
				a.chunks = append([]gw.ChatResponseChunk{{Choices: []gw.ChoiceDelta{{Delta: gw.NewMessage(gw.RoleAssistant, gw.Text("A"))}}}}, a.chunks...)
			}
			bChunk := gw.ChatResponseChunk{Choices: []gw.ChoiceDelta{{Delta: gw.NewMessage(gw.RoleAssistant, gw.Text("B")), FinishReason: "stop"}}}
			if mode == "partial" {
				bChunk = reviewCall(&idx, "served", "{}", "tool_calls")
			}
			b := &reviewedStreamProvider{scriptedModel: fallback, chunks: []gw.ChatResponseChunk{bChunk}}
			s.defaultBundle.Load().reg.Register(a)
			s.defaultBundle.Load().reg.Register(b)
			var calls []*pb.ToolCall
			var text strings.Builder
			finish := ""
			err := processChatCompletion(contextkeys.WithTenantID(context.Background(), "tenant-1"), s, chatReq("m-primary", true), func(c *pb.ChatCompletionResponse) error {
				for _, d := range c.Choices {
					for _, p := range d.Message.Content {
						text.WriteString(p.GetText())
					}
					if len(d.Message.ToolCalls) > 0 && !b.returned {
						t.Fatal("uncommitted call escaped")
					}
					calls = append(calls, d.Message.ToolCalls...)
					if d.FinishReason != "" {
						finish = d.FinishReason
					}
				}
				return nil
			})
			if mode == "text before failure" {
				// Text reached the client, so the request ends with the
				// error: no fallback answer is appended to the partial one.
				if err == nil || len(calls) != 0 || text.String() != "A" || b.returned {
					t.Fatal(err, calls, text.String(), b.returned)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "partial" {
				if len(calls) != 1 || calls[0].Id != "served" || finish != "tool_calls" {
					t.Fatal(calls, finish)
				}
			} else {
				if len(calls) != 0 || text.String() != "B" || finish != "stop" {
					t.Fatal(calls, text.String(), finish)
				}
			}
		})
	}
}
func TestReviewToolLimitFollowsHandlerErrorPath(t *testing.T) {
	primary := &scriptedModel{name: "pp", model: "m-primary"}
	s := attemptServer(t, newAttemptSink(), nil, primary)
	s.defaultBundle.Load().reg.Register(&reviewedStreamProvider{scriptedModel: primary, chunks: []gw.ChatResponseChunk{reviewCall(nil, "a", strings.Repeat("x", gw.MaxToolArgumentBytes+1), "")}})
	err := processChatCompletion(contextkeys.WithTenantID(context.Background(), "tenant-1"), s, chatReq("m-primary", true), func(*pb.ChatCompletionResponse) error { t.Fatal("over-limit output escaped"); return nil })
	if err == nil || !strings.Contains(err.Error(), "4 MiB") {
		t.Fatal(err)
	}
}
func TestReviewMultiChoiceChunkConversion(t *testing.T) {
	for _, tools := range []bool{false, true} {
		var stream gw.ToolStream
		c := gw.ChatResponseChunk{Choices: []gw.ChoiceDelta{{Index: 0, Delta: gw.NewMessage(gw.RoleAssistant, gw.Text("zero"))}, {Index: 1, Delta: gw.NewMessage(gw.RoleAssistant, gw.Text("one"))}}}
		if tools {
			idx := 0
			c.Choices[1].Delta.ToolCalls = []gw.ToolCall{{Index: &idx, ID: "a", Function: gw.ToolCallFunction{Name: "f", Arguments: "{}"}}}
		}
		next, err := stream.Push(c)
		if err != nil {
			t.Fatal(err)
		}
		out := toProtoResponseFromChunk(next)
		if len(out.Choices) != 2 || out.Choices[0].Index != 0 || out.Choices[1].Index != 1 || out.Choices[0].Message.Content[0].GetText() != "zero" || out.Choices[1].Message.Content[0].GetText() != "one" {
			t.Fatal(out)
		}
		next, err = stream.Push(gw.ChatResponseChunk{Choices: []gw.ChoiceDelta{{Index: 1, FinishReason: "stop"}}})
		if err != nil {
			t.Fatal(err)
		}
		out = toProtoResponseFromChunk(next)
		if out.Choices[0].Index != 1 {
			t.Fatal(out)
		}
		if tools {
			tail, err := stream.Commit()
			if err != nil {
				t.Fatal(err)
			}
			out = toProtoResponseFromChunk(tail)
			if len(out.Choices) != 1 || out.Choices[0].Index != 1 || len(out.Choices[0].Message.ToolCalls) != 1 {
				t.Fatal(out)
			}
		}
	}
}

func TestReviewProtoWireMultiChoiceSSE(t *testing.T) {
	for _, withTools := range []bool{false, true} {
		w := httptest.NewRecorder()
		handler := httpmw.WrapWithSSENegotiationWith(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			err := gw.StreamToolAttempt(func(on func(gw.ChatResponseChunk) error) error {
				c := gw.ChatResponseChunk{ID: "c", Choices: []gw.ChoiceDelta{{Index: 1, Delta: gw.NewMessage(gw.RoleAssistant, gw.Text("one"))}, {Index: 0, Delta: gw.NewMessage(gw.RoleAssistant, gw.Text("zero"))}}}
				if withTools {
					idx := 0
					c.Choices[0].Delta.ToolCalls = []gw.ToolCall{{Index: &idx, ID: "a", Function: gw.ToolCallFunction{Name: "f", Arguments: "{}"}}}
				}
				if err := on(c); err != nil {
					return err
				}
				return on(gw.ChatResponseChunk{ID: "c", Choices: []gw.ChoiceDelta{{Index: 1, FinishReason: "stop"}, {Index: 0, FinishReason: "stop"}}})
			}, func(c gw.ChatResponseChunk) error {
				body, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(toProtoResponseFromChunk(c))
				if err != nil {
					return err
				}
				_, err = fmt.Fprintf(rw, "{\"result\":%s}\n", body)
				return err
			})
			if err != nil {
				t.Error(err)
			}
		}), func(*http.Request) string { return "openai" })
		handler.ServeHTTP(w, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"stream":true}`)))
		texts := map[int]string{}
		finishes := map[int]string{}
		calls := map[int]int{}
		for _, line := range strings.Split(w.Body.String(), "\n") {
			if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
				continue
			}
			var frame struct {
				Choices []struct {
					Index int `json:"index"`
					Delta struct {
						Content string `json:"content"`
						Calls   []any  `json:"tool_calls"`
					} `json:"delta"`
					Finish string `json:"finish_reason"`
				} `json:"choices"`
			}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame); err != nil {
				t.Fatal(err)
			}
			for _, c := range frame.Choices {
				texts[c.Index] += c.Delta.Content
				calls[c.Index] += len(c.Delta.Calls)
				if c.Finish != "" {
					finishes[c.Index] = c.Finish
				}
			}
		}
		if texts[0] != "zero" || texts[1] != "one" || finishes[0] != "stop" || calls[0] != 0 {
			t.Fatal(w.Body.String())
		}
		if withTools {
			if calls[1] != 1 || finishes[1] != "tool_calls" {
				t.Fatal(w.Body.String())
			}
		} else if finishes[1] != "stop" || calls[1] != 0 {
			t.Fatal(w.Body.String())
		}
	}
}

func TestReviewSingleChoiceRepeatedDeltas(t *testing.T) {
	out := toProtoResponseFromChunk(gw.ChatResponseChunk{Choices: []gw.ChoiceDelta{{Index: 0, Delta: gw.NewMessage(gw.RoleAssistant, gw.Text("hello"))}, {Index: 0, Delta: gw.NewMessage(gw.RoleAssistant, gw.Text(" world")), FinishReason: "stop"}}})
	if len(out.Choices) != 1 || out.Choices[0].Index != 0 || out.Choices[0].FinishReason != "stop" || len(out.Choices[0].Message.Content) != 2 || out.Choices[0].Message.Content[0].GetText() != "hello" || out.Choices[0].Message.Content[1].GetText() != " world" {
		t.Fatal(out)
	}
}
