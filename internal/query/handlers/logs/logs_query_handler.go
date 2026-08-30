package logs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/database"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
	"github.com/everstacklabs/everstack/internal/services/provider_catalog"
	"github.com/jmoiron/sqlx"
)

// LogsQueryHandler handles gateway request logs queries
type LogsQueryHandler struct {
	clickhouse *sqlx.DB
	catalog    *provider_catalog.Service
}

// NewLogsQueryHandler creates a new logs query handler
func NewLogsQueryHandler(clickhouse *sqlx.DB, catalog *provider_catalog.Service) *LogsQueryHandler {
	return &LogsQueryHandler{
		clickhouse: clickhouse,
		catalog:    catalog,
	}
}

// QueryType returns the query type this handler processes
func (h *LogsQueryHandler) QueryType() string {
	return "ListLogs"
}

// Handle processes a ListLogsQuery and returns request logs
func (h *LogsQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	logsQuery, ok := q.(*ListLogsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected ListLogsQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)

	logger.WithFields(
		"query_type", logsQuery.QueryType(),
		"correlation_id", correlationID,
	).Debug("executing list logs query")

	// Default to last 24 hours if no from time specified
	from := logsQuery.From
	if from.IsZero() {
		from = logsQuery.Timestamp.Add(-24 * time.Hour)
	}

	// Handle 'to' parameter for historical queries
	to := logsQuery.To
	hasTo := !to.IsZero()

	// Resolve tenant ID for filtering. After PR #48 ripped out
	// schema-per-tenant, both context keys (TenantSchemaFromContext +
	// contextkeys.GetTenantID) carry the same tenant id, but not every
	// middleware sets both. The cookie session middleware on the
	// gateway pod and the api_key interceptors historically set only
	// contextkeys.GetTenantID, leaving TenantSchemaFromContext empty,
	// which silently zeroed every Logs query in cloud mode (Traces
	// kept working because that handler reads contextkeys directly).
	// The middlewares now set both keys, but the read-side fallback
	// stays here as defense-in-depth. Empty fails closed.
	tenantID := database.TenantSchemaFromContext(ctx)
	if tenantID == "" {
		tenantID = contextkeys.GetTenantID(ctx)
	}
	if tenantID == "" {
		return []query.RequestLogReadModel{}, nil
	}

	// Build WHERE clause based on whether 'to' is provided. Tenant
	// predicate is mandatory.
	whereClause := "WHERE Timestamp >= parseDateTimeBestEffort(?)"
	if hasTo {
		whereClause += " AND Timestamp <= parseDateTimeBestEffort(?)"
	}
	whereClause += " AND LogAttributes['tenant_id'] = ?"

	// User-defined attribute columns: project each as a map entry so its value
	// surfaces from whichever log line in the correlation group carries it.
	// Keys are validated identifiers (safe to inline as map keys); refs are
	// bound as parameters. These params sit in the SELECT, ahead of the WHERE
	// params, so they are prepended to the query args below.
	customAttrProjection := ""
	var customAttrSelectArgs []interface{}
	if len(logsQuery.CustomAttrColumns) > 0 {
		pairs := make([]string, 0, len(logsQuery.CustomAttrColumns))
		for _, c := range logsQuery.CustomAttrColumns {
			pairs = append(pairs, fmt.Sprintf("'%s', max(LogAttributes[?])", c.Key))
			customAttrSelectArgs = append(customAttrSelectArgs, c.Ref)
		}
		customAttrProjection = ", map(" + strings.Join(pairs, ", ") + ") as custom_attr_columns"
	}

	// ClickHouse query: group by correlation_id to get one log entry per request
	querySQL := `
	SELECT
		LogAttributes['correlation_id'] as correlation_id,
		max(Timestamp) as last_timestamp,
		min(Timestamp) as first_timestamp,
		-- Get command_type from earliest event where it exists (gateway.request.received is first)
		argMinIf(LogAttributes['command_type'], Timestamp, LogAttributes['command_type'] != '') as command_type,
		-- Provider from the latest SUCCESSFUL provider event (provider.response.received),
		-- falling back to the latest provider event. Using the earliest event surfaced a
		-- failed first attempt (a Cohere->OpenAI fallback showed Cohere in the logs list).
		coalesce(
			nullIf(argMaxIf(LogAttributes['provider'], Timestamp, LogAttributes['provider'] != '' AND LogAttributes['event'] = 'provider.response.received'), ''),
			nullIf(argMaxIf(LogAttributes['provider'], Timestamp, LogAttributes['provider'] != '' AND LogAttributes['event'] LIKE 'provider.%'), ''),
			''
		) as provider,
		-- Model from the latest successful provider event, same fallback (KEPT FOR BACKWARD COMPATIBILITY).
		coalesce(
			nullIf(argMaxIf(LogAttributes['model'], Timestamp, LogAttributes['model'] != '' AND LogAttributes['event'] = 'provider.response.received'), ''),
			nullIf(argMaxIf(LogAttributes['model'], Timestamp, LogAttributes['model'] != '' AND LogAttributes['event'] LIKE 'provider.%'), ''),
			''
		) as model,
		-- Prefer provider latency_ms (from provider.response.received or provider.error), fallback to command elapsed_ms
		COALESCE(
			maxIf(toInt64OrZero(LogAttributes['latency_ms']), LogAttributes['event'] IN ('provider.response.received', 'provider.error')),
			max(toInt64OrZero(LogAttributes['elapsed_ms']))
		) as latency_ms,
		argMax(LogAttributes['event'], Timestamp) as log_event,
		argMax(LogAttributes['payload'], Timestamp) as payload,
		argMin(LogAttributes['payload'], Timestamp) as first_payload,
		anyIf(LogAttributes['payload'], LogAttributes['event'] = 'provider.request.issued') as provider_request_payload,
		-- Response payload lives on provider.response.received. The
		-- terminal gateway.request.completed event emits no payload at all,
		-- so pulling response from argMax(payload, Timestamp) got an
		-- empty payload on every successful chat completion (the log
		-- sheet stayed blank or fell back to error text). Surface it
		-- explicitly and pair with provider.error / command.completed
		-- for fallback paths.
		anyIf(LogAttributes['payload'], LogAttributes['event'] = 'provider.response.received') as provider_response_payload,
		anyIf(LogAttributes['payload'], LogAttributes['event'] = 'provider.error') as provider_error_payload,
		anyIf(LogAttributes['payload'], LogAttributes['event'] = 'command.completed') as command_completed_payload,
		-- Extract stream flag: check if any log in the group has stream='true'
		countIf(LogAttributes['stream'] = 'true') > 0 as stream,
		any(TraceId) as trace_id,
		any(SpanId) as span_id,
		argMax(SeverityText, Timestamp) as severity,
		any(LogAttributes['tenant_id']) as tenant_id,
		any(LogAttributes['tenant_type']) as tenant_type,
		-- Multi-model fallback tracking
		-- Requested model: first model from provider.request.issued event
		argMinIf(LogAttributes['model'], Timestamp, LogAttributes['model'] != '' AND LogAttributes['event'] = 'provider.request.issued') as requested_model,
		-- Served model: last model from provider.response.received or provider.error event
		argMaxIf(LogAttributes['model'], Timestamp, LogAttributes['model'] != '' AND LogAttributes['event'] IN ('provider.request.issued', 'provider.response.received', 'provider.error')) as served_model,
		-- All attempted models in chronological order (convert array to JSON string)
		toString(groupArrayIf(LogAttributes['model'], LogAttributes['model'] != '' AND LogAttributes['event'] = 'provider.request.issued')) as all_attempted_models,
		-- Fallback occurred: more than one DISTINCT model attempted (not just retries of same model)
		toUInt8(if(length(arrayDistinct(groupArrayIf(LogAttributes['model'], LogAttributes['model'] != '' AND LogAttributes['event'] = 'provider.request.issued'))) > 1, 1, 0)) as fallback_occurred,
		-- Attempt count: number of provider.request.issued events (includes retries)
		toUInt32(countIf(LogAttributes['event'] = 'provider.request.issued')) as attempt_count,
		-- Streaming metrics (only for streaming requests)
		maxIf(toInt64OrZero(LogAttributes['stream.ttft_ms']), LogAttributes['stream'] = 'true') as stream_ttft_ms,
		maxIf(toInt32OrZero(LogAttributes['stream.chunk_count']), LogAttributes['stream'] = 'true') as stream_chunk_count,
		maxIf(toFloat64OrZero(LogAttributes['stream.avg_chunk_latency_ms']), LogAttributes['stream'] = 'true') as stream_avg_chunk_latency_ms,
		maxIf(toInt64OrZero(LogAttributes['stream.max_chunk_latency_ms']), LogAttributes['stream'] = 'true') as stream_max_chunk_latency_ms,
		maxIf(toFloat64OrZero(LogAttributes['stream.tokens_per_second']), LogAttributes['stream'] = 'true') as stream_tokens_per_second,
		maxIf(toInt64OrZero(LogAttributes['stream.duration_ms']), LogAttributes['stream'] = 'true') as stream_duration_ms` + customAttrProjection + `
	FROM otel_logs
	` + whereClause + `
	  AND LogAttributes['correlation_id'] != ''
	  AND LogAttributes['log_category'] = 'operational'
	  AND (
	    LogAttributes['event'] IN (
	      'gateway.request.received', 'gateway.request.completed',
	      'provider.request.issued', 'provider.response.received',
	      'command.completed', 'provider.error', 'gateway.error',
	      'function.execution.started', 'function.execution.completed', 'function.execution.error'
	    )
	    OR SeverityText IN ('ERROR', 'FATAL')
	  )
	GROUP BY correlation_id
	HAVING (
	    -- Terminal events — always show. gateway.request.completed is
	    -- the guaranteed end-of-request emitter (see chat.go defer);
	    -- it resolves PENDING rows even when the provider hung mid-stream
	    -- or never emitted provider.response.received.
	    argMax(LogAttributes['event'], Timestamp) IN ('gateway.request.completed', 'provider.response.received', 'command.completed', 'provider.error', 'gateway.error')
	    OR argMax(SeverityText, Timestamp) IN ('ERROR', 'FATAL')
	    -- In-flight requests within the last 5 minutes. Window was 60s
	    -- which dropped any request that hung (long provider call, broken
	    -- command bus, etc.) — those vanished from the UI even though the
	    -- rows existed in ClickHouse. Also accept gateway.request.received
	    -- so requests that never reached a provider still surface as
	    -- "processing" instead of disappearing entirely.
	    OR (
	        argMax(LogAttributes['event'], Timestamp) IN ('provider.request.issued', 'gateway.request.received')
	        AND max(Timestamp) >= now() - INTERVAL 5 MINUTE
	    )
	  )
	  AND (
	    -- Exclude catalog/config management operations
	    anyIf(LogAttributes['command_type'], LogAttributes['command_type'] != '') NOT IN ('UpsertModelFromCatalog', 'UpsertProviderFromCatalog', 'SyncCatalog', 'DeleteProvider', 'DeleteModel', 'UpdateProviderConfig')
	    OR anyIf(LogAttributes['command_type'], LogAttributes['command_type'] != '') = ''
	  )
	ORDER BY last_timestamp DESC
	LIMIT ? OFFSET ?
	`

	limit := logsQuery.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}

	offset := logsQuery.Offset
	if offset < 0 {
		offset = 0
	}

	// Build query args based on whether 'to' and tenant filtering is provided.
	// Custom-attribute projection params come first (they sit in the SELECT,
	// ahead of the WHERE params).
	queryArgs := append([]interface{}{}, customAttrSelectArgs...)
	queryArgs = append(queryArgs, from.UTC().Format("2006-01-02 15:04:05"))
	if hasTo {
		queryArgs = append(queryArgs, to.UTC().Format("2006-01-02 15:04:05"))
	}
	if tenantID != "" {
		queryArgs = append(queryArgs, tenantID)
	}
	queryArgs = append(queryArgs, limit, offset)

	var rows *sqlx.Rows
	var err error
	rows, err = h.clickhouse.QueryxContext(ctx, querySQL, queryArgs...)
	if err != nil {
		// Context canceled is expected when client disconnects (e.g., historical mode or mode switch)
		if ctx.Err() != nil {
			logger.WithFields(
				"query_type", logsQuery.QueryType(),
				"correlation_id", correlationID,
			).Debug("query canceled by client (expected in historical mode)")
			return nil, ctx.Err()
		}

		logger.WithFields(
			"query_type", logsQuery.QueryType(),
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("failed to execute list logs query")
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}
	defer rows.Close()

	logs := make([]query.RequestLogReadModel, 0)
	for rows.Next() {
		var log query.RequestLogReadModel
		var payload, firstPayload, providerRequestPayload string
		var providerResponsePayload, providerErrorPayload, commandCompletedPayload string
		var streamInt uint8            // ClickHouse returns 0 or 1 as UInt8
		var fallbackInt uint8          // ClickHouse returns 0 or 1 as UInt8
		var attemptedModelsJSON string // ClickHouse array as JSON string

		// Streaming metrics fields
		var streamTtftMs int64
		var streamChunkCount int32
		var streamAvgChunkLatencyMs float64
		var streamMaxChunkLatencyMs int64
		var streamTokensPerSecond float64
		var streamDurationMs int64

		var customAttrVals map[string]string
		scanTargets := []interface{}{
			&log.CorrelationID,
			&log.Timestamp,
			&log.FirstTimestamp,
			&log.CommandType,
			&log.Provider,
			&log.Model,
			&log.LatencyMs,
			&log.LogEvent,
			&payload,
			&firstPayload,
			&providerRequestPayload,
			&providerResponsePayload,
			&providerErrorPayload,
			&commandCompletedPayload,
			&streamInt,
			&log.TraceID,
			&log.SpanID,
			&log.Severity,
			&log.TenantID,
			&log.TenantType,
			&log.RequestedModel,
			&log.ServedModel,
			&attemptedModelsJSON,
			&fallbackInt,
			&log.AttemptCount,
			&streamTtftMs,
			&streamChunkCount,
			&streamAvgChunkLatencyMs,
			&streamMaxChunkLatencyMs,
			&streamTokensPerSecond,
			&streamDurationMs,
		}
		// The custom_attr_columns map column is present only when the query
		// projected it; scan it into the read model when so.
		if len(logsQuery.CustomAttrColumns) > 0 {
			customAttrVals = map[string]string{}
			scanTargets = append(scanTargets, &customAttrVals)
		}
		if err := rows.Scan(scanTargets...); err != nil {
			continue
		}
		if len(customAttrVals) > 0 {
			log.CustomAttrValues = customAttrVals
		}

		// Convert uint8 to bool
		log.Stream = streamInt != 0
		log.FallbackOccurred = fallbackInt != 0

		// Populate streaming metrics if this was a streaming request
		if log.Stream && (streamChunkCount > 0 || streamTtftMs > 0) {
			log.StreamingMetrics = &query.StreamingMetricsReadModel{
				TtftMs:            streamTtftMs,
				ChunkCount:        int(streamChunkCount),
				AvgChunkLatencyMs: streamAvgChunkLatencyMs,
				MaxChunkLatencyMs: streamMaxChunkLatencyMs,
				TokensPerSecond:   streamTokensPerSecond,
				StreamDurationMs:  streamDurationMs,
			}
		}

		// Parse attempted models array from ClickHouse
		if attemptedModelsJSON != "" && attemptedModelsJSON != "[]" && attemptedModelsJSON != "['']" {
			// ClickHouse toString(groupArray) returns string like "['model1','model2']"
			// Convert to JSON format by replacing single quotes with double quotes
			jsonStr := strings.ReplaceAll(attemptedModelsJSON, "'", "\"")

			var models []string
			if err := json.Unmarshal([]byte(jsonStr), &models); err == nil {
				// Filter out empty strings
				filteredModels := make([]string, 0, len(models))
				for _, m := range models {
					if m != "" {
						filteredModels = append(filteredModels, m)
					}
				}
				log.AllAttemptedModels = filteredModels
			}
		}

		// Ensure Model field is set for backward compatibility (use ServedModel)
		if log.Model == "" && log.ServedModel != "" {
			log.Model = log.ServedModel
		}
		// Ensure ServedModel is set if Model exists but ServedModel doesn't
		if log.ServedModel == "" && log.Model != "" {
			log.ServedModel = log.Model
		}

		// Default values
		var promptTokens, completionTokens int64
		var inputText, responseText string

		// Extract user input from provider request payload (provider.request.issued) - PRIMARY SOURCE
		if providerRequestPayload != "" {
			var providerReqData map[string]interface{}
			if err := json.Unmarshal([]byte(providerRequestPayload), &providerReqData); err == nil {
				// Extract provider name if not already set
				if log.Provider == "" {
					if providerRaw, ok := providerReqData["provider"]; ok {
						if providerMap, ok := providerRaw.(map[string]interface{}); ok {
							if providerName, ok := providerMap["gateway.provider.name"].(string); ok {
								log.Provider = providerName
							}
						}
					}
				}

				if requestRaw, ok := providerReqData["request"]; ok {
					if requestMap, ok := requestRaw.(map[string]interface{}); ok {
						if input, ok := requestMap["user_input"].(string); ok {
							inputText = input
						}
						// Extract model from request if not already set
						if log.Model == "" {
							if model, ok := requestMap["model"].(string); ok {
								log.Model = model
							}
						}
					}
				}
			}
		} else {
			// If provider_request_payload is empty, log what first_payload looks like
			if firstPayload != "" {
				preview := firstPayload
				if len(preview) > 200 {
					preview = preview[:200] + "..."
				}
			}
		}

		// Fallback: Extract user input from first payload (gateway.request.received)
		if inputText == "" && firstPayload != "" {
			var firstData map[string]interface{}
			if err := json.Unmarshal([]byte(firstPayload), &firstData); err == nil {
				// Extract command_type from first payload if not already set
				if log.CommandType == "" {
					if gatewayRaw, ok := firstData["gateway"]; ok {
						if gatewayMap, ok := gatewayRaw.(map[string]interface{}); ok {
							if procType, ok := gatewayMap["gateway.procedure.type"].(string); ok {
								log.CommandType = procType
							}
						}
					}
				}

				// Try multiple paths for user input
				if requestRaw, ok := firstData["request"]; ok {
					if requestMap, ok := requestRaw.(map[string]interface{}); ok {
						// Try user_input field
						if input, ok := requestMap["user_input"].(string); ok {
							inputText = input
						} else if input, ok := requestMap["gateway.request.input"].(string); ok {
							// Fallback to gateway.request.input
							inputText = input
						}
					}
				}
			}
		}

		// PRIMARY response source: provider.response.received payload.
		// argMax(payload, Timestamp) gets the terminal event's payload,
		// which is gateway.request.completed — and that event emits no
		// payload, so the legacy read-path stayed blank for successful
		// calls. Pull from dedicated event payloads in priority order:
		// success → command summary → provider error → first-payload
		// fallback.
		extractResponseText := func(raw string) string {
			if raw == "" {
				return ""
			}
			var d map[string]interface{}
			if err := json.Unmarshal([]byte(raw), &d); err != nil {
				return ""
			}
			respRaw, ok := d["response"].(map[string]interface{})
			if !ok {
				return ""
			}
			if s, ok := respRaw["response_text"].(string); ok && s != "" {
				return s
			}
			if s, ok := respRaw["chatbot_output"].(string); ok && s != "" {
				return s
			}
			return ""
		}
		if v := extractResponseText(providerResponsePayload); v != "" {
			responseText = v
		} else if v := extractResponseText(commandCompletedPayload); v != "" {
			responseText = v
		}
		// Provider error: surface partial_response_on_error when present;
		// otherwise the error message itself populates responseText below.
		if responseText == "" && providerErrorPayload != "" {
			var ed map[string]interface{}
			if err := json.Unmarshal([]byte(providerErrorPayload), &ed); err == nil {
				if sm, ok := ed["streaming_metrics"].(map[string]interface{}); ok {
					if pr, ok := sm["partial_response_on_error"].(string); ok && pr != "" {
						responseText = pr
					}
				}
			}
		}

		if payload != "" {
			var data map[string]interface{}
			if err := json.Unmarshal([]byte(payload), &data); err == nil {
				// Last resort: Try to extract user input from last payload if still empty
				if inputText == "" {
					if requestRaw, ok := data["request"]; ok {
						if requestMap, ok := requestRaw.(map[string]interface{}); ok {
							if input, ok := requestMap["user_input"].(string); ok {
								inputText = input
							}
						}
					}
				}

				// Extract model and command_type from error payload if not available
				if log.Model == "" {
					if errorRaw, ok := data["error"]; ok {
						if errorMap, ok := errorRaw.(map[string]interface{}); ok {
							if model, ok := errorMap["gateway.error.model"].(string); ok {
								log.Model = model
							}
						}
					}
				}

				// Extract command_type from payload if not in LogAttributes
				if log.CommandType == "" {
					if cmdType, ok := data["command_type"].(string); ok {
						log.CommandType = cmdType
					}
				}

				// Extract gateway.procedure.type from error payloads (e.g., model not found)
				if log.CommandType == "" {
					if gatewayRaw, ok := data["gateway"]; ok {
						if gatewayMap, ok := gatewayRaw.(map[string]interface{}); ok {
							if procType, ok := gatewayMap["gateway.procedure.type"].(string); ok {
								log.CommandType = procType
							}
						}
					}
				}

				// Extract usage/tokens
				if usageRaw, ok := data["usage"]; ok {
					if usageMap, ok := usageRaw.(map[string]interface{}); ok {
						// Try new format first
						if input, ok := usageMap["input_tokens"].(float64); ok {
							promptTokens = int64(input)
						}
						if output, ok := usageMap["output_tokens"].(float64); ok {
							completionTokens = int64(output)
						}
						// Fallback to old format
						if promptTokens == 0 {
							if p, ok := usageMap["prompt"].(float64); ok {
								promptTokens = int64(p)
							}
						}
						if completionTokens == 0 {
							if c, ok := usageMap["completion"].(float64); ok {
								completionTokens = int64(c)
							}
						}
					}
				}

				// Extract provider name from payload if not already set
				if log.Provider == "" {
					if providerRaw, ok := data["provider"]; ok {
						if providerMap, ok := providerRaw.(map[string]interface{}); ok {
							if providerName, ok := providerMap["gateway.provider.name"].(string); ok {
								log.Provider = providerName
							}
						}
					}
				}

				// Extract response text (provider response) or error message
				if responseRaw, ok := data["response"]; ok {
					if responseMap, ok := responseRaw.(map[string]interface{}); ok {
						if resp, ok := responseMap["response_text"].(string); ok {
							responseText = resp
						} else if resp, ok := responseMap["chatbot_output"].(string); ok {
							responseText = resp
						}

						// Extract model from response if not already set
						if log.Model == "" {
							if model, ok := responseMap["model"].(string); ok {
								log.Model = model
							}
						}
					}
				}

				// If no response, check for error message
				if responseText == "" {
					if errorRaw, ok := data["error"]; ok {
						if errorMap, ok := errorRaw.(map[string]interface{}); ok {
							// Try new format first
							if errMsg, ok := errorMap["message"].(string); ok {
								responseText = errMsg
							} else if errMsg, ok := errorMap["gateway.error.message"].(string); ok {
								responseText = errMsg
							}
						}
					}
				}

				// Extract input text
				if requestRaw, ok := data["request"]; ok {
					if requestMap, ok := requestRaw.(map[string]interface{}); ok {
						if input, ok := requestMap["user_input"].(string); ok {
							inputText = input
						}
					}
				}

				// Fallback: estimate tokens if usage not available
				const avgTokenLength = 4
				if promptTokens == 0 && inputText != "" {
					promptTokens = int64(len(inputText) / avgTokenLength)
					if promptTokens == 0 {
						promptTokens = 1
					}
				}
				if completionTokens == 0 && responseText != "" {
					completionTokens = int64(len(responseText) / avgTokenLength)
					if completionTokens == 0 {
						completionTokens = 1
					}
				}

				// Extract model if missing
				if log.Model == "" {
					if provider, ok := data["provider"].(map[string]interface{}); ok {
						if model, ok := provider["gateway.provider.model"].(string); ok && model != "" {
							log.Model = model
						}
					}
				}

				// Extract command type if missing
				if log.CommandType == "" {
					if command, ok := data["command"].(map[string]interface{}); ok {
						if cmdType, ok := command["gateway.command.type"].(string); ok && cmdType != "" {
							log.CommandType = cmdType
						}
					}
					if log.CommandType == "" {
						if requestMap, ok := data["request"].(map[string]interface{}); ok {
							if endpoint, ok := requestMap["gateway.request.endpoint"].(string); ok && endpoint != "" {
								switch endpoint {
								case "ChatCompletion":
									log.CommandType = "ProcessChat"
								case "Embeddings":
									log.CommandType = "ProcessEmbedding"
								default:
									log.CommandType = endpoint
								}
							}
						}
					}
				}

				// Extract streaming metrics from payload (for chunk_timeline and partial_response_on_error)
				if log.Stream {
					if streamingMetricsRaw, ok := data["streaming_metrics"]; ok {
						if streamingMap, ok := streamingMetricsRaw.(map[string]interface{}); ok {
							// Initialize streaming metrics if not already set
							if log.StreamingMetrics == nil {
								log.StreamingMetrics = &query.StreamingMetricsReadModel{}
							}

							// Extract partial response on error
							if partialResp, ok := streamingMap["partial_response_on_error"].(string); ok && partialResp != "" {
								log.StreamingMetrics.PartialResponseOnError = partialResp
							}

							// Extract chunk timeline if available
							if chunkTimelineRaw, ok := streamingMap["chunk_timeline"].([]interface{}); ok && len(chunkTimelineRaw) > 0 {
								log.StreamingMetrics.ChunkTimeline = make([]query.ChunkMetadataReadModel, 0, len(chunkTimelineRaw))
								for _, chunkRaw := range chunkTimelineRaw {
									if chunkMap, ok := chunkRaw.(map[string]interface{}); ok {
										chunk := query.ChunkMetadataReadModel{}
										if idx, ok := chunkMap["index"].(float64); ok {
											chunk.Index = int(idx)
										}
										if ts, ok := chunkMap["timestamp_ms"].(float64); ok {
											chunk.TimestampMs = int64(ts)
										}
										if lat, ok := chunkMap["latency_ms"].(float64); ok {
											chunk.LatencyMs = int64(lat)
										}
										if tc, ok := chunkMap["token_count"].(float64); ok {
											chunk.TokenCount = int(tc)
										}
										if ct, ok := chunkMap["cumulative_tokens"].(float64); ok {
											chunk.CumulativeTokens = int(ct)
										}
										log.StreamingMetrics.ChunkTimeline = append(log.StreamingMetrics.ChunkTimeline, chunk)
									}
								}
							}

							// Backfill streaming metrics from payload if not already populated from LogAttributes
							if log.StreamingMetrics.TtftMs == 0 {
								if ttft, ok := streamingMap["ttft_ms"].(float64); ok {
									log.StreamingMetrics.TtftMs = int64(ttft)
								}
							}
							if log.StreamingMetrics.ChunkCount == 0 {
								if chunkCount, ok := streamingMap["chunk_count"].(float64); ok {
									log.StreamingMetrics.ChunkCount = int(chunkCount)
								}
							}
							if log.StreamingMetrics.AvgChunkLatencyMs == 0 {
								if avg, ok := streamingMap["avg_chunk_latency_ms"].(float64); ok {
									log.StreamingMetrics.AvgChunkLatencyMs = avg
								}
							}
							if log.StreamingMetrics.MaxChunkLatencyMs == 0 {
								if maxLat, ok := streamingMap["max_chunk_latency_ms"].(float64); ok {
									log.StreamingMetrics.MaxChunkLatencyMs = int64(maxLat)
								}
							}
							if log.StreamingMetrics.TokensPerSecond == 0 {
								if tps, ok := streamingMap["tokens_per_second"].(float64); ok {
									log.StreamingMetrics.TokensPerSecond = tps
								}
							}
							if log.StreamingMetrics.StreamDurationMs == 0 {
								if dur, ok := streamingMap["stream_duration_ms"].(float64); ok {
									log.StreamingMetrics.StreamDurationMs = int64(dur)
								}
							}
						}
					}
				}
			}
		}

		log.PromptTokens = promptTokens
		log.CompletionTokens = completionTokens
		log.TotalTokens = promptTokens + completionTokens
		log.Cost = h.computeCost(log.Provider, log.Model, promptTokens, completionTokens)
		log.Status = mapEventToStatus(log.LogEvent)
		log.RequestText = inputText
		log.ResponseText = responseText
		log.Payload = payload

		logs = append(logs, log)
	}

	// Fetch function executions for all logs
	if len(logs) > 0 {
		correlationIDs := make([]string, len(logs))
		for i, log := range logs {
			correlationIDs[i] = log.CorrelationID
		}

		// Determine time range for function executions query
		funcFrom := from
		funcTo := to
		if funcTo.IsZero() {
			funcTo = time.Now()
		}

		logger.WithFields(
			"correlation_ids_count", len(correlationIDs),
			"func_from", funcFrom.Format(time.RFC3339),
			"func_to", funcTo.Format(time.RFC3339),
		).Debug("fetching function executions for logs")

		functionExecs, err := h.fetchFunctionExecutions(ctx, correlationIDs, funcFrom, funcTo)
		if err != nil {
			logger.WithFields(
				"error", err.Error(),
				"correlation_id", correlationID,
			).Warn("failed to fetch function executions")
		} else if functionExecs != nil {
			logger.WithFields(
				"function_execs_count", len(functionExecs),
				"correlation_ids", correlationIDs,
			).Debug("fetched function executions")

			// Attach function executions to their respective logs
			attachedCount := 0
			for i := range logs {
				if execs, ok := functionExecs[logs[i].CorrelationID]; ok {
					logs[i].FunctionExecutions = execs
					attachedCount++
					logger.WithFields(
						"correlation_id", logs[i].CorrelationID,
						"exec_count", len(execs),
					).Debug("attached function executions to log")
				}
			}
			logger.WithFields(
				"total_logs", len(logs),
				"attached_count", attachedCount,
			).Debug("function execution attachment complete")
		}
	}

	logger.WithFields(
		"query_type", logsQuery.QueryType(),
		"result_count", len(logs),
		"correlation_id", correlationID,
	).Debug("list logs query executed successfully")

	return logs, nil
}

