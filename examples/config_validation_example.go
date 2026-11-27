package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/everstacklabs/everstack/internal/api/grpc/config"
	configservice "github.com/everstacklabs/everstack/internal/config"
	"github.com/everstacklabs/everstack/internal/config/validator"
	configpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/config/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	// Example demonstrating the configuration validation status system

	fmt.Println("🎯 Configuration Validation Status Example")
	fmt.Println("==========================================")

	// 1. Create configuration service with schemas
	service := configservice.NewService()

	// Load schemas (in a real application, these would come from files)
	schemas := map[string][]byte{
		"config": []byte(`{
			"type": "object",
			"properties": {
				"grpc": {
					"type": "object",
					"properties": {
						"server": {
							"type": "object",
							"properties": {
								"port": {"type": "integer", "minimum": 1, "maximum": 65535},
								"host": {"type": "string"}
							},
							"required": ["port", "host"]
						}
					},
					"required": ["server"]
				}
			},
			"required": ["grpc"]
		}`),
	}

	if err := service.LoadSchemasFromData(schemas); err != nil {
		log.Fatalf("Failed to load schemas: %v", err)
	}

	// 2. Test validation with different configurations
	testConfigurations(service)

	// 3. Start gRPC server
	grpcServer := grpc.NewServer()
	configServer := config.NewServer(service)
	configpb.RegisterConfigServiceServer(grpcServer, configServer)

	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	fmt.Println("\n🚀 Starting gRPC server on :50051")
	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("Failed to serve: %v", err)
		}
	}()

	// 4. Start HTTP gateway
	ctx := context.Background()
	mux := runtime.NewServeMux()

	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	err = configpb.RegisterConfigServiceHandlerFromEndpoint(ctx, mux, "localhost:50051", opts)
	if err != nil {
		log.Fatalf("Failed to register gateway: %v", err)
	}

	fmt.Println("🌐 Starting HTTP gateway on :8080")
	fmt.Println("\n📋 Available endpoints:")
	fmt.Println("  POST http://localhost:8080/api/v1/config/validate")
	fmt.Println("  GET  http://localhost:8080/api/v1/config/validation-status")
	fmt.Println("  GET  http://localhost:8080/api/v1/config/validation-history")
	fmt.Println("  GET  http://localhost:8080/api/v1/config/schemas")

	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatalf("Failed to start HTTP server: %v", err)
	}
}

func testConfigurations(service *configservice.Service) {
	fmt.Println("\n📊 Testing Configuration Validation:")
	fmt.Println("=====================================")

	// Test 1: Valid configuration
	fmt.Println("\n✅ Test 1: Valid Configuration")
	validConfig := `
grpc:
  server:
    port: 8080
    host: "localhost"
`
	result, err := service.ValidateYAML(validConfig)
	if err != nil {
		log.Printf("Error: %v", err)
	} else {
		printValidationResult("Valid Config", result)
	}

	// Test 2: Invalid configuration (missing required field)
	fmt.Println("\n❌ Test 2: Invalid Configuration (missing host)")
	invalidConfig1 := `
grpc:
  server:
    port: 8080
`
	result, err = service.ValidateYAML(invalidConfig1)
	if err != nil {
		log.Printf("Error: %v", err)
	} else {
		printValidationResult("Missing Host", result)
	}

	// Test 3: Invalid configuration (port out of range)
	fmt.Println("\n❌ Test 3: Invalid Configuration (port out of range)")
	invalidConfig2 := `
grpc:
  server:
    port: 99999
    host: "localhost"
`
	result, err = service.ValidateYAML(invalidConfig2)
	if err != nil {
		log.Printf("Error: %v", err)
	} else {
		printValidationResult("Invalid Port", result)
	}

	// Show current validation status
	fmt.Println("\n📈 Current Validation Status:")
	fmt.Println("============================")
	status := service.GetValidationStatus()
	printValidationStatus(status)

	// Show validation history
	fmt.Println("\n📚 Validation History:")
	fmt.Println("======================")
	history := service.GetValidationHistory(10)
	printValidationHistory(history)
}

func printValidationResult(name string, result *validator.ValidationResult) {
	fmt.Printf("📝 %s:\n", name)
	fmt.Printf("   Valid: %t\n", result.Valid)
	fmt.Printf("   Errors: %d\n", len(result.Errors))
	fmt.Printf("   Warnings: %d\n", len(result.Warnings))

	if len(result.Errors) > 0 {
		fmt.Println("   Error Details:")
		for _, err := range result.Errors {
			fmt.Printf("     - %s: %s (%s)\n", err.Field, err.Message, err.Severity.String())
		}
	}

	if result.Status != nil {
		fmt.Printf("   Status: %s - %s\n", result.Status.State.String(), result.Status.Message)
	}
}

func printValidationStatus(status *validator.ValidationStatus) {
	statusJSON, _ := json.MarshalIndent(map[string]interface{}{
		"state":              status.State.String(),
		"message":            status.Message,
		"error_count":        status.ErrorCount,
		"warning_count":      status.WarningCount,
		"last_validated":     status.LastValidated.Format("2006-01-02 15:04:05"),
		"validated_sections": status.ValidatedSections,
		"failed_sections":    status.FailedSections,
	}, "", "  ")
	fmt.Println(string(statusJSON))
}

func printValidationHistory(history []validator.ValidationHistoryEntry) {
	for i, entry := range history {
		fmt.Printf("%d. %s - %s (Errors: %d, Warnings: %d) [%s]\n",
			i+1,
			entry.Timestamp.Format("15:04:05"),
			entry.State.String(),
			entry.ErrorCount,
			entry.WarningCount,
			entry.ConfigHash[:8],
		)
	}
}
