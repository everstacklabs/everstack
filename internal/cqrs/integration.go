// Package cqrs provides integration and setup for the CQRS system.
package cqrs

import (
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/everstacklabs/everstack/internal/commands"
	agentsHandler "github.com/everstacklabs/everstack/internal/commands/handlers/agents"
	annotationsHandler "github.com/everstacklabs/everstack/internal/commands/handlers/annotations"
	apiKeyHandler "github.com/everstacklabs/everstack/internal/commands/handlers/api_key"
	datasetsHandler "github.com/everstacklabs/everstack/internal/commands/handlers/datasets"
	functionsHandler "github.com/everstacklabs/everstack/internal/commands/handlers/functions"
	chatHandler "github.com/everstacklabs/everstack/internal/commands/handlers/gateway/chat"
	embeddingHandler "github.com/everstacklabs/everstack/internal/commands/handlers/gateway/embedding"
	lbHandler "github.com/everstacklabs/everstack/internal/commands/handlers/gateway/load_balancer"
	modelsHandler "github.com/everstacklabs/everstack/internal/commands/handlers/gateway/models"
	licenseHandler "github.com/everstacklabs/everstack/internal/commands/handlers/license"
	promptsHandler "github.com/everstacklabs/everstack/internal/commands/handlers/prompts"
	storageHandler "github.com/everstacklabs/everstack/internal/commands/handlers/storage"
	troopersHandler "github.com/everstacklabs/everstack/internal/commands/handlers/troopers"
	usageHandler "github.com/everstacklabs/everstack/internal/commands/handlers/usage"
	workflowsHandler "github.com/everstacklabs/everstack/internal/commands/handlers/workflows"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/database/dialect"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	queryPkg "github.com/everstacklabs/everstack/internal/query"
	agentsQueryHandler "github.com/everstacklabs/everstack/internal/query/handlers/agents"
	annotationsQueryHandler "github.com/everstacklabs/everstack/internal/query/handlers/annotations"
	apiKeyQueryHandler "github.com/everstacklabs/everstack/internal/query/handlers/api_key"
	datasetsQueryHandler "github.com/everstacklabs/everstack/internal/query/handlers/datasets"
	functionsQueryHandler "github.com/everstacklabs/everstack/internal/query/handlers/functions"
	lbStatsHandler "github.com/everstacklabs/everstack/internal/query/handlers/gateway/load_balancer"
	modelConfigHandler "github.com/everstacklabs/everstack/internal/query/handlers/gateway/models"
	licenseQueryHandler "github.com/everstacklabs/everstack/internal/query/handlers/license"
	logsQueryHandler "github.com/everstacklabs/everstack/internal/query/handlers/logs"
	analyticsHandler "github.com/everstacklabs/everstack/internal/query/handlers/metrics/analytics"
	errorRatesHandler "github.com/everstacklabs/everstack/internal/query/handlers/metrics/error_rates"
	realTimeHandler "github.com/everstacklabs/everstack/internal/query/handlers/metrics/real_time"
	promptsQueryHandler "github.com/everstacklabs/everstack/internal/query/handlers/prompts"
	storageQueryHandler "github.com/everstacklabs/everstack/internal/query/handlers/storage"
	tracesQueryHandler "github.com/everstacklabs/everstack/internal/query/handlers/traces"
	enhancedHandler "github.com/everstacklabs/everstack/internal/query/handlers/traces/enhanced"
	troopersQueryHandler "github.com/everstacklabs/everstack/internal/query/handlers/troopers"
	workflowsQueryHandler "github.com/everstacklabs/everstack/internal/query/handlers/workflows"
	"github.com/everstacklabs/everstack/internal/services/provider_catalog"
	"github.com/jmoiron/sqlx"
)

// System represents a complete CQRS system with command/query sides.
type System struct {
	CommandBus        commands.CommandBus
	QueryBus          queryPkg.QueryBus
	EventBus          *database.InMemoryEventBus
	ProjectionManager *database.ProjectionManager
	Writer            database.Writer
	ConnType          database.Type
	AnalyticsDB       *sqlx.DB
}

