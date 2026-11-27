package gateway

import (
	"errors"
	"strings"
	"testing"
)

func toolDelta(index *int, id, args string) ChatResponseChunk {
	return ChatResponseChunk{ID: "c", Choices: []ChoiceDelta{{Delta: Message{Role: RoleAssistant, ToolCalls: []ToolCall{{Index: index, ID: id, Function: ToolCallFunction{Name: "f", Arguments: args}}}}}}}
}
func TestToolStreamCommit(t *testing.T) {
	for _, n := range []int{1, 2} {
		var s ToolStream
		for step, frag := range []string{`{"x":`, `1}`} {
			for i := n - 1; i >= 0; i-- {
				idx := i
				c := toolDelta(&idx, string(rune('a'+i)), frag)
				if step > 0 {
					c.Choices[0].Delta.ToolCalls[0].Function.Name = ""
				}
				c.Choices[0].FinishReason = "tool_calls"
				out, err := s.Push(c)
				if err != nil {
					t.Fatal(err)
				}
				if len(out.Choices[0].Delta.ToolCalls) > 0 || out.Choices[0].FinishReason != "" {
					t.Fatal("uncommitted calls/finish escaped", out)
				}
			}
		}
		out, err := s.Commit()
		if err != nil {
			t.Fatal(err)
		}
		calls := out.Choices[0].Delta.ToolCalls
		if len(calls) != n {
			t.Fatal(calls)
		}
		for i, tc := range calls {
			if tc.ID != string(rune('a'+i)) || tc.Function.Name != "f" || tc.Function.Arguments != `{"x":1}` || tc.Index != nil {
				t.Fatal(tc)
			}
		}
	}
}
func TestToolStreamSuccessfulEOF(t *testing.T) {
	for _, tc := range []struct {
		args, finish, want string
		count              int
	}{{`{"x":1}`, "", "tool_calls", 1}, {"", "", "tool_calls", 1}, {" ", "", "tool_calls", 1}, {`{"x":`, "", "stop", 0}, {`bad`, "length", "length", 1}, {`bad`, "tool_calls", "tool_calls", 1}} {
		var s ToolStream
		idx := 0
		chunk := toolDelta(&idx, "a", tc.args)
		chunk.Choices[0].FinishReason = tc.finish
		if _, err := s.Push(chunk); err != nil {
			t.Fatal(err)
		}
		out, err := s.Commit()
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Choices) != 1 || len(out.Choices[0].Delta.ToolCalls) != tc.count || out.Choices[0].FinishReason != tc.want {
			t.Fatalf("%+v: %+v", tc, out)
		}
		if strings.TrimSpace(tc.args) == "" && out.Choices[0].Delta.ToolCalls[0].Function.Arguments != "{}" {
			t.Fatal(out)
		}
	}
}
func TestToolAttemptDiscard(t *testing.T) {
	for _, fragment := range []bool{false, true} {
		var emitted []ChatResponseChunk
		send := func(c ChatResponseChunk) error { emitted = append(emitted, c); return nil }
		idx := 0
		var index *int
		args := "{}"
		if fragment {
			index = &idx
			args = `{"x":`
		}
		err := StreamToolAttempt(func(on func(ChatResponseChunk) error) error {
			c := toolDelta(index, "failed", args)
			c.Choices[0].FinishReason = "tool_calls"
			if err := on(c); err != nil {
				return err
			}
			return errors.New("upstream 503")
		}, send)
		if err == nil {
			t.Fatal("attempt succeeded")
		}
		if err := StreamToolAttempt(func(on func(ChatResponseChunk) error) error {
			c := toolDelta(nil, "served", "{}")
			if !fragment {
				c.Choices[0].Delta = NewMessage(RoleAssistant, Text("B"))
			}
			c.Choices[0].FinishReason = "stop"
			return on(c)
		}, send); err != nil {
			t.Fatal(err)
		}
		calls := []ToolCall{}
		finish := ""
		for _, c := range emitted {
			for _, d := range c.Choices {
				calls = append(calls, d.Delta.ToolCalls...)
				if d.FinishReason != "" {
					finish = d.FinishReason
				}
			}
		}
		if fragment {
			if len(calls) != 1 || calls[0].ID != "served" || finish != "tool_calls" {
				t.Fatal(calls, finish)
			}
		} else {
			if len(calls) != 0 || finish != "stop" {
				t.Fatal(calls, finish)
			}
		}
	}
}
func TestToolStreamBounds(t *testing.T) {
	for _, indexed := range []bool{false, true} {
		var s ToolStream
		idx := 0
		var index *int
		if indexed {
			index = &idx
		}
		// The total argument budget spans calls and choices, not just one builder.
		c := toolDelta(index, "a", strings.Repeat("x", MaxToolArgumentBytes))
		if _, err := s.Push(c); err != nil {
			t.Fatal(err)
		}
		c = toolDelta(index, "a", "x")
		c.Choices[0].Index = 1
		if _, err := s.Push(c); err == nil || !strings.Contains(err.Error(), "4 MiB") {
			t.Fatal(err)
		}
		if _, err := s.Commit(); err == nil {
			t.Fatal("overflow committed")
		}
	}
	var s ToolStream
	for i := 0; i < MaxToolCalls; i++ {
		c := toolDelta(nil, "same-id", "{}")
		c.Choices[0].Index = i % 2
		if _, err := s.Push(c); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Push(toolDelta(nil, "excess", "{}")); err == nil || !strings.Contains(err.Error(), "256") {
		t.Fatal(err)
	}
}
func TestToolAttemptPropagatesCallbackFailure(t *testing.T) {
	var outputs int
	err := StreamToolAttempt(func(on func(ChatResponseChunk) error) error {
		_ = on(toolDelta(nil, "a", strings.Repeat("x", MaxToolArgumentBytes+1)))
		return nil
	}, func(ChatResponseChunk) error { outputs++; return nil })
	if err == nil || outputs != 0 {
		t.Fatal(err, outputs)
	}
}
func TestToolStreamDoesNotDeduplicateCompleteCalls(t *testing.T) {
	var s ToolStream
	for i := 0; i < 2; i++ {
		if _, err := s.Push(toolDelta(nil, "same-id", "{}")); err != nil {
			t.Fatal(err)
		}
	}
	out, err := s.Commit()
	if err != nil || len(out.Choices[0].Delta.ToolCalls) != 2 {
		t.Fatal(out, err)
	}
}

func TestToolAttemptDiscardsUncommittedToolFinish(t *testing.T) {
	var sent []ChatResponseChunk
	err := StreamToolAttempt(func(on func(ChatResponseChunk) error) error {
		if err := on(ChatResponseChunk{Choices: []ChoiceDelta{{FinishReason: "tool_calls"}}}); err != nil {
			return err
		}
		return errors.New("upstream failed after finish")
	}, func(c ChatResponseChunk) error { sent = append(sent, c); return nil })
	if err == nil {
		t.Fatal("failed attempt succeeded")
	}
	for _, c := range sent {
		for _, d := range c.Choices {
			if d.FinishReason != "" {
				t.Fatal("tool finish escaped failed attempt", d)
			}
		}
	}
}
