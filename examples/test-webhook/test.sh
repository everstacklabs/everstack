#!/bin/bash

# Test script for serverless functions
# Prerequisites:
# 1. Gateway running on localhost:8080
# 2. Test webhook server running: go run main.go (in this directory)
# 3. Database migrated with functions table

set -e

GATEWAY_URL="${GATEWAY_URL:-http://localhost:8089}"
WEBHOOK_URL="${WEBHOOK_URL:-http://localhost:9999}"
API_KEY="$EVERSTACK_API_KEY"

# Get tenant ID from the gateway's organization (you may need to adjust this)
TENANT_ID="${TENANT_ID:-00000000-0000-0000-0000-000000000000}"

echo "=== Serverless Functions Test ==="
echo "Gateway: $GATEWAY_URL"
echo "Webhook: $WEBHOOK_URL"
echo "Tenant ID: $TENANT_ID"
echo ""

# ============================================
# Step 1: Create a weather function
# ============================================
echo ">>> Step 1: Creating 'get_weather' function..."

curl -s -X POST "$GATEWAY_URL/v1/functions" \
  -H "Content-Type: application/json" \
  -H "x-evs-api-key": "$API_KEY" \
  -d '{
    "tenant_id": "'"$TENANT_ID"'",
    "name": "get_weather",
    "description": "Get the current weather for a specified location",
    "mode": 1,
    "webhook": {
      "url": "'"$WEBHOOK_URL"'/weather",
      "method": "POST",
      "timeout_ms": 30000
    },
    "parameters": {
      "type": "object",
      "properties": {
        "location": {
          "type": "string",
          "description": "The city and country, e.g. San Francisco, CA"
        }
      },
      "required": ["location"]
    },
    "timeout_ms": 30000,
    "max_retries": 1
  }' | jq .

echo ""

# ============================================
# Step 2: Create a calculator function
# ============================================
echo ">>> Step 2: Creating 'calculator' function..."

curl -s -X POST "$GATEWAY_URL/v1/functions" \
  -H "Content-Type: application/json" \
  -H "x-evs-api-key": "$API_KEY" \
  -d '{
    "tenant_id": "'"$TENANT_ID"'",
    "name": "calculator",
    "description": "Perform basic arithmetic operations",
    "mode": 1,
    "webhook": {
      "url": "'"$WEBHOOK_URL"'/calculator",
      "method": "POST",
      "timeout_ms": 30000
    },
    "parameters": {
      "type": "object",
      "properties": {
        "operation": {
          "type": "string",
          "enum": ["add", "subtract", "multiply", "divide"],
          "description": "The arithmetic operation to perform"
        },
        "a": {
          "type": "number",
          "description": "First operand"
        },
        "b": {
          "type": "number",
          "description": "Second operand"
        }
      },
      "required": ["operation", "a", "b"]
    },
    "timeout_ms": 30000,
    "max_retries": 1
  }' | jq .

echo ""

# ============================================
# Step 3: List functions
# ============================================
echo ">>> Step 3: Listing functions..."

curl -s -X GET "$GATEWAY_URL/v1/functions?tenant_id=$TENANT_ID" \
  -H "x-evs-api-key": "$API_KEY" | jq .

echo ""

# ============================================
# Step 4: Test chat with weather tool
# ============================================
echo ">>> Step 4: Testing chat with weather tool..."
echo "Sending: 'What is the weather like in London?'"
echo ""

curl -s -X POST "$GATEWAY_URL/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -H "x-evs-api-key": "$API_KEY" \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [
      {
        "role": "user",
        "content": "What is the weather like in London right now?"
      }
    ],
    "tools": [
      {
        "type": "function",
        "function": {
          "name": "get_weather",
          "description": "Get the current weather for a specified location",
          "parameters": {
            "type": "object",
            "properties": {
              "location": {
                "type": "string",
                "description": "The city and country, e.g. San Francisco, CA"
              }
            },
            "required": ["location"]
          }
        }
      }
    ],
    "tool_choice": "auto"
  }' | jq .

echo ""

# ============================================
# Step 5: Test chat with calculator tool
# ============================================
echo ">>> Step 5: Testing chat with calculator tool..."
echo "Sending: 'What is 42 multiplied by 17?'"
echo ""

curl -s -X POST "$GATEWAY_URL/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -H "x-evs-api-key": "$API_KEY" \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [
      {
        "role": "user",
        "content": "What is 42 multiplied by 17? Use the calculator."
      }
    ],
    "tools": [
      {
        "type": "function",
        "function": {
          "name": "calculator",
          "description": "Perform basic arithmetic operations",
          "parameters": {
            "type": "object",
            "properties": {
              "operation": {
                "type": "string",
                "enum": ["add", "subtract", "multiply", "divide"],
                "description": "The arithmetic operation to perform"
              },
              "a": {
                "type": "number",
                "description": "First operand"
              },
              "b": {
                "type": "number",
                "description": "Second operand"
              }
            },
            "required": ["operation", "a", "b"]
          }
        }
      }
    ],
    "tool_choice": "auto"
  }' | jq .

echo ""
echo "=== Test Complete ==="
