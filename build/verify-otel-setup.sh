#!/bin/bash

echo "🔍 OpenTelemetry Setup Verification"
echo "===================================="
echo ""

# Colors
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Check 1: OTEL Collector
echo "1️⃣  Checking OTEL Collector..."
if docker ps | grep -q otel-collector-gateway; then
    echo -e "   ${GREEN}✓${NC} Collector container is running"
    
    # Check if collector is actually ready
    if docker logs otel-collector-gateway 2>&1 | tail -5 | grep -q "Everything is ready"; then
        echo -e "   ${GREEN}✓${NC} Collector is ready to receive data"
    else
        echo -e "   ${RED}✗${NC} Collector is running but may not be ready"
        echo "   Last logs:"
        docker logs otel-collector-gateway --tail 3 | sed 's/^/     /'
    fi
else
    echo -e "   ${RED}✗${NC} Collector container is NOT running"
    echo "   Run: docker compose -f build/docker-compose-local.yaml up -d otel-collector"
fi
echo ""

# Check 2: ClickHouse
echo "2️⃣  Checking ClickHouse..."
if docker ps | grep -q clickhouse-gateway; then
    echo -e "   ${GREEN}✓${NC} ClickHouse container is running"
    
    # Check if otel_logs table exists
    if docker exec clickhouse-gateway clickhouse-client --query="EXISTS everstack.otel_logs" 2>/dev/null | grep -q 1; then
        echo -e "   ${GREEN}✓${NC} otel_logs table exists"
    else
        echo -e "   ${RED}✗${NC} otel_logs table does NOT exist"
        echo "   Run: docker exec -i clickhouse-gateway clickhouse-client < build/clickhouse-init.sql"
    fi
else
    echo -e "   ${RED}✗${NC} ClickHouse container is NOT running"
fi
echo ""

# Check 3: Gateway Config
echo "3️⃣  Checking Gateway Config..."
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel)"
CONFIG_FILE="$REPO_ROOT/examples/gateway_hybrid.yaml"
if [ -f "$CONFIG_FILE" ]; then
    echo -e "   ${GREEN}✓${NC} Config file exists: $CONFIG_FILE"
    
    # Check if OTEL is enabled
    if grep -A 5 "telemetry:" "$CONFIG_FILE" | grep -A 6 "otel:" | grep -q "enabled: true"; then
        echo -e "   ${GREEN}✓${NC} OTEL logging is enabled"
        
        # Extract collector URL
        COLLECTOR_URL=$(grep -A 5 "otel:" "$CONFIG_FILE" | grep "collector_url:" | awk '{print $2}' | tr -d '"')
        echo -e "   ${GREEN}✓${NC} Collector URL: $COLLECTOR_URL"
        
        # Extract tenant info
        TENANT_ID=$(grep -A 5 "otel:" "$CONFIG_FILE" | grep "tenant_id:" | awk '{print $2}' | tr -d '"')
        echo -e "   ${GREEN}✓${NC} Tenant ID: $TENANT_ID"
    else
        echo -e "   ${RED}✗${NC} OTEL logging is NOT enabled"
        echo "   Set: server.telemetry.otel.enabled: true"
    fi
else
    echo -e "   ${RED}✗${NC} Config file not found: $CONFIG_FILE"
fi
echo ""

# Check 4: Network connectivity
echo "4️⃣  Checking Network Connectivity..."
COLLECTOR_IP="localhost"
COLLECTOR_PORT="4317"

if nc -zv $COLLECTOR_IP $COLLECTOR_PORT 2>&1 | grep -q "succeeded\|open"; then
    echo -e "   ${GREEN}✓${NC} Collector is reachable at $COLLECTOR_IP:$COLLECTOR_PORT"
else
    echo -e "   ${YELLOW}⚠${NC}  Cannot reach collector at $COLLECTOR_IP:$COLLECTOR_PORT"
    echo "   This might be okay if gateway is running in Docker"
fi
echo ""

# Summary
echo "========================================="
echo ""
echo -e "${YELLOW}📋 NEXT STEP:${NC}"
echo "   Your gateway needs to be fully restarted to initialize OTEL."
echo ""
echo "   Hot-reload does NOT trigger OTEL initialization."
echo ""
echo "   Please restart your gateway:"
echo -e "   ${GREEN}./bin/gateway serve --config examples/gateway_hybrid.yaml${NC}"
echo ""
echo "   Then check gateway startup logs for:"
echo "   'OpenTelemetry logging initialized'"
echo ""

