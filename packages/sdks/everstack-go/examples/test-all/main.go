// Comprehensive SDK test — exercises all resource types.
//
// Usage:
//
//	EVERSTACK_API_KEY=pk_... go run examples/test-all/main.go
//	EVERSTACK_API_KEY=pk_... EVERSTACK_GATEWAY_URL=http://localhost:8089 go run examples/test-all/main.go
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	everstack "github.com/everstacklabs/everstack-go"
)

var (
	passed  []string
	failed  []string
	skipped []string
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

func skip(name, reason string) {
	fmt.Printf("  %s ... ⏭️  %s\n", name, reason)
	skipped = append(skipped, name)
}

func main() {
	apiKey := os.Getenv("EVERSTACK_API_KEY")
	baseURL := os.Getenv("EVERSTACK_GATEWAY_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8089"
	}

	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "EVERSTACK_API_KEY is required")
		os.Exit(1)
	}

	fmt.Printf("\n🧪 Everstack Go SDK — Full Test Suite\n")
	fmt.Printf("   Gateway: %s\n\n", baseURL)

	client := everstack.NewClient(apiKey, everstack.WithBaseURL(baseURL))
	ctx := context.Background()

	// ── Models ──────────────────────────────────────────────
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

	// Find test models
	var chatModel *everstack.ModelInfo
	var embeddingModel *everstack.ModelInfo

	for i := range models {
		m := &models[i]
		if m.OwnedBy == "openai" && !strings.Contains(m.ID, "embedding") && chatModel == nil {
			chatModel = m
		}
		if strings.Contains(m.ID, "embedding") && embeddingModel == nil {
			embeddingModel = m
		}
	}
	if chatModel == nil {
		for i := range models {
			if models[i].OwnedBy == "anthropic" {
				chatModel = &models[i]
				break
			}
		}
	}
	if chatModel == nil && len(models) > 0 {
		chatModel = &models[0]
	}

	if chatModel == nil {
		fmt.Println("\n❌ No models available. Is the gateway running?")
		os.Exit(1)
	}

	fmt.Printf("\n   Using chat model: %s\n", chatModel.ID)
	if embeddingModel != nil {
		fmt.Printf("   Using embedding model: %s\n", embeddingModel.ID)
	}
	fmt.Println()

	// ── Chat Completions ────────────────────────────────────
	run("Chat.Completions.Create (non-streaming)", func() error {
		res, err := client.Chat.Completions.Create(ctx, &everstack.ChatCompletionParams{
			Model:    chatModel.ID,
			Messages: []everstack.Message{{Role: "user", Content: "What is 2+2? One word."}},
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

	run("Chat.Completions.CreateStream", func() error {
		stream, err := client.Chat.Completions.CreateStream(ctx, &everstack.ChatCompletionParams{
			Model:    chatModel.ID,
			Messages: []everstack.Message{{Role: "user", Content: "Say hello in 3 words"}},
			MaxTokens: intPtr(20),
		})
		if err != nil {
			return err
		}
		defer stream.Close()

		var text string
		for stream.Next() {
			chunk := stream.Current()
			if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != nil {
				text += *chunk.Choices[0].Delta.Content
			}
		}
		if err := stream.Err(); err != nil {
			return err
		}
		if text == "" {
			return fmt.Errorf("no content from stream")
		}
		fmt.Printf("→ %q ", strings.TrimSpace(text))
		return nil
	})

	// ── Embeddings ──────────────────────────────────────────
	if embeddingModel != nil {
		run("Embeddings.Create", func() error {
			res, err := client.Embeddings.Create(ctx, &everstack.EmbeddingsParams{
				Model: embeddingModel.ID,
				Input: "Hello, world!",
			})
			if err != nil {
				return err
			}
			if len(res.Data) == 0 {
				return fmt.Errorf("no embeddings returned")
			}
			fmt.Printf("→ %d dims ", len(res.Data[0].Embedding))
			return nil
		})
	} else {
		skip("Embeddings.Create", "no embedding model available")
	}

	// ── Responses API ───────────────────────────────────────
	run("Responses.Create", func() error {
		res, err := client.Responses.Create(ctx, &everstack.ResponseCreateParams{
			Model: chatModel.ID,
			Input: []any{
				map[string]any{"role": "user", "content": "What is 1+1?"},
			},
		})
		if err != nil {
			return err
		}
		fmt.Printf("→ id=%s status=%s ", res.ID, res.Status)
		return nil
	})

	run("Responses.List", func() error {
		limit := 5
		res, err := client.Responses.List(ctx, &everstack.ResponseListParams{Limit: &limit})
		if err != nil {
			return err
		}
		fmt.Printf("→ %d responses ", len(res.Data))
		return nil
	})

	// ── Agents ──────────────────────────────────────────────
	run("Agents.List", func() error {
		res, err := client.Agents.List(ctx)
		if err != nil {
			return err
		}
		agents, _ := res["agents"].([]any)
		fmt.Printf("→ %d agents ", len(agents))
		return nil
	})

	var testAgentID string
	var testSessionID string

	run("Agents.Create", func() error {
		res, err := client.Agents.Create(ctx, map[string]any{
			"name":          "sdk-test-agent",
			"description":   "Temporary agent created by SDK test suite",
			"model":         chatModel.ID,
			"system_prompt": "You are a helpful test assistant. Keep answers very short.",
			"max_turns":     5,
		})
		if err != nil {
			return err
		}
		agent, _ := res["agent"].(map[string]any)
		id, _ := agent["id"].(string)
		if id == "" {
			return fmt.Errorf("no agent ID returned")
		}
		testAgentID = id
		fmt.Printf("→ id=%s ", testAgentID)
		return nil
	})

	if testAgentID != "" {
		run("Agents.Sessions.Create", func() error {
			res, err := client.Agents.Sessions.Create(ctx, map[string]any{
				"agent_id": testAgentID,
			})
			if err != nil {
				return err
			}
			session, _ := res["session"].(map[string]any)
			id, _ := session["id"].(string)
			if id == "" {
				return fmt.Errorf("no session ID returned")
			}
			testSessionID = id
			fmt.Printf("→ id=%s ", testSessionID)
			return nil
		})
	} else {
		skip("Agents.Sessions.Create", "no agent created")
	}

	if testSessionID != "" {
		run("Agents.Sessions.RunTurn", func() error {
			res, err := client.Agents.Sessions.RunTurn(ctx, testSessionID, map[string]any{
				"user_input": "What is the capital of France? One word.",
			})
			if err != nil {
				return err
			}
			turn, _ := res["turn"].(map[string]any)
			text, _ := turn["assistant_message"].(string)
			if len(text) > 60 {
				text = text[:60]
			}
			fmt.Printf("→ %q ", strings.TrimSpace(text))
			return nil
		})

		run("Agents.Sessions.RunTurnStream", func() error {
			stream, err := client.Agents.Sessions.RunTurnStream(ctx, testSessionID, map[string]any{
				"user_input":       "What is 10 * 10? One word.",
				"enable_streaming": true,
			})
			if err != nil {
				return err
			}
			defer stream.Close()

			var text string
			for stream.Next() {
				event := stream.Current()
				if delta, ok := event["text_delta"].(string); ok {
					text += delta
				}
			}
			if err := stream.Err(); err != nil {
				return err
			}
			if len(text) > 60 {
				text = text[:60]
			}
			fmt.Printf("→ %q ", strings.TrimSpace(text))
			return nil
		})
	} else {
		skip("Agents.Sessions.RunTurn", "no session created")
		skip("Agents.Sessions.RunTurnStream", "no session created")
	}

	// Cleanup: delete the test agent
	if testAgentID != "" {
		run("Agents.Delete (cleanup)", func() error {
			err := client.Agents.Delete(ctx, testAgentID)
			if err != nil {
				return err
			}
			fmt.Printf("→ deleted %s ", testAgentID)
			return nil
		})
	}

	// ── Datasets ────────────────────────────────────────────
	run("Datasets.List", func() error {
		_, err := client.Datasets.List(ctx)
		if err != nil {
			return err
		}
		fmt.Print("→ listed ")
		return nil
	})

	// ── Evaluations ─────────────────────────────────────────
	run("Evaluations.Runs.List", func() error {
		_, err := client.Evaluations.Runs.List(ctx)
		if err != nil {
			return err
		}
		fmt.Print("→ listed ")
		return nil
	})

	// ── Observability ───────────────────────────────────────
	run("Observability.Metrics.GetDashboard", func() error {
		_, err := client.Observability.Metrics.GetDashboard(ctx, map[string]any{})
		if err != nil {
			return err
		}
		fmt.Print("→ ok ")
		return nil
	})

	// ── Summary ─────────────────────────────────────────────
	fmt.Printf("\n%s\n", strings.Repeat("─", 50))
	fmt.Printf("  ✅ Passed:  %d\n", len(passed))
	if len(skipped) > 0 {
		fmt.Printf("  ⏭️  Skipped: %d\n", len(skipped))
	}
	if len(failed) > 0 {
		fmt.Printf("  ❌ Failed:  %d\n", len(failed))
		for _, f := range failed {
			fmt.Printf("     - %s\n", f)
		}
		os.Exit(1)
	}
	fmt.Println()
}

func intPtr(n int) *int { return &n }
