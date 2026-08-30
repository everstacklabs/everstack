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

	// List agents
	agents, err := client.Agents.List(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Agents: %v\n", agents)

	// Create a session
	session, err := client.Agents.Sessions.Create(ctx, map[string]any{
		"agent_id": "agent_abc123",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Session: %v\n", session["id"])

	// Run a turn
	result, err := client.Agents.Sessions.RunTurn(ctx, session["id"].(string), map[string]any{
		"message": "Hello, agent!",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Result: %v\n", result)
}
