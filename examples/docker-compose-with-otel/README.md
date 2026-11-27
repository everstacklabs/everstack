# Docker Compose Examples

This directory contains Docker Compose examples for running Everstack Gateway.

## Quick Start

### Option 1: Without OTEL Telemetry (Simplest)

```bash
docker-compose -f docker-compose.without-otel.yaml up -d
```

This starts:
- PostgreSQL (database)
- ClickHouse (analytics)
- Redis (cache)
- Everstack Gateway (telemetry disabled)

### Option 2: With OTEL Telemetry

```bash
# Step 1: Export the embedded OTEL collector config
mf otel export -o otel-collector-config.yaml

# Step 2: Start all services
docker-compose -f docker-compose.with-otel.yaml up -d
```

This starts everything from Option 1 plus:
- OTEL Collector (telemetry collection)

## Configuration Modes

### OTEL Collector Modes

The OTEL collector can run in two modes:

#### 1. Default Mode (Console Logging)
```bash
# Export default config (logs to console)
mf otel export -o otel-collector-config.yaml

# Start services
docker-compose -f docker-compose.with-otel.yaml up -d
```

Good for: Development, testing, debugging

#### 2. ClickHouse Mode (Persistent Storage)
```bash
# Export ClickHouse config (stores in database)
mf otel export --mode clickhouse -o otel-collector-config.yaml

# Start services
docker-compose -f docker-compose.with-otel.yaml up -d
```

Good for: Production, analytics, long-term storage

## One-Liner Setup

### Without Telemetry
```bash
curl -fsSL https://raw.githubusercontent.com/everstack/everstack/main/examples/docker-compose/docker-compose.without-otel.yaml | docker-compose -f - up -d
```

### With Telemetry
```bash
# Download and export config
mf otel export -o otel-collector-config.yaml

# Start services
curl -fsSL https://raw.githubusercontent.com/everstack/everstack/main/examples/docker-compose/docker-compose.with-otel.yaml | docker-compose -f - up -d
```

## Service URLs

After starting, services are available at:

- **Gateway**: http://localhost:8089
- **PostgreSQL**: localhost:5432
- **ClickHouse HTTP**: http://localhost:8123
- **ClickHouse Native**: localhost:9000
- **Redis**: localhost:6379
- **OTEL Collector gRPC**: localhost:4317
- **OTEL Collector HTTP**: localhost:4318
- **OTEL Collector Metrics**: http://localhost:8888/metrics

## Health Checks

Check if services are healthy:

```bash
# All services
docker-compose -f docker-compose.with-otel.yaml ps

# Gateway health
curl http://localhost:8089/health

# OTEL Collector metrics
curl http://localhost:8888/metrics
```

## Logs

View logs:

```bash
# All services
docker-compose -f docker-compose.with-otel.yaml logs -f

# Gateway only
docker-compose -f docker-compose.with-otel.yaml logs -f gateway

# OTEL Collector only
docker-compose -f docker-compose.with-otel.yaml logs -f otel-collector
```

## Stopping Services

```bash
# Stop all services
docker-compose -f docker-compose.with-otel.yaml down

# Stop and remove volumes (WARNING: deletes all data)
docker-compose -f docker-compose.with-otel.yaml down -v
```

## Customization

### Using Custom Gateway Config

If you want to use a custom gateway configuration file:

```yaml
services:
  gateway:
    # ... other config ...
    volumes:
      - ./my-gateway-config.yaml:/app/config/gateway.yaml:ro
    command: ["serve", "--config", "/app/config/gateway.yaml"]
```

### Using Custom OTEL Config

The OTEL collector config is already mounted from `otel-collector-config.yaml`. To customize:

1. Export the default config:
   ```bash
   mf otel export -o otel-collector-config.yaml
   ```

2. Edit `otel-collector-config.yaml` as needed

3. Restart the collector:
   ```bash
   docker-compose -f docker-compose.with-otel.yaml restart otel-collector
   ```

### Environment Variables

You can override any environment variable in the compose file:

```bash
# Create .env file
cat > .env <<EOF
EVS_TELEMETRY_ENABLED=true
EVS_CACHE_TYPE=redis
EVS_SERVER_PORT=8089
EOF

# Start with .env
docker-compose -f docker-compose.with-otel.yaml up -d
```

## Troubleshooting

### OTEL Collector Connection Errors

If you see errors like:
```
processor export timeout: rpc error: code = Unavailable
```

**Solution 1**: Disable telemetry
```bash
# Edit docker-compose.with-otel.yaml
# Set: EVS_TELEMETRY_ENABLED: "false"
docker-compose -f docker-compose.with-otel.yaml up -d
```

**Solution 2**: Check OTEL collector is running
```bash
docker-compose -f docker-compose.with-otel.yaml ps otel-collector
docker-compose -f docker-compose.with-otel.yaml logs otel-collector
```

### Database Connection Errors

```bash
# Check if databases are healthy
docker-compose -f docker-compose.with-otel.yaml ps

# Check logs
docker-compose -f docker-compose.with-otel.yaml logs postgres
docker-compose -f docker-compose.with-otel.yaml logs clickhouse
```

### Port Conflicts

If ports are already in use, edit the compose file to use different ports:

```yaml
services:
  gateway:
    ports:
      - "8090:8089"  # Changed from 8089:8089
```

## Production Considerations

For production deployments:

1. **Use secrets for passwords**:
   ```yaml
   environment:
     POSTGRES_PASSWORD_FILE: /run/secrets/postgres_password
   secrets:
     - postgres_password
   ```

2. **Use persistent volumes**:
   ```yaml
   volumes:
     postgres_data:
       driver: local
       driver_opts:
         type: none
         o: bind
         device: /mnt/data/postgres
   ```

3. **Enable TLS**:
   ```yaml
   environment:
     EVS_SERVER_TLS_ENABLED: "true"
     EVS_SERVER_TLS_CERT_PATH: /certs/server.crt
     EVS_SERVER_TLS_KEY_PATH: /certs/server.key
   ```

4. **Use ClickHouse mode for OTEL**:
   ```bash
   mf otel export --mode clickhouse -o otel-collector-config.yaml
   ```

5. **Set proper resource limits**:
   ```yaml
   services:
     gateway:
       deploy:
         resources:
           limits:
             cpus: '2'
             memory: 2G
           reservations:
             cpus: '1'
             memory: 1G
   ```

## Related Documentation

- [Gateway Configuration](../../cmd/config/gateway/defaults/gateway-default.yaml)
- [OTEL Collector Configuration](../../cmd/config/otel/defaults/)
- [Environment Variables](../../env.vps.example)
