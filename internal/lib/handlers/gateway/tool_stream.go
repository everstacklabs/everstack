package gateway

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	MaxToolArgumentBytes = 4 << 20
	MaxToolCalls         = 256
)

// ToolStream holds calls for one attempt until its stream returns successfully.
// OpenAI-compatible upstreams must send argument DELTAS, not snapshots. Calls
// without an index are complete (Anthropic and Gemini), each emitted once by
// its provider. No snapshot de-duplication is performed. Text is not held.
type ToolStream struct {
	choices                  map[int]*toolChoiceState
	metadata                 ChatResponseChunk
	argumentBytes, callCount int
	err                      error
}
type toolChoiceState struct {
	indexed  map[int]*streamToolCall
	complete []*streamToolCall
	finish   string
}
type streamToolCall struct {
	id              string
	name, arguments strings.Builder
}

// Push forwards text immediately, but withholds calls and their finish reasons.
func (s *ToolStream) Push(chunk ChatResponseChunk) (ChatResponseChunk, error) {
	if s.err != nil {
		return ChatResponseChunk{}, s.err
	}
	if s.choices == nil {
		s.choices = map[int]*toolChoiceState{}
	}
	if chunk.ID != "" {
		s.metadata.ID = chunk.ID
	}
	if chunk.Model != "" {
		s.metadata.Model = chunk.Model
	}
	if !chunk.Created.IsZero() {
		s.metadata.Created = chunk.Created
	}
	for i := range chunk.Choices {
		d := &chunk.Choices[i]
		state := s.choices[d.Index]
		if state == nil {
			state = &toolChoiceState{indexed: map[int]*streamToolCall{}}
			s.choices[d.Index] = state
		}
		for _, tc := range d.Delta.ToolCalls {
			if len(tc.Function.Arguments) > MaxToolArgumentBytes-s.argumentBytes {
				s.err = fmt.Errorf("tool-call arguments exceed 4 MiB response limit")
				return ChatResponseChunk{}, s.err
			}
			s.argumentBytes += len(tc.Function.Arguments)
			var call *streamToolCall
			if tc.Index != nil {
				call = state.indexed[*tc.Index]
			}
			if call == nil {
				if s.callCount == MaxToolCalls {
					s.err = fmt.Errorf("tool-call count exceeds 256 response limit")
					return ChatResponseChunk{}, s.err
				}
				s.callCount++
				call = &streamToolCall{}
				if tc.Index == nil {
					state.complete = append(state.complete, call)
				} else {
					state.indexed[*tc.Index] = call
				}
			}
			if tc.ID != "" {
				call.id = tc.ID
			}
			call.name.WriteString(tc.Function.Name)
			call.arguments.WriteString(tc.Function.Arguments)
		}
		d.Delta.ToolCalls = nil
		if len(state.indexed)+len(state.complete) > 0 || state.finish != "" || d.FinishReason == "tool_calls" || d.FinishReason == "function_call" {
			if d.FinishReason != "" {
				state.finish = d.FinishReason
			}
			d.FinishReason = ""
		}
	}
	return chunk, nil
}

// Commit is called only after a successful attempt. At EOF without a finish
// chunk, incomplete JSON calls are dropped; empty arguments mean an empty object.
func (s *ToolStream) Commit() (ChatResponseChunk, error) {
	if s.err != nil {
		return ChatResponseChunk{}, s.err
	}
	out := s.metadata
	indexes := make([]int, 0, len(s.choices))
	for index := range s.choices {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		state := s.choices[index]
		if len(state.complete)+len(state.indexed) == 0 && state.finish == "" {
			continue
		}
		calls := append([]*streamToolCall(nil), state.complete...)
		upstream := make([]int, 0, len(state.indexed))
		for n := range state.indexed {
			upstream = append(upstream, n)
		}
		sort.Ints(upstream)
		for _, n := range upstream {
			calls = append(calls, state.indexed[n])
		}
		d := ChoiceDelta{Index: index, Delta: Message{Role: RoleAssistant}, FinishReason: state.finish}
		for _, call := range calls {
			args := call.arguments.String()
			if strings.TrimSpace(args) == "" {
				args = "{}"
			}
			if state.finish == "" && !json.Valid([]byte(args)) {
				continue
			}
			d.Delta.ToolCalls = append(d.Delta.ToolCalls, ToolCall{ID: call.id, Type: "function", Function: ToolCallFunction{Name: call.name.String(), Arguments: args}})
		}
		if d.FinishReason == "" {
			d.FinishReason = "stop"
		}
		if len(d.Delta.ToolCalls) > 0 && (d.FinishReason == "stop" || d.FinishReason == "function_call") {
			d.FinishReason = "tool_calls"
		}
		out.Choices = append(out.Choices, d)
	}
	return out, nil
}

// StreamToolAttempt commits tool output only after run returns nil. A callback
// failure remains terminal for this attempt even if the provider swallows it.
func StreamToolAttempt(run func(func(ChatResponseChunk) error) error, send func(ChatResponseChunk) error) error {
	var stream ToolStream
	var callbackErr error
	err := run(func(c ChatResponseChunk) error {
		if callbackErr != nil {
			return callbackErr
		}
		c, callbackErr = stream.Push(c)
		if callbackErr == nil {
			callbackErr = send(c)
		}
		return callbackErr
	})
	if callbackErr != nil {
		return callbackErr
	}
	if err != nil {
		return err
	}
	tail, err := stream.Commit()
	if err != nil {
		return err
	}
	if len(tail.Choices) > 0 {
		return send(tail)
	}
	return nil
}