// NewSystem creates and configures a complete CQRS system.
func NewSystem(ctx context.Context, conn *database.Conn) (*System, error) {
	// Create event bus
	eventBus := database.NewInMemoryEventBus()

	// Create writer based on connection type
	var writer database.Writer
	if conn.RW != nil {
		// Select SQL dialect so inserts use correct placeholders/syntax
		var d dialect.Dialect
		switch conn.Type {
		case database.TypePostgres:
			d = dialect.Postgres{}
		case database.TypeClickHouse:
			d = dialect.ClickHouse{}
		}
		writer = database.NewSQLWriter(conn, d)
	} else {
		// Memory writer for testing/development
		writer = database.NewMemoryWriter()
	}

	// Create command bus
	commandBus := commands.NewCommandBus(writer, eventBus)

	// Create query bus
	queryBus := queryPkg.NewQueryBus()

	// Create projection manager (transactional projections only; Postgres)
	var projectionManager *database.ProjectionManager
	if conn.RW != nil && conn.Type == database.TypePostgres {
		projectionManager = database.NewProjectionManager(conn.RW, eventBus)
	}

	system := &System{
		CommandBus:        commandBus,
		QueryBus:          queryBus,
		EventBus:          eventBus,
		ProjectionManager: projectionManager,
		Writer:            writer,
		ConnType:          conn.Type,
	}

	// Register default handlers
	if err := system.registerDefaultHandlers(conn.RW); err != nil {
		return nil, fmt.Errorf("failed to register default handlers: %w", err)
	}

	// Initialize projections (if available)
	if projectionManager != nil {
		if err := projectionManager.Initialize(ctx); err != nil {
			return nil, fmt.Errorf("failed to initialize projections: %w", err)
		}
	}

	return system, nil
}

// NewHybridSystem creates a CQRS system with a primary transactional connection
// (typically Postgres) and an optional analytics DB (typically ClickHouse).
func NewHybridSystem(ctx context.Context, primary *database.Conn, analytics *sqlx.DB) (*System, error) {
	// Create event bus
	eventBus := database.NewInMemoryEventBus()

	// Create primary writer with appropriate dialect
	var primaryDialect dialect.Dialect
	switch primary.Type {
	case database.TypePostgres:
		primaryDialect = dialect.Postgres{}
	case database.TypeClickHouse:
		primaryDialect = dialect.ClickHouse{}
	}
	primaryWriter := database.NewSQLWriter(primary, primaryDialect)

	// If analytics DB provided, use ClickHouse writer exclusively (no Postgres writes in hybrid)
	var writer database.Writer = primaryWriter
	if analytics != nil {
		chConn := &database.Conn{Type: database.TypeClickHouse, RW: analytics}
		chWriter := database.NewSQLWriter(chConn, dialect.ClickHouse{})
		writer = chWriter
	}

	// Create buses
	commandBus := commands.NewCommandBus(writer, eventBus)
	queryBus := queryPkg.NewQueryBus()

	// Projections only on transactional store (Postgres)
	var projectionManager *database.ProjectionManager
	if primary.RW != nil && primary.Type == database.TypePostgres {
		projectionManager = database.NewProjectionManager(primary.RW, eventBus)
	}

	system := &System{
		CommandBus:        commandBus,
		QueryBus:          queryBus,
		EventBus:          eventBus,
		ProjectionManager: projectionManager,
		Writer:            writer,
		ConnType:          primary.Type,
		AnalyticsDB:       analytics,
	}

	// Register handlers using primary DB for transactional reads
	if err := system.registerDefaultHandlers(primary.RW); err != nil {
		return nil, fmt.Errorf("failed to register default handlers: %w", err)
	}
	// Initialize projections (if available)
	if projectionManager != nil {
		if err := projectionManager.Initialize(ctx); err != nil {
			return nil, fmt.Errorf("failed to initialize projections: %w", err)
		}
	}
	return system, nil
}

