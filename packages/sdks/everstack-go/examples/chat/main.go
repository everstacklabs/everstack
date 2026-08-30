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

	// Simple chat completion
	resp, err := client.Chat.Completions.Create(ctx, &everstack.ChatCompletionParams{
		Model: "@openai/gpt-4o",
		Messages: []everstack.Message{
			{Role: "user", Content: "What is the capital of France?"},
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	if len(resp.Choices) > 0 && resp.Choices[0].Message.Content != nil {
		fmt.Println(*resp.Choices[0].Message.Content)
	}
}
