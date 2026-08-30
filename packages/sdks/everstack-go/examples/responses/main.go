package main

import (
	"context"
	"fmt"
	"log"

	everstack "github.com/everstacklabs/everstack-go"
)

func main() {
	client := everstack.NewClient("pk_...")

	ctx := context.Background()

	// Non-streaming response
	resp, err := client.Responses.Create(ctx, &everstack.ResponseCreateParams{
		Model: "@openai/gpt-4o",
		Input: []any{
			map[string]any{"role": "user", "content": "Summarize the latest AI news"},
		},
		Tools: []any{
			map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        "web_search",
					"description": "Search the web",
					"parameters": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"query": map[string]any{"type": "string"},
						},
					},
				},
			},
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Status: %s\n", resp.Status)
	for _, item := range resp.Output {
		fmt.Printf("  [%s] %v\n", item.Type, item.Content)
	}
}
