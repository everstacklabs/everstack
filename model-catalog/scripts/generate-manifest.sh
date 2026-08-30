#!/bin/bash
# Generate manifest.yaml from the providers directory structure
# This script is run by GitHub Actions to keep manifest.yaml in sync

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CATALOG_DIR="$(dirname "$SCRIPT_DIR")"
PROVIDERS_DIR="$CATALOG_DIR/providers"
MANIFEST_FILE="$CATALOG_DIR/manifest.yaml"
VERSION_FILE="$CATALOG_DIR/version.txt"

# Read current version
VERSION="unknown"
if [[ -f "$VERSION_FILE" ]]; then
    VERSION=$(cat "$VERSION_FILE" | tr -d '\n\r ')
fi

# Get current timestamp in ISO format
GENERATED_AT=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

# Initialize counters
TOTAL_PROVIDERS=0
TOTAL_MODELS=0
STATIC_PROVIDERS=0
META_PROVIDERS=0

# Start building the manifest
cat > "$MANIFEST_FILE" << EOF
# Model Catalog Manifest
# Auto-generated - DO NOT EDIT MANUALLY
# Run: ./scripts/generate-manifest.sh to regenerate

version: "$VERSION"
generated_at: "$GENERATED_AT"
schema_version: "1.0"

providers:
EOF

# Iterate through provider directories
for provider_dir in "$PROVIDERS_DIR"/*/; do
    if [[ ! -d "$provider_dir" ]]; then
        continue
    fi

    provider_name=$(basename "$provider_dir")
    TOTAL_PROVIDERS=$((TOTAL_PROVIDERS + 1))

    # Check provider type from provider.yaml
    provider_yaml="$provider_dir/provider.yaml"
    if [[ -f "$provider_yaml" ]]; then
        provider_type=$(grep -E "^provider_type:" "$provider_yaml" | awk '{print $2}' | tr -d '"' || echo "static")
        if [[ "$provider_type" == "meta" ]]; then
            META_PROVIDERS=$((META_PROVIDERS + 1))
        else
            STATIC_PROVIDERS=$((STATIC_PROVIDERS + 1))
        fi
    fi

    echo "  - name: $provider_name" >> "$MANIFEST_FILE"
    echo "    files:" >> "$MANIFEST_FILE"

    # Add provider-level files
    for file in provider.yaml categories.yaml templates.yaml; do
        if [[ -f "$provider_dir$file" ]]; then
            echo "      - providers/$provider_name/$file" >> "$MANIFEST_FILE"
        fi
    done

    # Add model files
    models_dir="$provider_dir/models"
    echo "    models:" >> "$MANIFEST_FILE"
    
    if [[ -d "$models_dir" ]]; then
        model_count=0
        for model_file in "$models_dir"/*.yaml; do
            if [[ -f "$model_file" ]]; then
                model_filename=$(basename "$model_file")
                echo "      - providers/$provider_name/models/$model_filename" >> "$MANIFEST_FILE"
                model_count=$((model_count + 1))
                TOTAL_MODELS=$((TOTAL_MODELS + 1))
            fi
        done
        
        # If no models found (meta-provider), add empty array indicator
        if [[ $model_count -eq 0 ]]; then
            echo "      # Dynamic models discovered at runtime" >> "$MANIFEST_FILE"
        fi
    else
        echo "      # No models directory" >> "$MANIFEST_FILE"
    fi

    echo "" >> "$MANIFEST_FILE"
done

# Add statistics
cat >> "$MANIFEST_FILE" << EOF
# Statistics
stats:
  total_providers: $TOTAL_PROVIDERS
  total_models: $TOTAL_MODELS
  static_providers: $STATIC_PROVIDERS
  meta_providers: $META_PROVIDERS
EOF

echo "✅ Generated manifest.yaml"
echo "   - Providers: $TOTAL_PROVIDERS ($STATIC_PROVIDERS static, $META_PROVIDERS meta)"
echo "   - Models: $TOTAL_MODELS"
echo "   - Version: $VERSION"

