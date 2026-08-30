-- Enhanced Observability System - Database Schema
-- Adds workflow tracking, step sequencing, performance metrics, and resource utilization

-- ====================================================================
-- ENHANCED SPAN COLUMNS
-- ====================================================================

-- Add enhanced observability columns to existing otel_traces table
ALTER TABLE otel_traces 
  ADD COLUMN IF NOT EXISTS StepNumber Nullable(UInt32) CODEC(ZSTD(1)),
  ADD COLUMN IF NOT EXISTS NodeName Nullable(String) CODEC(ZSTD(1)),
  ADD COLUMN IF NOT EXISTS ObservationType LowCardinality(Nullable(String)) CODEC(ZSTD(1));

-- Add indexes for enhanced columns
ALTER TABLE otel_traces 
  ADD INDEX IF NOT EXISTS idx_step_number StepNumber TYPE minmax GRANULARITY 1;

ALTER TABLE otel_traces 
  ADD INDEX IF NOT EXISTS idx_node_name NodeName TYPE bloom_filter(0.01) GRANULARITY 1;

ALTER TABLE otel_traces 
  ADD INDEX IF NOT EXISTS idx_observation_type ObservationType TYPE bloom_filter(0.01) GRANULARITY 1;

-- ====================================================================
-- OBSERVATION I/O TABLE
-- ====================================================================

-- Store input/output data separately to avoid bloating main traces table
CREATE TABLE IF NOT EXISTS otel_observation_io (
    ObservationId String CODEC(ZSTD(1)),
    TraceId String CODEC(ZSTD(1)),
    Timestamp DateTime64(9) CODEC(Delta(8), ZSTD(1)),
    
    -- Input/Output data (JSON strings)
    InputData String CODEC(ZSTD(3)), -- Higher compression for large text
    OutputData String CODEC(ZSTD(3)),
    
    -- Token counts
    InputTokens Nullable(Int64) CODEC(ZSTD(1)),
    OutputTokens Nullable(Int64) CODEC(ZSTD(1)),
    TotalTokens Nullable(Int64) CODEC(ZSTD(1)),
    
    -- MIME types
    InputMimeType LowCardinality(Nullable(String)) CODEC(ZSTD(1)),
    OutputMimeType LowCardinality(Nullable(String)) CODEC(ZSTD(1)),
    
    INDEX idx_observation_id ObservationId TYPE bloom_filter(0.001) GRANULARITY 1,
    INDEX idx_trace_id TraceId TYPE bloom_filter(0.001) GRANULARITY 1
) ENGINE = MergeTree()
PARTITION BY toDate(Timestamp)
ORDER BY (TraceId, ObservationId, toUnixTimestamp(Timestamp))
TTL toDateTime(Timestamp) + INTERVAL 30 DAY
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

-- ====================================================================
-- PERFORMANCE METRICS TABLE
-- ====================================================================

-- Detailed timing breakdown for each observation
CREATE TABLE IF NOT EXISTS otel_performance_metrics (
    ObservationId String CODEC(ZSTD(1)),
    TraceId String CODEC(ZSTD(1)),
    Timestamp DateTime64(9) CODEC(Delta(8), ZSTD(1)),
    
    -- Timing breakdown (all in nanoseconds)
    QueueTimeNs Nullable(Int64) CODEC(ZSTD(1)),
    ProcessingTimeNs Nullable(Int64) CODEC(ZSTD(1)),
    NetworkLatencyNs Nullable(Int64) CODEC(ZSTD(1)),
    SerializationTimeNs Nullable(Int64) CODEC(ZSTD(1)),
    DbQueryTimeNs Nullable(Int64) CODEC(ZSTD(1)),
    CacheLookupTimeNs Nullable(Int64) CODEC(ZSTD(1)),
    
    -- LLM-specific timings
    LlmTimeToFirstTokenNs Nullable(Int64) CODEC(ZSTD(1)),
    LlmTimePerTokenNs Nullable(Int64) CODEC(ZSTD(1)),
    
    INDEX idx_observation_id ObservationId TYPE bloom_filter(0.001) GRANULARITY 1,
    INDEX idx_trace_id TraceId TYPE bloom_filter(0.001) GRANULARITY 1,
    INDEX idx_processing_time ProcessingTimeNs TYPE minmax GRANULARITY 1
) ENGINE = MergeTree()
PARTITION BY toDate(Timestamp)
ORDER BY (TraceId, ObservationId, toUnixTimestamp(Timestamp))
TTL toDateTime(Timestamp) + INTERVAL 30 DAY
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

-- ====================================================================
-- RESOURCE METRICS TABLE
-- ====================================================================

