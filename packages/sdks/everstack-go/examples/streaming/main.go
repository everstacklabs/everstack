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

	stream, err := client.Chat.Completions.CreateStream(ctx, &everstack.ChatCompletionParams{
		Model: "@anthropic/claude-3-5-sonnet-20241022",
		Messages: []everstack.Message{
			{Role: "user", Content: "Tell me a short joke"},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer stream.Close()

	for stream.Next() {
		chunk := stream.Current()
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != nil {
			fmt.Print(*chunk.Choices[0].Delta.Content)
		}
	}
	if err := stream.Err(); err != nil {
		log.Fatal(err)
	}
	fmt.Println()
}
