package observations

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// EnhancedRecorder records observations with full metadata including workflow, performance, and resource metrics
type EnhancedRecorder struct {
	db *sql.DB
}

// NewEnhancedRecorder creates a new enhanced observation recorder
func NewEnhancedRecorder(db *sql.DB) *EnhancedRecorder {
	return &EnhancedRecorder{
		db: db,
	}
}

// ObservationRecord represents a complete observation with all enhanced data
type ObservationRecord struct {
	// Base observation fields
	ObservationID       string
	TraceID             string
	ParentObservationID string
	Name                string
	StartTime           time.Time
	EndTime             *time.Time
	Duration            int64 // nanoseconds
	StatusCode          string
	StatusMessage       string

	// Enhanced workflow fields
	Step            *uint32
	Node            *string
	ObservationType *string

	// Performance metrics
	Performance *PerformanceMetrics

	// Resource metrics
	Resources *ResourceMetrics

	// I/O data
	IO *ObservationIO

	// Workflow context
	Workflow *WorkflowContext
}

// PerformanceMetrics represents detailed timing breakdown
type PerformanceMetrics struct {
	QueueTimeNs         *int64
	ProcessingTimeNs    *int64
	NetworkLatencyNs    *int64
	SerializationTimeNs *int64
	DbQueryTimeNs       *int64
	CacheLookupTimeNs   *int64
	LlmTTFTNs           *int64
	LlmTimePerTokenNs   *int64
}

// ResourceMetrics represents resource utilization
type ResourceMetrics struct {
	MemoryUsedBytes      *int64
	MemoryAllocatedBytes *int64
	CpuUsagePercent      *float64
	NetworkBytesSent     *int64
	NetworkBytesReceived *int64
	DiskReadBytes        *int64
	DiskWriteBytes       *int64
	ThreadCount          *int32
}

// ObservationIO represents input/output data
type ObservationIO struct {
	InputData      *string
	OutputData     *string
	InputTokens    *int64
	OutputTokens   *int64
	TotalTokens    *int64
	InputMimeType  *string
	OutputMimeType *string
}

// WorkflowContext represents workflow execution context
type WorkflowContext struct {
	WorkflowID      string
	WorkflowType    string
	WorkflowName    string
	WorkflowVersion *string
	Context         map[string]string
	ExecutionMode   *string
	TriggerSource   *string
}

// RecordObservation records a complete observation with all enhanced data
func (r *EnhancedRecorder) RecordObservation(ctx context.Context, obs *ObservationRecord) error {
	// Start a transaction to ensure atomicity
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Record performance metrics if provided
	if obs.Performance != nil {
		if err := r.recordPerformanceMetrics(ctx, tx, obs.ObservationID, obs.TraceID, obs.StartTime, obs.Performance); err != nil {
			logger.WithFields("observation_id", obs.ObservationID, "error", err.Error()).Warn("failed to record performance metrics")
		}
	}

	// Record resource metrics if provided
	if obs.Resources != nil {
		if err := r.recordResourceMetrics(ctx, tx, obs.ObservationID, obs.TraceID, obs.StartTime, obs.Resources); err != nil {
			logger.WithFields("observation_id", obs.ObservationID, "error", err.Error()).Warn("failed to record resource metrics")
		}
	}

	// Record I/O data if provided
	if obs.IO != nil {
		if err := r.recordObservationIO(ctx, tx, obs.ObservationID, obs.TraceID, obs.StartTime, obs.IO); err != nil {
			logger.WithFields("observation_id", obs.ObservationID, "error", err.Error()).Warn("failed to record I/O data")
		}
	}

	// Record workflow context if provided
	if obs.Workflow != nil {
		if err := r.recordWorkflowMetadata(ctx, tx, obs.TraceID, obs.StartTime, obs.Workflow); err != nil {
			logger.WithFields("trace_id", obs.TraceID, "error", err.Error()).Warn("failed to record workflow metadata")
		}
	}

	// Commit the transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	logger.WithFields(
		"observation_id", obs.ObservationID,
		"trace_id", obs.TraceID,
		"step", obs.Step,
		"node", obs.Node,
	).Debug("recorded enhanced observation")

	return nil
}

