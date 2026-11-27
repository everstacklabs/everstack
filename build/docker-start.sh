#!/bin/bash

# Everstack Docker Quick Start Script
set -euo pipefail

BLUE='\033[0;34m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

echo -e "${BLUE}"
echo "🚀 EVERSTACK DOCKER QUICK START"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "Starting complete Everstack stack with Docker Compose"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo -e "${NC}"

# Check if Docker is running
if ! docker info >/dev/null 2>&1; then
    echo -e "${RED}❌ Docker is not running. Please start Docker first.${NC}"
    exit 1
fi

# Check if Docker Compose is available
if ! docker compose version >/dev/null 2>&1; then
    echo -e "${RED}❌ Docker Compose is not available. Please install Docker Compose.${NC}"
    exit 1
fi

echo -e "${YELLOW}🔧 Starting Everstack stack...${NC}"
echo ""

# Start the stack
docker compose up -d

echo ""
echo -e "${GREEN}✅ Everstack stack is starting up!${NC}"
echo ""

# Wait for services to be ready
echo -e "${YELLOW}⏳ Waiting for services to be ready...${NC}"
echo ""

# Function to check service health
check_service() {
    local service_name="$1"
    local url="$2"
    local max_attempts=30
    local attempt=1
    
    echo -n "Checking ${service_name}... "
    
    while [ $attempt -le $max_attempts ]; do
        if curl -s "$url" >/dev/null 2>&1; then
            echo -e "${GREEN}✅ Ready${NC}"
            return 0
        fi
        sleep 2
        attempt=$((attempt + 1))
        echo -n "."
    done
    
    echo -e "${RED}❌ Timeout${NC}"
    return 1
}

# Check core services
echo "Checking service health:"
check_service "PostgreSQL" "localhost:5432" || true
check_service "Redis" "localhost:6379" || true
check_service "ClickHouse" "http://localhost:8123/ping" || true

# Wait a bit more for Everstack to start
echo ""
echo -e "${YELLOW}⏳ Waiting for Everstack gateway to start...${NC}"
sleep 15

# Check Everstack gateway
if curl -s "http://localhost:8089/health" >/dev/null 2>&1; then
    echo -e "${GREEN}✅ Everstack gateway is ready!${NC}"
else
    echo -e "${YELLOW}⚠️  Everstack gateway is still starting up...${NC}"
fi

echo ""
echo -e "${GREEN}🎉 EVERSTACK STACK IS READY!${NC}"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo -e "${BLUE}📊 Service URLs:${NC}"
echo "🚀 Everstack Gateway:    http://localhost:8089"
echo "📈 Grafana Monitoring:   http://localhost:3001 (admin/admin123)"
echo "🔍 Jaeger Tracing:       http://localhost:16686"
echo "💾 ClickHouse DB:        http://localhost:8123"
echo ""
echo -e "${BLUE}📚 API Endpoints:${NC}"
echo "🌐 Health Check:         http://localhost:8089/health"
echo "🤖 Chat Completions:     http://localhost:8089/v1/chat/completions"
echo "🔌 Connect gRPC:         http://localhost:8089/everstack.gateway.v1.GatewayService/ChatCompletion"
echo ""
echo -e "${YELLOW}📝 Quick Test:${NC}"
echo '
curl -X POST http://localhost:8089/v1/chat/completions \'
echo '  -H "Content-Type: application/json" \'
echo '  -H "Authorization: Bearer YOUR_API_KEY" \'
echo '  -d '"'"'{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Hello!"}]}'"'"
echo ""
echo -e "${BLUE}🛠️  Management:${NC}"
echo "📋 View logs:            docker compose logs -f everstack"
echo "🔄 Restart:              docker compose restart everstack"
echo "🛑 Stop:                 docker compose down"
echo "🧹 Clean up:             docker compose down -v"
echo ""
echo -e "${GREEN}Happy building with Everstack! 🚀${NC}"
