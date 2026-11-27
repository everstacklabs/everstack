#!/bin/bash
# Test script for OpenTelemetry logging integration

set -e

echo "🧪 OpenTelemetry Logging Test Script"
echo "===================================="
echo ""

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

# Configuration
GATEWAY_URL="${GATEWAY_URL:-http://localhost:8089}"
CLICKHOUSE_HOST="${CLICKHOUSE_HOST:-localhost}"
API_KEY="${EVS_API_KEY:-sk-mf-test}"

echo "📋 Configuration:"
echo "  Gateway URL: $GATEWAY_URL"
echo "  ClickHouse Host: $CLICKHOUSE_HOST"
echo "  API Key: ${API_KEY:0:20}..."
echo ""

# Test 1: Model Not Found (should emit model.not_found event)
echo "🔍 Test 1: Model Not Found Error"
echo "--------------------------------"
echo "Making request with non-existent model..."

CORRELATION_ID_1=$(openssl rand -hex 8)
curl -X POST "$GATEWAY_URL/everstack.gateway.v1.GatewayService/ChatCompletion" \
  -H "Content-Type: application/json" \
  -H "x-evs-api-key: $API_KEY" \
  -H "x-correlation-id: $CORRELATION_ID_1" \
  -d '{
    "messages": [{
      "role": "ROLE_USER",
      "content": [{"type": "text", "text": "Hello"}]
    }]
  }' 2>&1 | head -20

echo ""
echo "✅ Test 1 completed. Correlation ID: $CORRELATION_ID_1"
echo ""

# Wait for logs to be flushed
echo "⏳ Waiting 15 seconds for logs to be flushed to ClickHouse..."
sleep 15

# Test 2: Query ClickHouse for the correlation_id
echo "📊 Test 2: Verify logs in ClickHouse"
echo "------------------------------------"
echo "Querying logs for correlation_id: $CORRELATION_ID_1"

docker exec clickhouse-gateway clickhouse-client --query="
SELECT 
    Timestamp,
    SeverityText,
    Body,
    LogAttributes['correlation_id'] as CorrelationID,
    LogAttributes['requested_model'] as RequestedModel,
    ResourceAttributes['tenant.id'] as TenantID
FROM everstack.otel_logs
WHERE LogAttributes['correlation_id'] = '$CORRELATION_ID_1'
ORDER BY Timestamp ASC
FORMAT Pretty
" || echo "${RED}⚠️  Failed to query ClickHouse${NC}"

echo ""

# Test 3: Check model not found view
echo "🔎 Test 3: Query Model Not Found Events"
echo "---------------------------------------"
echo "Querying model_not_found_events view..."

docker exec clickhouse-gateway clickhouse-client --query="
SELECT 
    Timestamp,
    RequestedModel,
    UserID,
    CorrelationID,
    TenantID
FROM everstack.model_not_found_events
WHERE Timestamp >= now() - INTERVAL 1 HOUR
ORDER BY Timestamp DESC
LIMIT 10
FORMAT Pretty
" || echo "${RED}⚠️  Failed to query ClickHouse${NC}"

echo ""

# Test 4: Check gateway errors view
echo "⚠️  Test 4: Query Gateway Errors"
echo "--------------------------------"
echo "Querying gateway_errors view (last hour)..."

docker exec clickhouse-gateway clickhouse-client --query="
SELECT 
    Timestamp,
    SeverityText,
    Body,
    CorrelationID,
    Model,
    TenantID
FROM everstack.gateway_errors
WHERE Timestamp >= now() - INTERVAL 1 HOUR
ORDER BY Timestamp DESC
LIMIT 10
FORMAT Pretty
" || echo "${RED}⚠️  Failed to query ClickHouse${NC}"

echo ""

# Test 5: Tenant isolation test
echo "🔒 Test 5: Tenant Isolation"
echo "---------------------------"
echo "Querying user logs vs internal logs..."

echo "User Logs (self-hosted):"
docker exec clickhouse-gateway clickhouse-client --query="
SELECT count() as UserLogCount
FROM everstack.gateway_user_logs
WHERE Timestamp >= now() - INTERVAL 1 HOUR
FORMAT Pretty
" || echo "${RED}⚠️  Failed to query ClickHouse${NC}"

echo ""
echo "Internal Logs (Everstack):"
docker exec clickhouse-gateway clickhouse-client --query="
SELECT count() as InternalLogCount
FROM everstack.gateway_internal_logs
WHERE Timestamp >= now() - INTERVAL 1 HOUR
FORMAT Pretty
" || echo "${RED}⚠️  Failed to query ClickHouse${NC}"

echo ""

# Test 6: Check OTEL Collector metrics
echo "📈 Test 6: OTEL Collector Metrics"
echo "----------------------------------"
echo "Checking collector health..."

curl -s http://localhost:8888/metrics | grep -E "otelcol_receiver_accepted_log_records|otelcol_exporter_sent_log_records" | head -10 || echo "${RED}⚠️  Failed to reach collector metrics${NC}"

echo ""
echo ""

# Summary
echo "========================================"
echo "✅ Test Suite Completed"
echo "========================================"
echo ""
echo "Next steps:"
echo "1. Check logs: docker logs otel-collector-gateway"
echo "2. Access ClickHouse: docker exec -it clickhouse-gateway clickhouse-client"
echo "3. View collector metrics: curl http://localhost:8888/metrics"
echo ""
echo "Useful queries:"
echo "  SELECT * FROM everstack.otel_logs WHERE LogAttributes['correlation_id'] = '$CORRELATION_ID_1';"
echo "  SELECT * FROM everstack.model_not_found_events LIMIT 10;"
echo "  SELECT * FROM everstack.gateway_errors WHERE Timestamp >= now() - INTERVAL 24 HOUR;"
echo ""