// computeCost calculates the cost based on provider catalog rates
func (h *LogsQueryHandler) computeCost(provider, model string, promptTokens, completionTokens int64) float64 {
	if h.catalog == nil {
		return 0.0
	}

	catalogEntry, exists := h.catalog.GetProvider(provider)
	if !exists || catalogEntry == nil {
		return 0.0
	}

	for _, m := range catalogEntry.Models {
		// Exact match first
		if m.Name == model {
			inputCost := (float64(promptTokens) / 1000.0) * m.InputCostPer1K
			outputCost := (float64(completionTokens) / 1000.0) * m.OutputCostPer1K
			return inputCost + outputCost
		}
	}

	// Fallback: Try prefix match for versioned models (e.g., gpt-4o-mini-2024-07-18 → gpt-4o-mini)
	for _, m := range catalogEntry.Models {
		if len(model) > len(m.Name) && model[:len(m.Name)] == m.Name {
			// Check if the remaining part looks like a version (starts with -)
			remaining := model[len(m.Name):]
			if len(remaining) > 0 && remaining[0] == '-' {
				inputCost := (float64(promptTokens) / 1000.0) * m.InputCostPer1K
				outputCost := (float64(completionTokens) / 1000.0) * m.OutputCostPer1K
				return inputCost + outputCost
			}
		}
	}

	return 0.0
}