-- Resource utilization for each observation
CREATE TABLE IF NOT EXISTS otel_resource_metrics (
    ObservationId String CODEC(ZSTD(1)),
    TraceId String CODEC(ZSTD(1)),
    Timestamp DateTime64(9) CODEC(Delta(8), ZSTD(1)),
    
    -- Memory metrics (bytes)
    MemoryUsedBytes Nullable(Int64) CODEC(ZSTD(1)),
    MemoryAllocatedBytes Nullable(Int64) CODEC(ZSTD(1)),
    
    -- CPU metrics
    CpuUsagePercent Nullable(Float64) CODEC(ZSTD(1)),
    
    -- Network metrics (bytes)
    NetworkBytesSent Nullable(Int64) CODEC(ZSTD(1)),
    NetworkBytesReceived Nullable(Int64) CODEC(ZSTD(1)),
    
    -- Disk metrics (bytes)
    DiskReadBytes Nullable(Int64) CODEC(ZSTD(1)),
    DiskWriteBytes Nullable(Int64) CODEC(ZSTD(1)),
    
    -- Thread count
    ThreadCount Nullable(Int32) CODEC(ZSTD(1)),
    
    INDEX idx_observation_id ObservationId TYPE bloom_filter(0.001) GRANULARITY 1,
    INDEX idx_trace_id TraceId TYPE bloom_filter(0.001) GRANULARITY 1,
    INDEX idx_memory_used MemoryUsedBytes TYPE minmax GRANULARITY 1,
    INDEX idx_cpu_usage CpuUsagePercent TYPE minmax GRANULARITY 1
) ENGINE = MergeTree()
PARTITION BY toDate(Timestamp)
ORDER BY (TraceId, ObservationId, toUnixTimestamp(Timestamp))
TTL toDateTime(Timestamp) + INTERVAL 30 DAY
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

-- ====================================================================
-- WORKFLOW METADATA TABLE
-- ====================================================================

-- Workflow execution context
CREATE TABLE IF NOT EXISTS otel_workflow_metadata (
    WorkflowId String CODEC(ZSTD(1)),
    TraceId String CODEC(ZSTD(1)),
    Timestamp DateTime64(9) CODEC(Delta(8), ZSTD(1)),
    
    -- Workflow identification
    WorkflowType LowCardinality(String) CODEC(ZSTD(1)),
    WorkflowName String CODEC(ZSTD(1)),
    WorkflowVersion Nullable(String) CODEC(ZSTD(1)),
    
    -- Execution context
    ExecutionMode LowCardinality(Nullable(String)) CODEC(ZSTD(1)), -- sync, async, streaming
    TriggerSource Nullable(String) CODEC(ZSTD(1)),
    
    -- Additional context (JSON)
    Context String CODEC(ZSTD(1)),
    
    INDEX idx_workflow_id WorkflowId TYPE bloom_filter(0.001) GRANULARITY 1,
    INDEX idx_trace_id TraceId TYPE bloom_filter(0.001) GRANULARITY 1,
    INDEX idx_workflow_type WorkflowType TYPE bloom_filter(0.01) GRANULARITY 1
) ENGINE = MergeTree()
PARTITION BY toDate(Timestamp)
ORDER BY (WorkflowType, WorkflowId, toUnixTimestamp(Timestamp))
TTL toDateTime(Timestamp) + INTERVAL 90 DAY
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

-- ====================================================================
-- MATERIALIZED VIEWS FOR ANALYTICS
-- ====================================================================

-- Trace Analytics Materialized View
-- Pre-aggregates trace statistics for fast analytics queries
CREATE MATERIALIZED VIEW IF NOT EXISTS trace_analytics_mv
ENGINE = AggregatingMergeTree()
PARTITION BY toYYYYMM(start_time)
ORDER BY (trace_id, toUnixTimestamp64Nano(start_time))
TTL start_time + INTERVAL 90 DAY
AS SELECT
    TraceId as trace_id,
    min(Timestamp) as start_time,
    max(Timestamp) as end_time,
    sum(Duration) as total_duration_ns,
    count(*) as total_observations,
    countIf(StatusCode = 'ERROR') as error_count,
    
    -- Performance percentiles (using quantile functions)
    quantileState(0.50)(Duration) as p50_latency_state,
    quantileState(0.95)(Duration) as p95_latency_state,
    quantileState(0.99)(Duration) as p99_latency_state,
    avgState(Duration) as avg_latency_state,
    maxState(Duration) as max_latency_ns,
    minState(Duration) as min_latency_ns,
    
    -- Observation type breakdown
    groupArrayState((ifNull(ObservationType, ''), 1)) as observation_type_counts_state,
    groupArrayState((ifNull(ObservationType, ''), Duration)) as observation_type_durations_state
FROM otel_traces
GROUP BY TraceId;

-- Workflow Performance Materialized View
-- Aggregates workflow-level metrics
CREATE MATERIALIZED VIEW IF NOT EXISTS workflow_performance_mv
ENGINE = AggregatingMergeTree()
PARTITION BY toYYYYMM(start_time)
ORDER BY (workflow_type, workflow_id, toUnixTimestamp64Nano(start_time))
TTL start_time + INTERVAL 90 DAY
AS SELECT
    wm.WorkflowId as workflow_id,
    wm.WorkflowType as workflow_type,
    wm.WorkflowName as workflow_name,
    min(t.Timestamp) as start_time,
    max(t.Timestamp) as end_time,
    sum(t.Duration) as total_duration_ns,
    
    -- Step statistics
    countState(*) as total_steps_state,
    countIfState(t.StatusCode = 'OK') as completed_steps_state,
    countIfState(t.StatusCode = 'ERROR') as failed_steps_state,
    
    -- Success rate
    avgIfState(1, t.StatusCode = 'OK') as success_rate_state