// registerDefaultHandlers registers the default command and query handlers.
func (s *System) registerDefaultHandlers(db *sqlx.DB) error {
	// Register command handlers
	s.CommandBus.RegisterHandler(chatHandler.NewChatCommandHandler())
	s.CommandBus.RegisterHandler(embeddingHandler.NewEmbeddingCommandHandler())
	s.CommandBus.RegisterHandler(modelsHandler.NewModelConfigCommandHandler())
	s.CommandBus.RegisterHandler(lbHandler.NewLoadBalancerCommandHandler())
	// API key commands are transactional; only on Postgres/memory
	if s.ConnType == database.TypePostgres || s.ConnType == database.TypeMemory {
		s.CommandBus.RegisterHandler(apiKeyHandler.NewApiKeyCommandHandler())
		s.CommandBus.RegisterHandler(licenseHandler.NewLicenseCommandHandler())
		s.CommandBus.RegisterHandler(functionsHandler.NewFunctionsCommandHandler())
		s.CommandBus.RegisterHandler(workflowsHandler.NewWorkflowsCommandHandler())
		s.CommandBus.RegisterHandler(agentsHandler.NewAgentsCommandHandler(db))
		s.CommandBus.RegisterHandler(usageHandler.NewUsageCommandHandler())
		s.CommandBus.RegisterHandler(storageHandler.NewStorageCommandHandler())
		s.CommandBus.RegisterHandler(annotationsHandler.NewAnnotationsCommandHandler(db))
		s.CommandBus.RegisterHandler(datasetsHandler.NewDatasetsCommandHandler())
		s.CommandBus.RegisterHandler(datasetsHandler.NewEvalCommandHandler())
		s.CommandBus.RegisterHandler(promptsHandler.NewPromptsCommandHandler())
		s.CommandBus.RegisterHandler(troopersHandler.NewTroopersCommandHandler()) // Deprecated: trooper handlers kept as shims during migration to unified agents
	}

	// Register query handlers (only if we have a database connection)
	if db != nil {
		// Prefer analytics DB for analytics queries if provided (e.g., ClickHouse)
		analyticsDB := db
		analyticsDialect := "postgres"
		if s.AnalyticsDB != nil {
			analyticsDB = s.AnalyticsDB
			analyticsDialect = "clickhouse"
		}
		s.QueryBus.RegisterHandler(analyticsHandler.NewAnalyticsQueryHandler(analyticsDB, analyticsDialect))
		s.QueryBus.RegisterHandler(analyticsHandler.NewChatHistoryQueryHandler(analyticsDB, analyticsDialect))

		// Operational/query handlers remain on primary DB
		s.QueryBus.RegisterHandler(modelConfigHandler.NewModelConfigQueryHandler(db))
		s.QueryBus.RegisterHandler(lbStatsHandler.NewLoadBalancerStatsQueryHandler(db))
		s.QueryBus.RegisterHandler(errorRatesHandler.NewErrorRatesQueryHandler(analyticsDB, analyticsDialect))
		s.QueryBus.RegisterHandler(realTimeHandler.NewRealTimeMetricsQueryHandler(db))

		// Logs query handler (requires ClickHouse analytics DB - only register in hybrid mode)
		if s.AnalyticsDB != nil && analyticsDialect == "clickhouse" {
			// Initialize provider catalog for cost calculation
			catalog, err := provider_catalog.New()
			if err != nil {
				logger.WithError(err).Warn("failed to initialize provider catalog for logs handler")
			} else {
				s.QueryBus.RegisterHandler(logsQueryHandler.NewLogsQueryHandler(analyticsDB, catalog))
			}
		}

		// Traces query handlers (requires ClickHouse native connection)
		// Note: Trace handlers are registered separately when the native ClickHouse connection is available
		// This happens in the API server startup where we have access to the DSN
		// See cmd/serve/start_api.go for trace handler registration

		// API key queries are transactional; only on Postgres/memory
		if s.ConnType == database.TypePostgres || s.ConnType == database.TypeMemory {
			s.QueryBus.RegisterHandler(apiKeyQueryHandler.NewApiKeyQueryHandler(db))
			s.QueryBus.RegisterHandler(apiKeyQueryHandler.NewApiKeyByHashQueryHandler(db))
			s.QueryBus.RegisterHandler(apiKeyQueryHandler.NewListApiKeysQueryHandler(db))
			// License queries for gateway enforcement
			s.QueryBus.RegisterHandler(licenseQueryHandler.NewGetActiveInstanceIDHandler(db))
			s.QueryBus.RegisterHandler(licenseQueryHandler.NewGetInstanceCredentialsHandler(db))
			s.QueryBus.RegisterHandler(licenseQueryHandler.NewGetLicenseByInstanceIDHandler(db))
			// Functions query handlers for serverless functions management
			s.QueryBus.RegisterHandler(functionsQueryHandler.NewFunctionsQueryHandler(db))
			s.QueryBus.RegisterHandler(functionsQueryHandler.NewFunctionByNameQueryHandler(db))
			s.QueryBus.RegisterHandler(functionsQueryHandler.NewListFunctionsQueryHandler(db))
			// Workflows query handlers for pipeline/workflow management
			s.QueryBus.RegisterHandler(workflowsQueryHandler.NewWorkflowsQueryHandler(db))
			s.QueryBus.RegisterHandler(workflowsQueryHandler.NewListWorkflowsQueryHandler(db))
			// Agents query handlers for agent orchestration
			s.QueryBus.RegisterHandler(agentsQueryHandler.NewAgentByIDQueryHandler(db))
			s.QueryBus.RegisterHandler(agentsQueryHandler.NewAgentByNameQueryHandler(db))
			s.QueryBus.RegisterHandler(agentsQueryHandler.NewListAgentsQueryHandler(db))
			s.QueryBus.RegisterHandler(agentsQueryHandler.NewSessionByIDQueryHandler(db))
			s.QueryBus.RegisterHandler(agentsQueryHandler.NewListSessionsQueryHandler(db))
			s.QueryBus.RegisterHandler(agentsQueryHandler.NewApprovalReviewByIDQueryHandler(db))
			s.QueryBus.RegisterHandler(agentsQueryHandler.NewListApprovalReviewsQueryHandler(db))
			// Sandbox query handlers
			s.QueryBus.RegisterHandler(agentsQueryHandler.NewListSandboxInstancesQueryHandler(db))
			s.QueryBus.RegisterHandler(agentsQueryHandler.NewListSandboxExecutionsQueryHandler(db))
			s.QueryBus.RegisterHandler(agentsQueryHandler.NewListSandboxEventsQueryHandler(db))
			// Spawn tree query handlers
			s.QueryBus.RegisterHandler(agentsQueryHandler.NewGetSpawnTreeQueryHandler(db))
			s.QueryBus.RegisterHandler(agentsQueryHandler.NewListSpawnNodesQueryHandler(db))
			// Agent link & channel binding query handlers
			s.QueryBus.RegisterHandler(agentsQueryHandler.NewListAgentLinksQueryHandler(db))
			s.QueryBus.RegisterHandler(agentsQueryHandler.NewListAgentChannelBindingsQueryHandler(db))
			// Trooper query handlers — deprecated: kept as shims during migration to unified agents.
			// These will be removed once all clients have migrated to the unified Agent API.
			s.QueryBus.RegisterHandler(troopersQueryHandler.NewTrooperByIDQueryHandler(db))
			s.QueryBus.RegisterHandler(troopersQueryHandler.NewListTroopersQueryHandler(db))
			s.QueryBus.RegisterHandler(troopersQueryHandler.NewListTrooperLinksQueryHandler(db))
			s.QueryBus.RegisterHandler(troopersQueryHandler.NewListChannelBindingsQueryHandler(db))
			// Version history reads from the events table which lives in the writer's DB
			// (ClickHouse in hybrid mode, Postgres otherwise)
			// Storage query handlers
			s.QueryBus.RegisterHandler(storageQueryHandler.NewGetStorageConfigHandler(db))
			s.QueryBus.RegisterHandler(storageQueryHandler.NewListStorageConfigsHandler(db))
			s.QueryBus.RegisterHandler(storageQueryHandler.NewListStorageObjectsHandler(db))
			s.QueryBus.RegisterHandler(storageQueryHandler.NewGetStorageUsageHandler(db))
			// Annotation queue query handlers
			s.QueryBus.RegisterHandler(annotationsQueryHandler.NewGetQueueByIDHandler(db))
			s.QueryBus.RegisterHandler(annotationsQueryHandler.NewListQueuesHandler(db))
			s.QueryBus.RegisterHandler(annotationsQueryHandler.NewListQueueItemsHandler(db))
			s.QueryBus.RegisterHandler(annotationsQueryHandler.NewGetNextItemHandler(db))
			s.QueryBus.RegisterHandler(annotationsQueryHandler.NewGetQueueStatsHandler(db))
			// Datasets & Evaluations query handlers
			s.QueryBus.RegisterHandler(datasetsQueryHandler.NewGetDatasetByIDQueryHandler(db))
			s.QueryBus.RegisterHandler(datasetsQueryHandler.NewListDatasetsQueryHandler(db))
			s.QueryBus.RegisterHandler(datasetsQueryHandler.NewListDatasetItemsQueryHandler(db))
			s.QueryBus.RegisterHandler(datasetsQueryHandler.NewGetScoreConfigQueryHandler(db))
			s.QueryBus.RegisterHandler(datasetsQueryHandler.NewListScoreConfigsQueryHandler(db))
			s.QueryBus.RegisterHandler(datasetsQueryHandler.NewGetEvalRunQueryHandler(db))
			s.QueryBus.RegisterHandler(datasetsQueryHandler.NewListEvalRunsQueryHandler(db))
			s.QueryBus.RegisterHandler(datasetsQueryHandler.NewListEvalRunItemsQueryHandler(db))
			// Prompt library query handlers
			s.QueryBus.RegisterHandler(promptsQueryHandler.NewGetPromptQueryHandler(db))
			s.QueryBus.RegisterHandler(promptsQueryHandler.NewListPromptsQueryHandler(db))
			s.QueryBus.RegisterHandler(promptsQueryHandler.NewListPromptVersionsQueryHandler(db))
			s.QueryBus.RegisterHandler(promptsQueryHandler.NewGetPromptVersionQueryHandler(db))
			s.QueryBus.RegisterHandler(workflowsQueryHandler.NewWorkflowVersionHistoryQueryHandler(analyticsDB, analyticsDialect))
			s.QueryBus.RegisterHandler(workflowsQueryHandler.NewWorkflowAtVersionQueryHandler(analyticsDB, analyticsDialect))
		}
	}

	logger.Debug("CQRS handlers registered ", "command_handlers: ", len(s.CommandBus.(*commands.DefaultCommandBus).GetRegisteredHandlers()),
		"query_handlers: ", func() int {
			if db != nil {
				return len(s.QueryBus.(*queryPkg.DefaultQueryBus).GetRegisteredHandlers())
			}
			return 0
		}())
	return nil
}