func (r *EnhancedRecorder) recordPerformanceMetrics(ctx context.Context, tx *sql.Tx, observationID, traceID string, timestamp time.Time, metrics *PerformanceMetrics) error {
	query := `
		INSERT INTO otel_performance_metrics (
			ObservationId, TraceId, Timestamp,
			QueueTimeNs, ProcessingTimeNs, NetworkLatencyNs,
			SerializationTimeNs, DbQueryTimeNs, CacheLookupTimeNs,
			LlmTimeToFirstTokenNs, LlmTimePerTokenNs
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := tx.ExecContext(ctx, query,
		observationID, traceID, timestamp,
		metrics.QueueTimeNs, metrics.ProcessingTimeNs, metrics.NetworkLatencyNs,
		metrics.SerializationTimeNs, metrics.DbQueryTimeNs, metrics.CacheLookupTimeNs,
		metrics.LlmTTFTNs, metrics.LlmTimePerTokenNs,
	)

	if err != nil {
		return fmt.Errorf("failed to insert performance metrics: %w", err)
	}

	return nil
}

func (r *EnhancedRecorder) recordResourceMetrics(ctx context.Context, tx *sql.Tx, observationID, traceID string, timestamp time.Time, metrics *ResourceMetrics) error {
	query := `
		INSERT INTO otel_resource_metrics (
			ObservationId, TraceId, Timestamp,
			MemoryUsedBytes, MemoryAllocatedBytes, CpuUsagePercent,
			NetworkBytesSent, NetworkBytesReceived,
			DiskReadBytes, DiskWriteBytes, ThreadCount
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := tx.ExecContext(ctx, query,
		observationID, traceID, timestamp,
		metrics.MemoryUsedBytes, metrics.MemoryAllocatedBytes, metrics.CpuUsagePercent,
		metrics.NetworkBytesSent, metrics.NetworkBytesReceived,
		metrics.DiskReadBytes, metrics.DiskWriteBytes, metrics.ThreadCount,
	)

	if err != nil {
		return fmt.Errorf("failed to insert resource metrics: %w", err)
	}

	return nil
}

func (r *EnhancedRecorder) recordObservationIO(ctx context.Context, tx *sql.Tx, observationID, traceID string, timestamp time.Time, io *ObservationIO) error {
	query := `
		INSERT INTO otel_observation_io (
			ObservationId, TraceId, Timestamp,
			InputData, OutputData,
			InputTokens, OutputTokens, TotalTokens,
			InputMimeType, OutputMimeType
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := tx.ExecContext(ctx, query,
		observationID, traceID, timestamp,
		io.InputData, io.OutputData,
		io.InputTokens, io.OutputTokens, io.TotalTokens,
		io.InputMimeType, io.OutputMimeType,
	)

	if err != nil {
		return fmt.Errorf("failed to insert observation I/O: %w", err)
	}

	return nil
}

func (r *EnhancedRecorder) recordWorkflowMetadata(ctx context.Context, tx *sql.Tx, traceID string, timestamp time.Time, workflow *WorkflowContext) error {
	// Convert context map to JSON
	contextJSON, err := json.Marshal(workflow.Context)
	if err != nil {
		return fmt.Errorf("failed to marshal workflow context: %w", err)
	}

	query := `
		INSERT INTO otel_workflow_metadata (
			WorkflowId, TraceId, Timestamp,
			WorkflowType, WorkflowName, WorkflowVersion,
			ExecutionMode, TriggerSource, Context
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err = tx.ExecContext(ctx, query,
		workflow.WorkflowID, traceID, timestamp,
		workflow.WorkflowType, workflow.WorkflowName, workflow.WorkflowVersion,
		workflow.ExecutionMode, workflow.TriggerSource, string(contextJSON),
	)

	if err != nil {
		return fmt.Errorf("failed to insert workflow metadata: %w", err)
	}

	return nil
}

// RecordBatch records multiple observations in a single transaction for better performance
func (r *EnhancedRecorder) RecordBatch(ctx context.Context, observations []*ObservationRecord) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	for _, obs := range observations {
		if obs.Performance != nil {
			if err := r.recordPerformanceMetrics(ctx, tx, obs.ObservationID, obs.TraceID, obs.StartTime, obs.Performance); err != nil {
				logger.WithFields("observation_id", obs.ObservationID, "error", err.Error()).Warn("failed to record performance metrics in batch")
			}
		}

		if obs.Resources != nil {
			if err := r.recordResourceMetrics(ctx, tx, obs.ObservationID, obs.TraceID, obs.StartTime, obs.Resources); err != nil {
				logger.WithFields("observation_id", obs.ObservationID, "error", err.Error()).Warn("failed to record resource metrics in batch")
			}
		}

		if obs.IO != nil {
			if err := r.recordObservationIO(ctx, tx, obs.ObservationID, obs.TraceID, obs.StartTime, obs.IO); err != nil {
				logger.WithFields("observation_id", obs.ObservationID, "error", err.Error()).Warn("failed to record I/O data in batch")
			}
		}

		if obs.Workflow != nil {
			if err := r.recordWorkflowMetadata(ctx, tx, obs.TraceID, obs.StartTime, obs.Workflow); err != nil {
				logger.WithFields("trace_id", obs.TraceID, "error", err.Error()).Warn("failed to record workflow metadata in batch")
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit batch transaction: %w", err)
	}

	logger.WithFields("count", len(observations)).Debug("recorded batch of enhanced observations")

	return nil
}
