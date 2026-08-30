package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run tools/protogen/method_registry.go <project-root>")
		os.Exit(1)
	}

	projectRoot := os.Args[1]
	outputFile := filepath.Join(projectRoot, "internal/api/method_registry.go")

	// Create the method registry content
	content := `package api

import (
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/everstack/v1"
	"google.golang.org/grpc"
)

// RegisterServices registers all gRPC services
func RegisterServices(s *grpc.Server, service *EverstackService) {
	everstackv1.RegisterEverstackServiceServer(s, service)
}

// Available methods for EverstackService:
// - Health
// - GetSystemInfo
`

	// Write the file
	err := os.WriteFile(outputFile, []byte(content), 0644)
	if err != nil {
		fmt.Printf("Error writing method registry: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Method registry generated: %s\n", outputFile)
}
