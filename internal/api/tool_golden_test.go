package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	httpmw "github.com/everstacklabs/everstack/internal/api/http/middleware"
)

const textGoldenFrames = `{"result":{"id":"chat_fixed","created":"1700000000","model":"fixed","choices":[{"message":{"content":[{"text":"hello"}]}}]}}
{"result":{"choices":[{"message":{"content":[{"text":" world"}]},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}}
`

func TestTextOnlyGolden(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "text_unary.golden"
		body := `{"stream":false}`
		if stream {
			name = "text_stream.golden"
			body = `{"stream":true}`
		}
		t.Run(name, func(t *testing.T) {
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(textGoldenFrames))
			})
			var handler http.Handler = inner
			if stream {
				handler = httpmw.WrapWithSSENegotiationWith(inner, func(*http.Request) string { return "openai" })
			}
			r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
			w := httptest.NewRecorder()
			serveOpenAICompat(w, r, handler)
			path := "testdata/" + name
			if os.Getenv("CAPTURE_TEXT_GOLDEN") == "1" {
				if err := os.WriteFile(path, w.Body.Bytes(), 0644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(want) != w.Body.String() {
				t.Fatalf("text wire output changed\nwant %s\ngot %s", want, w.Body.String())
			}
		})
	}
}