// RegisterAnalyticsHandlers registers (or re-registers) analytics and log query
// handlers that require ClickHouse. This is called separately in shared gateway
// mode where the CQRS system is created with NewSystem (no AnalyticsDB) but a
// tenant-aware ClickHouse pool is available for reads.
func (s *System) RegisterAnalyticsHandlers(chDB *sqlx.DB) error {
	if chDB == nil {
		return fmt.Errorf("nil ClickHouse DB")
	}
	// Re-register analytics handlers with ClickHouse dialect (overrides Postgres ones)
	s.QueryBus.RegisterHandler(analyticsHandler.NewAnalyticsQueryHandler(chDB, "clickhouse"))
	s.QueryBus.RegisterHandler(analyticsHandler.NewChatHistoryQueryHandler(chDB, "clickhouse"))
	s.QueryBus.RegisterHandler(errorRatesHandler.NewErrorRatesQueryHandler(chDB, "clickhouse"))

	// Register logs handler (was skipped in registerDefaultHandlers because AnalyticsDB was nil)
	catalog, err := provider_catalog.New()
	if err != nil {
		logger.WithError(err).Warn("failed to initialize provider catalog for logs handler")
	} else {
		s.QueryBus.RegisterHandler(logsQueryHandler.NewLogsQueryHandler(chDB, catalog))
	}

	logger.Debug("Analytics/logs query handlers registered with ClickHouse")
	return nil
}