func mapEventToStatus(event string) string {
	switch event {
	case "gateway.request.completed", "command.completed", "provider.response.received":
		return "success"
	case "provider.error", "gateway.error":
		return "error"
	case "provider.request.issued":
		return "processing"
	default:
		return "pending"
	}
}

// fetchFunctionExecutions retrieves function execution logs for the given correlation IDs
func (h *LogsQueryHandler) fetchFunctionExecutions(ctx context.Context, correlationIDs []string, from, to time.Time) (map[string][]query.FunctionExecutionReadModel, error) {
	if len(correlationIDs) == 0 {
		return nil, nil
	}

	// Build query to fetch function execution events
	// Note: The function payload has keys with dots like "function.id", so we
	// fetch the raw payload and parse it in Go.
	// We check both 'log_payload'/'log_event' (logrus format) and 'payload'/'event' (normalized OTEL)
	// Resolve tenant for filtering. Same fail-closed contract as the
	// top-level handler — without a tenant schema the function-execution
	// joiner would otherwise return foreign tenants' rows.
	funcTenantID := database.TenantSchemaFromContext(ctx)
	if funcTenantID == "" {
		return map[string][]query.FunctionExecutionReadModel{}, nil
	}
	tenantFilter := "AND LogAttributes['tenant_id'] = ? "

	querySQL := `
	SELECT
		LogAttributes['correlation_id'] as correlation_id,
		COALESCE(
			nullIf(LogAttributes['log_payload'], ''),
			nullIf(LogAttributes['payload'], ''),
			''
		) as payload
	FROM otel_logs
	WHERE Timestamp >= parseDateTimeBestEffort(?)
	  AND Timestamp <= parseDateTimeBestEffort(?)
	  ` + tenantFilter + `
	  AND LogAttributes['correlation_id'] IN (?)
	  AND (
	    LogAttributes['log_category'] = 'operational'
	    OR LogAttributes['category'] = 'operational'
	  )
	  AND (
	    LogAttributes['log_event'] IN ('function.execution.completed', 'function.execution.error')
	    OR LogAttributes['event'] IN ('function.execution.completed', 'function.execution.error')
	  )
	ORDER BY Timestamp ASC
	`

	// Build the IN clause for correlation IDs
	placeholders := make([]string, len(correlationIDs))
	args := make([]interface{}, 0, len(correlationIDs)+3)
	args = append(args, from.UTC().Format("2006-01-02 15:04:05"))
	args = append(args, to.UTC().Format("2006-01-02 15:04:05"))
	if funcTenantID != "" {
		args = append(args, funcTenantID)
	}

	for i, id := range correlationIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}

	// Replace the single ? with the actual placeholders
	querySQL = strings.Replace(querySQL, "IN (?)", "IN ("+strings.Join(placeholders, ",")+")", 1)

	rows, err := h.clickhouse.QueryxContext(ctx, querySQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string][]query.FunctionExecutionReadModel)
	rowCount := 0
	for rows.Next() {
		var correlationID string
		var payload string

		if err := rows.Scan(&correlationID, &payload); err != nil {
			logger.WithFields("error", err.Error()).Warn("failed to scan function execution row")
			continue
		}
		rowCount++

		// Parse the payload JSON to extract function execution details
		exec := parseFunctionExecutionPayload(payload)
		if exec != nil {
			result[correlationID] = append(result[correlationID], *exec)
		} else {
			logger.WithFields("correlation_id", correlationID).Debug("parseFunctionExecutionPayload returned nil")
		}
	}

	logger.WithFields("row_count", rowCount, "result_count", len(result)).Debug("fetchFunctionExecutions completed")
	return result, nil
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// parseFunctionExecutionPayload extracts function execution data from the log payload
func parseFunctionExecutionPayload(payload string) *query.FunctionExecutionReadModel {
	if payload == "" {
		return nil
	}

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &data); err != nil {
		return nil
	}

	// Extract the function object
	functionRaw, ok := data["function"]
	if !ok {
		return nil
	}

	functionMap, ok := functionRaw.(map[string]interface{})
	if !ok {
		return nil
	}

	exec := &query.FunctionExecutionReadModel{}

	// Extract fields with dot-notation keys
	if v, ok := functionMap["function.id"].(string); ok {
		exec.FunctionID = v
	}
	if v, ok := functionMap["function.name"].(string); ok {
		exec.FunctionName = v
	}
	if v, ok := functionMap["function.runtime"].(string); ok {
		exec.Runtime = v
	}
	if v, ok := functionMap["function.backend"].(string); ok {
		exec.Backend = v
	}
	if v, ok := functionMap["function.execution_mode"].(string); ok {
		exec.ExecutionMode = v
	}
	if v, ok := functionMap["function.duration_ms"].(float64); ok {
		exec.DurationMs = int64(v)
	}
	if v, ok := functionMap["function.success"].(bool); ok {
		exec.Success = v
	}
	if v, ok := functionMap["function.error"].(string); ok {
		exec.Error = v
	}
	if v, ok := functionMap["function.error_type"].(string); ok {
		exec.ErrorType = v
	}
	if v, ok := functionMap["function.stdout"].(string); ok {
		exec.Stdout = v
	}
	if v, ok := functionMap["function.stderr"].(string); ok {
		exec.Stderr = v
	}

	// Only return if we have at least a function ID
	if exec.FunctionID == "" {
		return nil
	}

	return exec
}

// ListLogs helper executes a list logs query
func ListLogs(ctx context.Context, queryBus query.QueryBus, from, to time.Time, apiKey, userID string, limit, offset int, cols ...CustomAttrColumn) ([]query.RequestLogReadModel, error) {
	q := NewListLogsQuery(from, to, apiKey, userID)
	q.Limit = limit
	q.Offset = offset
	q.CustomAttrColumns = cols
	result, err := queryBus.Execute(ctx, q)
	if err != nil {
		return nil, err
	}

	if response, ok := result.(*query.Response); ok {
		if logs, ok := response.Data.([]query.RequestLogReadModel); ok {
			return logs, nil
		}
	}

	return nil, fmt.Errorf("unexpected result type from list logs query")
}
