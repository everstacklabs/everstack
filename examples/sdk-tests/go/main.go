// Smoke tests for the Everstack Go SDK against a live gateway.
//
// Usage:
//
//	cp ../.env.example ../.env   # fill in URL + key (shared with the other SDKs)
//	go run .
//
// Reads EVERSTACK_GATEWAY_URL, EVERSTACK_API_KEY, and optional EVERSTACK_TEST_MODEL
// from the environment (or a shared ../.env / local ./.env).
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	everstack "github.com/everstacklabs/everstack-go"
)

var (
	passed []string
	failed []string
)

func run(name string, fn func() error) {
	fmt.Printf("  %s ... ", name)
	if err := fn(); err != nil {
		fmt.Printf("❌ %v\n", err)
		failed = append(failed, name)
	} else {
		fmt.Println("✅")
		passed = append(passed, name)
	}
}

func intPtr(i int) *int { return &i }

// loadDotEnv reads simple KEY=VALUE lines from the given file and sets any env
// var that is not already present. Shell-exported values always win.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
}

func requireEnv(name string) string {
	v := os.Getenv(name)
	if v == "" {
		fmt.Fprintf(os.Stderr, "Missing required env var: %s\nCopy ../.env.example to ../.env and fill it in.\n", name)
		os.Exit(1)
	}
	return v
}

func main() {
	loadDotEnv("../.env")
	loadDotEnv(".env")

	apiKey := requireEnv("EVERSTACK_API_KEY")
	baseURL := requireEnv("EVERSTACK_GATEWAY_URL")
	modelOverride := os.Getenv("EVERSTACK_TEST_MODEL")

	fmt.Printf("\n🧪 Everstack Go SDK smoke tests\n   %s\n\n", baseURL)

	client := everstack.NewClient(apiKey, everstack.WithBaseURL(baseURL))
	ctx := context.Background()

	var models []everstack.ModelInfo
	run("Models.List", func() error {
		res, err := client.Models.List(ctx)
		if err != nil {
			return err
		}
		models = res.Data
		fmt.Printf("(%d models) ", len(models))
		return nil
	})

	chatModel := modelOverride
	if chatModel == "" {
		for i := range models {
			m := &models[i]
			if m.OwnedBy == "openai" && !strings.Contains(m.ID, "embedding") {
				chatModel = m.ID
				break
			}
		}
		if chatModel == "" {
			for i := range models {
				if models[i].OwnedBy == "anthropic" {
					chatModel = models[i].ID
					break
				}
			}
		}
		if chatModel == "" && len(models) > 0 {
			chatModel = models[0].ID
		}
	}
	if chatModel == "" {
		fmt.Println("\n❌ No models available. Set EVERSTACK_TEST_MODEL to test a specific model.")
		os.Exit(1)
	}
	fmt.Printf("   Using chat model: %s\n\n", chatModel)

	run("Chat.Completions.Create (non-streaming)", func() error {
		res, err := client.Chat.Completions.Create(ctx, &everstack.ChatCompletionParams{
			Model:     chatModel,
			Messages:  []everstack.Message{{Role: "user", Content: "What is 2+2? One word."}},
			MaxTokens: intPtr(10),
		})
		if err != nil {
			return err
		}
		if len(res.Choices) == 0 || res.Choices[0].Message.Content == nil {
			return fmt.Errorf("no content in response")
		}
		fmt.Printf("→ %q ", strings.TrimSpace(*res.Choices[0].Message.Content))
		return nil
	})

	run("Chat.Completions.CreateStream (streaming)", func() error {
		stream, err := client.Chat.Completions.CreateStream(ctx, &everstack.ChatCompletionParams{
			Model:     chatModel,
			Messages:  []everstack.Message{{Role: "user", Content: "Say hello in 3 words"}},
			MaxTokens: intPtr(20),
		})
		if err != nil {
			return err
		}
		defer stream.Close()
		var text strings.Builder
		for stream.Next() {
			chunk := stream.Current()
			if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != nil {
				text.WriteString(*chunk.Choices[0].Delta.Content)
			}
		}
		if err := stream.Err(); err != nil {
			return err
		}
		if text.Len() == 0 {
			return fmt.Errorf("no content from stream")
		}
		fmt.Printf("→ %q ", strings.TrimSpace(text.String()))
		return nil
	})

	fmt.Println("\n========== RESULTS ==========")
	for _, n := range passed {
		fmt.Printf("  ✅ PASS  %s\n", n)
	}
	for _, n := range failed {
		fmt.Printf("  ❌ FAIL  %s\n", n)
	}
	fmt.Println("=============================")
	fmt.Printf("%d/%d passed\n", len(passed), len(passed)+len(failed))
	if len(failed) > 0 {
		os.Exit(1)
	}
}
