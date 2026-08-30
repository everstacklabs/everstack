package openai

import (
	"encoding/json"
	"net/http"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// OpenAIChatCompletionsHandler exposes an OpenAI-compatible endpoint that accepts
// OpenAI chat completions requests and returns OpenAI-shaped responses while routing
// through the Everstack gateway provider stack.
func OpenAIChatCompletionsHandler(router *gw.Router, fallbackModel string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string      `json:"role"`
				Content interface{} `json:"content"` // string or []parts
			} `json:"messages"`
			Temperature float64  `json:"temperature,omitempty"`
			TopP        float64  `json:"top_p,omitempty"`
			MaxTokens   int      `json:"max_tokens,omitempty"`
			Stop        []string `json:"stop,omitempty"`
			Stream      bool     `json:"stream,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request_error", "message": "invalid JSON"})
			return
		}

		model := in.Model
		if model == "" {
			model = fallbackModel
		}
		req := gw.ChatCompletionRequest{
			Model:    model,
			Sampling: gw.SamplingParams{Temperature: in.Temperature, TopP: in.TopP, MaxTokens: in.MaxTokens, Stop: in.Stop},
			Stream:   in.Stream,
		}

		// Convert OpenAI message content (string or array) to normalized parts
		for _, m := range in.Messages {
			var parts []gw.ContentPart
			switch v := m.Content.(type) {
			case string:
				parts = append(parts, gw.Text(v))
			case []interface{}:
				for _, pv := range v {
					if pm, ok := pv.(map[string]interface{}); ok {
						if t, ok := pm["type"].(string); ok && t == "text" {
							if txt, ok := pm["text"].(string); ok {
								parts = append(parts, gw.Text(txt))
							}
						}
					}
				}
			}
			req.Messages = append(req.Messages, gw.NewMessage(gw.MessageRole(m.Role), parts...))
		}

		if req.Stream {
			// Return OpenAI SSE chunks
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			enc := json.NewEncoder(w)
			sender := func(chunk gw.ChatResponseChunk) error {
				out := map[string]interface{}{
					"id":      chunk.ID,
					"object":  "chat.completion.chunk",
					"created": chunk.Created.Unix(),
					"model":   chunk.Model,
				}
				var deltas []map[string]interface{}
				for _, ch := range chunk.Choices {
					// Aggregate text parts in delta to a single string
					text := ""
					for _, p := range ch.Delta.Content {
						if p.Type == "text" && p.Text != nil {
							text += *p.Text
						}
					}
					deltas = append(deltas, map[string]interface{}{
						"index": ch.Index,
						"delta": map[string]interface{}{
							"content": text,
						},
						"finish_reason": ch.FinishReason,
					})
				}
				out["choices"] = deltas
				if _, err := w.Write([]byte("data: ")); err != nil {
					return err
				}
				if err := enc.Encode(out); err != nil {
					return err
				}
				if _, err := w.Write([]byte("\n")); err != nil {
					return err
				}
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				return nil
			}
			_ = gw.HandleChatStream(r.Context(), router, req, sender)
			// Write final done marker
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			return
		}

		// Unary path -> OpenAI-shaped JSON response
		resp, err := gw.HandleChat(r.Context(), router, req)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "provider_error", "message": err.Error()})
			return
		}
		out := map[string]interface{}{
			"id":      resp.ID,
			"object":  "chat.completion",
			"created": resp.Created.Unix(),
			"model":   resp.Model,
		}
		var choices []map[string]interface{}
		for _, c := range resp.Choices {
			text := ""
			for _, p := range c.Message.Content {
				if p.Type == "text" && p.Text != nil {
					text += *p.Text
				}
			}
			choices = append(choices, map[string]interface{}{
				"index": c.Index,
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": text,
				},
				"finish_reason": c.FinishReason,
			})
		}
		out["choices"] = choices
		out["usage"] = map[string]int{
			"prompt_tokens":     resp.Usage.PromptTokens,
			"completion_tokens": resp.Usage.CompletionTokens,
			"total_tokens":      resp.Usage.TotalTokens,
		}
		writeJSON(w, http.StatusOK, out)
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