// RegisterTraceHandlers registers trace query handlers with a native ClickHouse connection
// This must be called separately after system initialization if trace support is needed
func (s *System) RegisterTraceHandlers(conn clickhouse.Conn) error {
	if conn == nil {
		return fmt.Errorf("nil ClickHouse connection")
	}

	s.QueryBus.RegisterHandler(tracesQueryHandler.NewListTracesHandler(conn))
	s.QueryBus.RegisterHandler(tracesQueryHandler.NewTraceByIDHandler(conn))
	s.QueryBus.RegisterHandler(tracesQueryHandler.NewGetTraceHandler(conn))
	s.QueryBus.RegisterHandler(tracesQueryHandler.NewTraceTreeHandler(conn))
	s.QueryBus.RegisterHandler(tracesQueryHandler.NewTraceLogsHandler(conn))
	s.QueryBus.RegisterHandler(tracesQueryHandler.NewTraceStatsHandler(conn))

	// Register enhanced observability handlers
	s.QueryBus.RegisterHandler(enhancedHandler.NewEnhancedObservationHandler(conn))
	s.QueryBus.RegisterHandler(enhancedHandler.NewPerformanceBreakdownHandler(conn))
	s.QueryBus.RegisterHandler(enhancedHandler.NewBatchAnalyticsHandler(conn))
	s.QueryBus.RegisterHandler(enhancedHandler.NewWorkflowMetricsHandler(conn))
	s.QueryBus.RegisterHandler(tracesQueryHandler.NewResourceUtilizationHandler(conn))

	// Register observability metrics/sessions/users handlers (Phase 3)
	s.QueryBus.RegisterHandler(tracesQueryHandler.NewMetricsDashboardHandler(conn))
	s.QueryBus.RegisterHandler(tracesQueryHandler.NewMetricsTimeSeriesHandler(conn))
	s.QueryBus.RegisterHandler(tracesQueryHandler.NewMetricsBreakdownHandler(conn))
	s.QueryBus.RegisterHandler(tracesQueryHandler.NewListSessionsHandler(conn))
	s.QueryBus.RegisterHandler(tracesQueryHandler.NewGetSessionHandler(conn))
	s.QueryBus.RegisterHandler(tracesQueryHandler.NewListUsersHandler(conn))
	s.QueryBus.RegisterHandler(tracesQueryHandler.NewGetUserHandler(conn))

	// Register outcome dashboard handlers (Phase 4)
	s.QueryBus.RegisterHandler(tracesQueryHandler.NewOutcomeDashboardHandler(conn))
	s.QueryBus.RegisterHandler(tracesQueryHandler.NewOutcomeTimeSeriesHandler(conn))

	logger.Debug("Trace query handlers registered with native ClickHouse connection")
	return nil
}

// Health returns the health status of the CQRS system.
func (s *System) Health() map[string]interface{} {
	health := map[string]interface{}{
		"event_bus_active": true,
		"writer_available": s.Writer != nil,
	}

	// Type assert to get access to specific methods
	if cmdBus, ok := s.CommandBus.(*commands.DefaultCommandBus); ok {
		health["command_handlers"] = len(cmdBus.GetRegisteredHandlers())
	}

	if queryBus, ok := s.QueryBus.(*queryPkg.DefaultQueryBus); ok {
		health["query_handlers"] = len(queryBus.GetRegisteredHandlers())
	}

	if s.EventBus != nil {
		health["active_subscriptions"] = len(s.EventBus.GetSubscriptions())
	}

	return health
}

// Shutdown gracefully shuts down the CQRS system.
func (s *System) Shutdown(ctx context.Context) error {
	logger.Info("shutting down CQRS system")

	// TODO: Implement graceful shutdown
	// - Stop accepting new commands/queries
	// - Wait for in-flight operations to complete
	// - Close database connections
	// - Clean up event subscriptions

	return nil
}
