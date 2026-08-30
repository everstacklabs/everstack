package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
)

func main() {
	catalogDir := "model-catalog"
	if len(os.Args) > 1 {
		catalogDir = os.Args[1]
	}

	models, providers, err := validator.LoadCatalogFromDirectory(catalogDir)
	if err != nil {
		panic(fmt.Errorf("load catalog: %w", err))
	}

	defaultsDir := filepath.Join("cmd", "config", "gateway", "defaults")
	if err := os.WriteFile(filepath.Join(defaultsDir, "models.yaml"), models, 0o644); err != nil {
		panic(fmt.Errorf("write embedded models: %w", err))
	}
	if err := os.WriteFile(filepath.Join(defaultsDir, "providers.yaml"), providers, 0o644); err != nil {
		panic(fmt.Errorf("write embedded providers: %w", err))
	}

	fmt.Printf("generated embedded defaults from %s\n", catalogDir)
}