FROM otel_workflow_metadata wm
INNER JOIN otel_traces t ON wm.TraceId = t.TraceId
GROUP BY wm.WorkflowId, wm.WorkflowType, wm.WorkflowName;

-- Step Execution Materialized View
-- Tracks step-by-step execution for workflow analysis
CREATE MATERIALIZED VIEW IF NOT EXISTS step_execution_mv
ENGINE = AggregatingMergeTree()
PARTITION BY toYYYYMM(timestamp)
ORDER BY (trace_id, toUnixTimestamp64Nano(timestamp))
TTL timestamp + INTERVAL 90 DAY
AS SELECT
    TraceId as trace_id,
    ifNull(StepNumber, 0) as step_number,
    ifNull(NodeName, '') as node_name,
    ifNull(ObservationType, '') as observation_type,
    Timestamp as timestamp,
    Duration as duration_ns,
    StatusCode as status,
    
    -- Aggregate metrics per step
    avgState(Duration) as avg_duration_state,
    maxState(Duration) as max_duration_state,
    minState(Duration) as min_duration_state,
    countState(*) as execution_count_state
FROM otel_traces
WHERE StepNumber IS NOT NULL
GROUP BY TraceId, StepNumber, NodeName, ObservationType, Timestamp, Duration, StatusCode;

-- ====================================================================
-- HELPER VIEWS
-- ====================================================================

-- Enhanced Observations View
-- Combines all enhanced data for easy querying
CREATE VIEW IF NOT EXISTS enhanced_observations_view AS
SELECT
    t.TraceId as trace_id,
    t.SpanId as observation_id,
    t.ParentSpanId as parent_observation_id,
    t.SpanName as name,
    t.Timestamp as start_time,
    t.Duration as duration_ns,
    t.StatusCode as status_code,
    t.StatusMessage as status_message,
    
    -- Enhanced fields
    t.StepNumber as step,
    t.NodeName as node,
    t.ObservationType as observation_type,
    
    -- Performance metrics
    pm.QueueTimeNs as queue_time_ns,
    pm.ProcessingTimeNs as processing_time_ns,
    pm.NetworkLatencyNs as network_latency_ns,
    pm.LlmTimeToFirstTokenNs as llm_ttft_ns,
    
    -- Resource metrics
    rm.MemoryUsedBytes as memory_used_bytes,
    rm.CpuUsagePercent as cpu_usage_percent,
    rm.NetworkBytesSent as network_bytes_sent,
    rm.NetworkBytesReceived as network_bytes_received,
    
    -- I/O data availability
    io.ObservationId IS NOT NULL as has_io_data,
    io.InputTokens as input_tokens,
    io.OutputTokens as output_tokens,
    
    -- Workflow context
    wm.WorkflowId as workflow_id,
    wm.WorkflowType as workflow_type,
    wm.WorkflowName as workflow_name
FROM otel_traces t
LEFT JOIN otel_performance_metrics pm 
    ON t.SpanId = pm.ObservationId AND t.TraceId = pm.TraceId
LEFT JOIN otel_resource_metrics rm 
    ON t.SpanId = rm.ObservationId AND t.TraceId = rm.TraceId
LEFT JOIN otel_observation_io io 
    ON t.SpanId = io.ObservationId AND t.TraceId = io.TraceId
LEFT JOIN otel_workflow_metadata wm 
    ON t.TraceId = wm.TraceId;

-- Performance Breakdown View
-- Aggregates performance metrics by node and type
CREATE VIEW IF NOT EXISTS performance_breakdown_view AS
SELECT
    t.TraceId as trace_id,
    t.NodeName as node,
    t.ObservationType as observation_type,
    count(*) as observation_count,
    sum(t.Duration) as total_duration_ns,
    avg(t.Duration) as avg_duration_ns,
    sum(pm.QueueTimeNs) as total_queue_time_ns,
    sum(pm.ProcessingTimeNs) as total_processing_time_ns,
    sum(pm.NetworkLatencyNs) as total_network_latency_ns,
    avg(rm.CpuUsagePercent) as avg_cpu_percent,
    sum(rm.MemoryUsedBytes) as total_memory_bytes
FROM otel_traces t
LEFT JOIN otel_performance_metrics pm 
    ON t.SpanId = pm.ObservationId
LEFT JOIN otel_resource_metrics rm 
    ON t.SpanId = rm.ObservationId
WHERE t.NodeName IS NOT NULL OR t.ObservationType IS NOT NULL
GROUP BY t.TraceId, t.NodeName, t.ObservationType;

