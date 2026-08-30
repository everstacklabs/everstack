package v1

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/authzresource"
	datasetscmd "github.com/everstacklabs/everstack/internal/commands/handlers/datasets"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/enterprise"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/utils"
	"github.com/everstacklabs/everstack/internal/query"
	datasetsquery "github.com/everstacklabs/everstack/internal/query/handlers/datasets"
	eval_runner "github.com/everstacklabs/everstack/internal/services/eval_runner"
	datasetspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/datasets/v1"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// getSys retrieves the CQRS system from either request or server context.
func (s *DatasetServer) getSys(ctx context.Context) (*cqrs.System, error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}
	return sys, nil
}

// getTenantID returns the tenant id set by the auth middleware. The reqTenantID
// argument is intentionally ignored — it is client-controlled and accepting it
// would let any caller read another tenant's data, which is the cross-tenant
// leak class this helper now actively guards against. The argument is kept on
// the signature so existing call sites compile, but it is dropped on the floor
// here. Empty result means "request is unauthenticated"; callers must treat
// that as an error before issuing a query.
func getTenantID(ctx context.Context, _ string) string {
	return contextkeys.GetTenantID(ctx)
}

func requireTenantID(ctx context.Context, reqTenantID string) (string, error) {
	tenantID := getTenantID(ctx, reqTenantID)
	if tenantID == "" {
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New("tenant id is required"))
	}
	return tenantID, nil
}

func ensureTenantSchema(ctx context.Context, tenantID string) context.Context {
	if tenantID == "" {
		return ctx
	}
	if database.TenantSchemaFromContext(ctx) == "" {
		return database.WithTenantSchema(ctx, tenantID)
	}
	return ctx
}

func (s *DatasetServer) getPrimaryDB(ctx context.Context) (*sqlx.DB, error) {
	if s.db != nil {
		return s.db, nil
	}
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}
	return getDBFromContext(ctx, sys)
}

// ===== Dataset RPCs =====

func (s *DatasetServer) CreateDataset(ctx context.Context, req *connect.Request[datasetspb.CreateDatasetRequest]) (*connect.Response[datasetspb.CreateDatasetResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	if database.TenantSchemaFromContext(ctx) == "" && tenantID != "" {
		ctx = database.WithTenantSchema(ctx, tenantID)
	}
	userID := contextkeys.GetUserID(ctx)

	var metadata map[string]interface{}
	if req.Msg.GetMetadata() != nil {
		metadata = req.Msg.GetMetadata().AsMap()
	}

	cmd := datasetscmd.NewCreateDatasetCommand(
		tenantID,
		req.Msg.GetName(),
		req.Msg.GetDescription(),
		userID,
		"",
		metadata,
	)

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Record the resource -> instance parent link (+ a manager grant for the
	// creator) so the ReBAC engine can resolve inherited per-resource access.
	// No-op unless authz is enabled.
	authzresource.OnResourceCreated(ctx, "dataset", cmd.ID, userID)

	resp := &datasetspb.CreateDatasetResponse{
		Dataset: &datasetspb.Dataset{
			Id:       cmd.ID,
			TenantId: tenantID,
			Name:     req.Msg.GetName(),
		},
	}
	return connect.NewResponse(resp), nil
}

func (s *DatasetServer) GetDataset(ctx context.Context, req *connect.Request[datasetspb.GetDatasetRequest]) (*connect.Response[datasetspb.GetDatasetResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	q := datasetsquery.NewGetDatasetByIDQuery(req.Msg.GetId(), tenantID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if res == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("dataset not found"))
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}

	rm, ok := data.(*datasetsquery.DatasetReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("unexpected data type"))
	}

	return connect.NewResponse(&datasetspb.GetDatasetResponse{Dataset: datasetToProto(rm)}), nil
}

func (s *DatasetServer) ListDatasets(ctx context.Context, req *connect.Request[datasetspb.ListDatasetsRequest]) (*connect.Response[datasetspb.ListDatasetsResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	q := datasetsquery.NewListDatasetsQuery(tenantID, int(req.Msg.GetLimit()), int(req.Msg.GetOffset()))
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var datasets []*datasetspb.Dataset
	if res != nil {
		var data interface{} = res
		if resp, ok := res.(*query.Response); ok {
			data = resp.Data
		}
		if list, ok := data.([]datasetsquery.DatasetReadModel); ok {
			for i := range list {
				datasets = append(datasets, datasetToProto(&list[i]))
			}
		}
	}

	return connect.NewResponse(&datasetspb.ListDatasetsResponse{Datasets: datasets}), nil
}

func (s *DatasetServer) UpdateDataset(ctx context.Context, req *connect.Request[datasetspb.UpdateDatasetRequest]) (*connect.Response[datasetspb.UpdateDatasetResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	ctx = ensureTenantSchema(ctx, tenantID)
	userID := contextkeys.GetUserID(ctx)

	cmd := datasetscmd.NewUpdateDatasetCommand(req.Msg.GetId(), tenantID, userID, "")
	if req.Msg.Name != nil {
		cmd.Name = req.Msg.Name
	}
	if req.Msg.Description != nil {
		cmd.Description = req.Msg.Description
	}
	if req.Msg.GetMetadata() != nil {
		cmd.Metadata = req.Msg.GetMetadata().AsMap()
	}

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&datasetspb.UpdateDatasetResponse{
		Dataset: &datasetspb.Dataset{
			Id:       req.Msg.GetId(),
			TenantId: tenantID,
		},
	}), nil
}

func (s *DatasetServer) DeleteDataset(ctx context.Context, req *connect.Request[datasetspb.DeleteDatasetRequest]) (*connect.Response[datasetspb.DeleteDatasetResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	ctx = ensureTenantSchema(ctx, tenantID)
	userID := contextkeys.GetUserID(ctx)

	cmd := datasetscmd.NewDeleteDatasetCommand(req.Msg.GetId(), tenantID, userID, "")
	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Remove the resource's authz parent link. No-op unless authz is enabled.
	authzresource.OnResourceDeleted(ctx, "dataset", req.Msg.GetId())

	return connect.NewResponse(&datasetspb.DeleteDatasetResponse{
		Success: true,
		Message: "dataset deletion dispatched",
	}), nil
}

// ===== DatasetVersion RPCs =====

type datasetVersionRow struct {
	ID            string    `db:"id"`
	DatasetID     string    `db:"dataset_id"`
	TenantID      string    `db:"tenant_id"`
	VersionNumber int32     `db:"version_number"`
	Label         string    `db:"label"`
	Note          string    `db:"note"`
	ItemCount     int32     `db:"item_count"`
	CreatedAt     time.Time `db:"created_at"`
	CreatedBy     string    `db:"created_by"`
}

func (s *DatasetServer) CreateDatasetVersion(ctx context.Context, req *connect.Request[datasetspb.CreateDatasetVersionRequest]) (*connect.Response[datasetspb.CreateDatasetVersionResponse], error) {
	tenantID, err := requireTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	if req.Msg.GetDatasetId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("dataset id is required"))
	}
	ctx = ensureTenantSchema(ctx, tenantID)

	db, err := s.getPrimaryDB(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	defer func() { _ = tx.Rollback() }()

	var datasetID string
	if err := tx.GetContext(ctx, &datasetID, `
		SELECT id
		FROM datasets
		WHERE id = $1 AND tenant_id = $2
		FOR UPDATE
	`, req.Msg.GetDatasetId(), tenantID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("dataset not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var versionNumber int32
	if err := tx.GetContext(ctx, &versionNumber, `
		SELECT COALESCE(MAX(version_number), 0) + 1
		FROM dataset_versions
		WHERE dataset_id = $1 AND tenant_id = $2
	`, datasetID, tenantID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	versionID := uuid.New().String()
	userID := contextkeys.GetUserID(ctx)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO dataset_versions (
			id, dataset_id, tenant_id, version_number, label, note, item_count, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, 0, $7)
	`, versionID, datasetID, tenantID, versionNumber, req.Msg.GetLabel(), req.Msg.GetNote(), userID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	type activeDatasetItemID struct {
		ID string `db:"id"`
	}
	var activeItems []activeDatasetItemID
	if err := tx.SelectContext(ctx, &activeItems, `
		SELECT id
		FROM dataset_items
		WHERE dataset_id = $1 AND tenant_id = $2 AND status = 'active'
		ORDER BY created_at, id
		FOR SHARE
	`, datasetID, tenantID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	itemCount := int64(0)
	if len(activeItems) > 0 {
		versionItemIDs := make([]string, 0, len(activeItems))
		sourceItemIDs := make([]string, 0, len(activeItems))
		for _, item := range activeItems {
			versionItemIDs = append(versionItemIDs, uuid.New().String())
			sourceItemIDs = append(sourceItemIDs, item.ID)
		}

		res, err := tx.ExecContext(ctx, `
			WITH generated(id, source_dataset_item_id) AS (
				SELECT *
				FROM unnest($1::text[], $2::text[])
			)
			INSERT INTO dataset_version_items (
				id, dataset_version_id, tenant_id, source_dataset_item_id,
				input, expected_output, metadata, source_trace_id, source_observation_id
			)
			SELECT
				generated.id, $3, dataset_items.tenant_id, dataset_items.id,
				dataset_items.input, dataset_items.expected_output, COALESCE(dataset_items.metadata, '{}'::jsonb),
				COALESCE(dataset_items.source_trace_id, ''), COALESCE(dataset_items.source_observation_id, '')
			FROM dataset_items
			JOIN generated ON generated.source_dataset_item_id = dataset_items.id
			WHERE dataset_items.dataset_id = $4 AND dataset_items.tenant_id = $5 AND dataset_items.status = 'active'
		`, pq.Array(versionItemIDs), pq.Array(sourceItemIDs), versionID, datasetID, tenantID)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		itemCount, err = res.RowsAffected()
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}

	var row datasetVersionRow
	if err := tx.GetContext(ctx, &row, `
		UPDATE dataset_versions
		SET item_count = $2
		WHERE id = $1 AND tenant_id = $3
		RETURNING id, dataset_id, tenant_id, version_number, label, note, item_count, created_at, created_by
	`, versionID, int32(itemCount), tenantID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if err := tx.Commit(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&datasetspb.CreateDatasetVersionResponse{
		Version: datasetVersionToProto(&row),
	}), nil
}

func (s *DatasetServer) ListDatasetVersions(ctx context.Context, req *connect.Request[datasetspb.ListDatasetVersionsRequest]) (*connect.Response[datasetspb.ListDatasetVersionsResponse], error) {
	tenantID, err := requireTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	if req.Msg.GetDatasetId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("dataset id is required"))
	}
	ctx = ensureTenantSchema(ctx, tenantID)

	db, err := s.getPrimaryDB(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	limit := int(req.Msg.GetLimit())
	if limit <= 0 {
		limit = 50
	}
	offset := int(req.Msg.GetOffset())

	var total int32
	if err := db.GetContext(ctx, &total, `
		SELECT COUNT(*)
		FROM dataset_versions
		WHERE dataset_id = $1 AND tenant_id = $2
	`, req.Msg.GetDatasetId(), tenantID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var rows []datasetVersionRow
	if err := db.SelectContext(ctx, &rows, `
		SELECT id, dataset_id, tenant_id, version_number, label, note, item_count, created_at, created_by
		FROM dataset_versions
		WHERE dataset_id = $1 AND tenant_id = $2
		ORDER BY version_number DESC
		LIMIT $3 OFFSET $4
	`, req.Msg.GetDatasetId(), tenantID, limit, offset); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	versions := make([]*datasetspb.DatasetVersion, 0, len(rows))
	for i := range rows {
		versions = append(versions, datasetVersionToProto(&rows[i]))
	}
	return connect.NewResponse(&datasetspb.ListDatasetVersionsResponse{
		Versions: versions,
		Total:    total,
	}), nil
}

func (s *DatasetServer) GetDatasetVersion(ctx context.Context, req *connect.Request[datasetspb.GetDatasetVersionRequest]) (*connect.Response[datasetspb.GetDatasetVersionResponse], error) {
	tenantID, err := requireTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	if req.Msg.GetId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("dataset version id is required"))
	}
	ctx = ensureTenantSchema(ctx, tenantID)

	db, err := s.getPrimaryDB(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var row datasetVersionRow
	if err := db.GetContext(ctx, &row, `
		SELECT id, dataset_id, tenant_id, version_number, label, note, item_count, created_at, created_by
		FROM dataset_versions
		WHERE id = $1 AND tenant_id = $2
	`, req.Msg.GetId(), tenantID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("dataset version not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&datasetspb.GetDatasetVersionResponse{
		Version: datasetVersionToProto(&row),
	}), nil
}

// ===== DatasetItem RPCs =====

func (s *DatasetServer) CreateDatasetItem(ctx context.Context, req *connect.Request[datasetspb.CreateDatasetItemRequest]) (*connect.Response[datasetspb.CreateDatasetItemResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID, err := requireTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	userID := contextkeys.GetUserID(ctx)

	if err := enterprise.CheckResourceLimit(ctx, s.db, enterprise.LicenseMonitorFromContext(ctx),
		enterprise.UsageTypeDatasetItems,
		`SELECT COUNT(*) FROM dataset_items WHERE tenant_id = $1`,
		[]interface{}{tenantID}, 1, "dataset item"); err != nil {
		return nil, connect.NewError(connect.CodeResourceExhausted, err)
	}

	var input, expectedOutput, metadata map[string]interface{}
	if req.Msg.GetInput() != nil {
		input = req.Msg.GetInput().AsMap()
	}
	if req.Msg.GetExpectedOutput() != nil {
		expectedOutput = req.Msg.GetExpectedOutput().AsMap()
	}
	if req.Msg.GetMetadata() != nil {
		metadata = req.Msg.GetMetadata().AsMap()
	}

	cmd := datasetscmd.NewCreateDatasetItemCommand(
		tenantID,
		req.Msg.GetDatasetId(),
		userID,
		"",
		input,
		expectedOutput,
		metadata,
		req.Msg.GetSourceTraceId(),
		req.Msg.GetSourceObservationId(),
	)

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&datasetspb.CreateDatasetItemResponse{
		Item: &datasetspb.DatasetItem{
			Id:        cmd.ID,
			DatasetId: req.Msg.GetDatasetId(),
			TenantId:  tenantID,
			Status:    "active",
		},
	}), nil
}

func (s *DatasetServer) CreateDatasetItemBatch(ctx context.Context, req *connect.Request[datasetspb.CreateDatasetItemBatchRequest]) (*connect.Response[datasetspb.CreateDatasetItemBatchResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID, err := requireTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	userID := contextkeys.GetUserID(ctx)

	// Batch creation counts against DATASET_ITEMS with the full batch size as
	// the delta — previously this RPC bypassed the limit entirely
	// (editions-and-billing.md, resolver/check API).
	if err := enterprise.CheckResourceLimit(ctx, s.db, enterprise.LicenseMonitorFromContext(ctx),
		enterprise.UsageTypeDatasetItems,
		`SELECT COUNT(*) FROM dataset_items WHERE tenant_id = $1`,
		[]interface{}{tenantID}, int64(len(req.Msg.GetItems())), "dataset item"); err != nil {
		return nil, connect.NewError(connect.CodeResourceExhausted, err)
	}

	var items []datasetscmd.CreateDatasetItemCommand
	for _, item := range req.Msg.GetItems() {
		var input, expectedOutput, metadata map[string]interface{}
		if item.GetInput() != nil {
			input = item.GetInput().AsMap()
		}
		if item.GetExpectedOutput() != nil {
			expectedOutput = item.GetExpectedOutput().AsMap()
		}
		if item.GetMetadata() != nil {
			metadata = item.GetMetadata().AsMap()
		}

		itemCmd := datasetscmd.NewCreateDatasetItemCommand(
			tenantID,
			req.Msg.GetDatasetId(),
			userID,
			"",
			input,
			expectedOutput,
			metadata,
			item.GetSourceTraceId(),
			item.GetSourceObservationId(),
		)
		items = append(items, *itemCmd)
	}

	cmd := datasetscmd.NewCreateDatasetItemBatchCommand(tenantID, req.Msg.GetDatasetId(), userID, "", items)
	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var responseItems []*datasetspb.DatasetItem
	for _, item := range items {
		responseItems = append(responseItems, &datasetspb.DatasetItem{
			Id:        item.ID,
			DatasetId: req.Msg.GetDatasetId(),
			TenantId:  tenantID,
			Status:    "active",
		})
	}

	return connect.NewResponse(&datasetspb.CreateDatasetItemBatchResponse{Items: responseItems}), nil
}

func (s *DatasetServer) GetDatasetItem(ctx context.Context, req *connect.Request[datasetspb.GetDatasetItemRequest]) (*connect.Response[datasetspb.GetDatasetItemResponse], error) {
	// GetDatasetItem uses the same ListDatasetItems query with a post-filter, or a dedicated query.
	// For simplicity, we query by ID using raw SQL through the query bus pattern.
	// Since we don't have a dedicated GetDatasetItemByID query handler yet, return unimplemented.
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("GetDatasetItem not yet implemented — use ListDatasetItems"))
}

func (s *DatasetServer) ListDatasetItems(ctx context.Context, req *connect.Request[datasetspb.ListDatasetItemsRequest]) (*connect.Response[datasetspb.ListDatasetItemsResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	ctx = ensureTenantSchema(ctx, tenantID)

	var status *string
	if req.Msg.Status != nil {
		status = req.Msg.Status
	}

	q := datasetsquery.NewListDatasetItemsQuery(tenantID, req.Msg.GetDatasetId(), status, int(req.Msg.GetLimit()), int(req.Msg.GetOffset()))
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var items []*datasetspb.DatasetItem
	if res != nil {
		var data interface{} = res
		if resp, ok := res.(*query.Response); ok {
			data = resp.Data
		}
		if list, ok := data.([]datasetsquery.DatasetItemReadModel); ok {
			for i := range list {
				items = append(items, datasetItemToProto(&list[i]))
			}
		}
	}

	return connect.NewResponse(&datasetspb.ListDatasetItemsResponse{Items: items}), nil
}

func (s *DatasetServer) UpdateDatasetItem(ctx context.Context, req *connect.Request[datasetspb.UpdateDatasetItemRequest]) (*connect.Response[datasetspb.UpdateDatasetItemResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	userID := contextkeys.GetUserID(ctx)

	cmd := datasetscmd.NewUpdateDatasetItemCommand(req.Msg.GetId(), tenantID, userID, "")
	if req.Msg.GetInput() != nil {
		cmd.Input = req.Msg.GetInput().AsMap()
	}
	if req.Msg.GetExpectedOutput() != nil {
		cmd.ExpectedOutput = req.Msg.GetExpectedOutput().AsMap()
	}
	if req.Msg.GetMetadata() != nil {
		cmd.Metadata = req.Msg.GetMetadata().AsMap()
	}
	if req.Msg.Status != nil {
		cmd.Status = req.Msg.Status
	}

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&datasetspb.UpdateDatasetItemResponse{
		Item: &datasetspb.DatasetItem{
			Id:       req.Msg.GetId(),
			TenantId: tenantID,
		},
	}), nil
}

func (s *DatasetServer) DeleteDatasetItem(ctx context.Context, req *connect.Request[datasetspb.DeleteDatasetItemRequest]) (*connect.Response[datasetspb.DeleteDatasetItemResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	userID := contextkeys.GetUserID(ctx)

	cmd := datasetscmd.NewDeleteDatasetItemCommand(req.Msg.GetId(), tenantID, userID, "")
	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&datasetspb.DeleteDatasetItemResponse{
		Success: true,
		Message: "dataset item deletion dispatched",
	}), nil
}

// ===== ScoreConfig RPCs =====

func (s *DatasetServer) CreateScoreConfig(ctx context.Context, req *connect.Request[datasetspb.CreateScoreConfigRequest]) (*connect.Response[datasetspb.CreateScoreConfigResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	userID := contextkeys.GetUserID(ctx)

	var minValue, maxValue, passThreshold *float64
	if req.Msg.MinValue != nil {
		v := req.Msg.GetMinValue()
		minValue = &v
	}
	if req.Msg.MaxValue != nil {
		v := req.Msg.GetMaxValue()
		maxValue = &v
	}
	if req.Msg.PassThreshold != nil {
		v := req.Msg.GetPassThreshold()
		passThreshold = &v
	}

	var categories map[string]interface{}
	if req.Msg.GetCategories() != nil {
		categories = req.Msg.GetCategories().AsMap()
	}

	cmd := datasetscmd.NewCreateScoreConfigCommand(
		tenantID,
		req.Msg.GetName(),
		req.Msg.GetDataType(),
		req.Msg.GetDescription(),
		userID,
		"",
		minValue,
		maxValue,
		categories,
		req.Msg.GetEvalPrompt(),
		req.Msg.GetEvalModel(),
		req.Msg.GetScorerCode(),
		req.Msg.GetScorerLanguage(),
		req.Msg.GetUseSandbox(),
		req.Msg.GetSlug(),
		req.Msg.GetScorerType(),
		req.Msg.GetOutputType(),
		scoreConfigMessagesToCommand(req.Msg.GetMessages()),
		scoreConfigModelParamsToCommand(req.Msg.GetModelParams()),
		scoreConfigChoiceScoresToCommand(req.Msg.GetChoiceScores()),
		req.Msg.GetUseCot(),
		passThreshold,
		req.Msg.GetDagDefinition(),
	)

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&datasetspb.CreateScoreConfigResponse{
		ScoreConfig: &datasetspb.ScoreConfig{
			Id:            cmd.ID,
			TenantId:      tenantID,
			Name:          req.Msg.GetName(),
			DataType:      req.Msg.GetDataType(),
			Slug:          req.Msg.GetSlug(),
			ScorerType:    req.Msg.GetScorerType(),
			OutputType:    req.Msg.GetOutputType(),
			Messages:      req.Msg.GetMessages(),
			ModelParams:   req.Msg.GetModelParams(),
			ChoiceScores:  req.Msg.GetChoiceScores(),
			UseCot:        req.Msg.GetUseCot(),
			PassThreshold: passThreshold,
			DagDefinition: req.Msg.GetDagDefinition(),
		},
	}), nil
}

func (s *DatasetServer) GetScoreConfig(ctx context.Context, req *connect.Request[datasetspb.GetScoreConfigRequest]) (*connect.Response[datasetspb.GetScoreConfigResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	q := datasetsquery.NewGetScoreConfigQuery(req.Msg.GetId(), tenantID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if res == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("score config not found"))
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}

	rm, ok := data.(*datasetsquery.ScoreConfigReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("unexpected data type"))
	}

	return connect.NewResponse(&datasetspb.GetScoreConfigResponse{ScoreConfig: scoreConfigToProto(rm)}), nil
}

func (s *DatasetServer) ListScoreConfigs(ctx context.Context, req *connect.Request[datasetspb.ListScoreConfigsRequest]) (*connect.Response[datasetspb.ListScoreConfigsResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	q := datasetsquery.NewListScoreConfigsQuery(tenantID, int(req.Msg.GetLimit()), int(req.Msg.GetOffset()))
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var configs []*datasetspb.ScoreConfig
	if res != nil {
		var data interface{} = res
		if resp, ok := res.(*query.Response); ok {
			data = resp.Data
		}
		if list, ok := data.([]datasetsquery.ScoreConfigReadModel); ok {
			for i := range list {
				configs = append(configs, scoreConfigToProto(&list[i]))
			}
		}
	}

	return connect.NewResponse(&datasetspb.ListScoreConfigsResponse{ScoreConfigs: configs}), nil
}

func (s *DatasetServer) UpdateScoreConfig(ctx context.Context, req *connect.Request[datasetspb.UpdateScoreConfigRequest]) (*connect.Response[datasetspb.UpdateScoreConfigResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	userID := contextkeys.GetUserID(ctx)

	cmd := datasetscmd.NewUpdateScoreConfigCommand(req.Msg.GetId(), tenantID, userID, "")
	if req.Msg.Name != nil {
		cmd.Name = req.Msg.Name
	}
	if req.Msg.Description != nil {
		cmd.Description = req.Msg.Description
	}
	if req.Msg.MinValue != nil {
		v := req.Msg.GetMinValue()
		cmd.MinValue = &v
	}
	if req.Msg.MaxValue != nil {
		v := req.Msg.GetMaxValue()
		cmd.MaxValue = &v
	}
	if req.Msg.GetCategories() != nil {
		cmd.Categories = req.Msg.GetCategories().AsMap()
	}
	if req.Msg.DataType != nil {
		cmd.DataType = req.Msg.DataType
	}
	if req.Msg.EvalPrompt != nil {
		cmd.EvalPrompt = req.Msg.EvalPrompt
	}
	if req.Msg.EvalModel != nil {
		cmd.EvalModel = req.Msg.EvalModel
	}
	if req.Msg.IsArchived != nil {
		cmd.IsArchived = req.Msg.IsArchived
	}
	if req.Msg.ScorerCode != nil {
		cmd.ScorerCode = req.Msg.ScorerCode
	}
	if req.Msg.ScorerLanguage != nil {
		cmd.ScorerLanguage = req.Msg.ScorerLanguage
	}
	if req.Msg.UseSandbox != nil {
		cmd.UseSandbox = req.Msg.UseSandbox
	}
	if req.Msg.Slug != nil {
		cmd.Slug = req.Msg.Slug
	}
	if req.Msg.ScorerType != nil {
		cmd.ScorerType = req.Msg.ScorerType
	}
	if req.Msg.OutputType != nil {
		cmd.OutputType = req.Msg.OutputType
	}
	if req.Msg.Messages != nil {
		cmd.Messages = scoreConfigMessagesToCommand(req.Msg.GetMessages())
	}
	if req.Msg.ModelParams != nil {
		cmd.ModelParams = scoreConfigModelParamsToCommand(req.Msg.GetModelParams())
	}
	if req.Msg.ChoiceScores != nil {
		cmd.ChoiceScores = scoreConfigChoiceScoresToCommand(req.Msg.GetChoiceScores())
	}
	if req.Msg.UseCot != nil {
		cmd.UseCot = req.Msg.UseCot
	}
	if req.Msg.PassThreshold != nil {
		v := req.Msg.GetPassThreshold()
		cmd.PassThreshold = &v
	}
	cmd.DagDefinition = req.Msg.GetDagDefinition()

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&datasetspb.UpdateScoreConfigResponse{
		ScoreConfig: &datasetspb.ScoreConfig{
			Id:       req.Msg.GetId(),
			TenantId: tenantID,
		},
	}), nil
}

func (s *DatasetServer) DeleteScoreConfig(ctx context.Context, req *connect.Request[datasetspb.DeleteScoreConfigRequest]) (*connect.Response[datasetspb.DeleteScoreConfigResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	userID := contextkeys.GetUserID(ctx)

	cmd := datasetscmd.NewDeleteScoreConfigCommand(req.Msg.GetId(), tenantID, userID, "")
	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&datasetspb.DeleteScoreConfigResponse{
		Success: true,
		Message: "score config deletion dispatched",
	}), nil
}

// ===== Built-in Metrics =====

func (s *DatasetServer) ListBuiltinMetrics(ctx context.Context, req *connect.Request[datasetspb.ListBuiltinMetricsRequest]) (*connect.Response[datasetspb.ListBuiltinMetricsResponse], error) {
	templates := eval_runner.GetBuiltinMetrics()
	metrics := make([]*datasetspb.BuiltinMetric, len(templates))
	for i, t := range templates {
		metrics[i] = &datasetspb.BuiltinMetric{
			Key:          t.Key,
			Name:         t.Name,
			Description:  t.Description,
			DataType:     t.DataType,
			EvalPrompt:   t.EvalPrompt,
			MinValue:     t.MinValue,
			MaxValue:     t.MaxValue,
			Category:     t.Category,
			ScorerType:   firstNonEmpty(t.ScorerType, "llm_judge"),
			OutputType:   firstNonEmpty(t.OutputType, "numeric"),
			Messages:     builtinMetricMessagesToProto(t.Messages),
			ChoiceScores: builtinMetricChoiceScoresToProto(t.ChoiceScores),
			UseCot:       t.UseCot,
		}
	}
	return connect.NewResponse(&datasetspb.ListBuiltinMetricsResponse{Metrics: metrics}), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func builtinMetricMessagesToProto(messages []eval_runner.ScoreConfigMessage) []*datasetspb.ScoreConfigMessage {
	if len(messages) == 0 {
		return nil
	}
	out := make([]*datasetspb.ScoreConfigMessage, 0, len(messages))
	for _, message := range messages {
		out = append(out, &datasetspb.ScoreConfigMessage{
			Role:    message.Role,
			Content: message.Content,
		})
	}
	return out
}

func builtinMetricChoiceScoresToProto(choiceScores []eval_runner.ScoreConfigChoice) []*datasetspb.ChoiceScore {
	if len(choiceScores) == 0 {
		return nil
	}
	out := make([]*datasetspb.ChoiceScore, 0, len(choiceScores))
	for _, choiceScore := range choiceScores {
		out = append(out, &datasetspb.ChoiceScore{
			Choice: choiceScore.Choice,
			Score:  choiceScore.Score,
		})
	}
	return out
}

// ===== EvalRun RPCs (on EvalServer) =====

func (s *EvalServer) getSys(ctx context.Context) (*cqrs.System, error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}
	return sys, nil
}

func (s *EvalServer) CreateEvalRun(ctx context.Context, req *connect.Request[datasetspb.CreateEvalRunRequest]) (*connect.Response[datasetspb.CreateEvalRunResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID, err := requireTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	userID := contextkeys.GetUserID(ctx)

	if err := enterprise.CheckResourceLimit(ctx, s.db, enterprise.LicenseMonitorFromContext(ctx),
		enterprise.UsageTypeEvalRunsMonthly,
		`SELECT COUNT(*) FROM eval_runs WHERE tenant_id = $1 AND created_at >= date_trunc('month', NOW())`,
		[]interface{}{tenantID}, 1, "eval run"); err != nil {
		return nil, connect.NewError(connect.CodeResourceExhausted, err)
	}

	var evalConfig map[string]interface{}
	if req.Msg.GetEvalConfig() != nil {
		evalConfig = req.Msg.GetEvalConfig().AsMap()
	}

	cmd := datasetscmd.NewCreateEvalRunCommand(
		tenantID,
		req.Msg.GetDatasetId(),
		req.Msg.GetName(),
		req.Msg.GetDescription(),
		req.Msg.GetEvalTargetType(),
		req.Msg.GetEvalTargetId(),
		userID,
		"",
		evalConfig,
		req.Msg.GetScorerConfigIds(),
		req.Msg.GetDatasetVersionId(),
	)

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&datasetspb.CreateEvalRunResponse{
		EvalRun: &datasetspb.EvalRun{
			Id:        cmd.ID,
			TenantId:  tenantID,
			DatasetId: req.Msg.GetDatasetId(),
			Name:      req.Msg.GetName(),
			Status:    "pending",
		},
	}), nil
}

func (s *EvalServer) GetEvalRun(ctx context.Context, req *connect.Request[datasetspb.GetEvalRunRequest]) (*connect.Response[datasetspb.GetEvalRunResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	ctx = ensureTenantSchema(ctx, tenantID)
	q := datasetsquery.NewGetEvalRunQuery(req.Msg.GetId(), tenantID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if res == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("eval run not found"))
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}

	rm, ok := data.(*datasetsquery.EvalRunReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("unexpected data type"))
	}

	return connect.NewResponse(&datasetspb.GetEvalRunResponse{EvalRun: evalRunToProto(rm)}), nil
}

func (s *EvalServer) ListEvalRuns(ctx context.Context, req *connect.Request[datasetspb.ListEvalRunsRequest]) (*connect.Response[datasetspb.ListEvalRunsResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	ctx = ensureTenantSchema(ctx, tenantID)

	var datasetID, status *string
	if req.Msg.DatasetId != nil {
		datasetID = req.Msg.DatasetId
	}
	if req.Msg.Status != nil {
		status = req.Msg.Status
	}

	q := datasetsquery.NewListEvalRunsQuery(tenantID, datasetID, status, int(req.Msg.GetLimit()), int(req.Msg.GetOffset()))
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var runs []*datasetspb.EvalRun
	if res != nil {
		var data interface{} = res
		if resp, ok := res.(*query.Response); ok {
			data = resp.Data
		}
		if list, ok := data.([]datasetsquery.EvalRunReadModel); ok {
			for i := range list {
				runs = append(runs, evalRunToProto(&list[i]))
			}
		}
	}

	return connect.NewResponse(&datasetspb.ListEvalRunsResponse{EvalRuns: runs}), nil
}

func (s *EvalServer) CancelEvalRun(ctx context.Context, req *connect.Request[datasetspb.CancelEvalRunRequest]) (*connect.Response[datasetspb.CancelEvalRunResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	userID := contextkeys.GetUserID(ctx)

	cmd := datasetscmd.NewCancelEvalRunCommand(req.Msg.GetId(), tenantID, userID, "")
	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&datasetspb.CancelEvalRunResponse{
		EvalRun: &datasetspb.EvalRun{
			Id:       req.Msg.GetId(),
			TenantId: tenantID,
			Status:   "cancelled",
		},
	}), nil
}

func (s *EvalServer) RetryEvalRun(ctx context.Context, req *connect.Request[datasetspb.RetryEvalRunRequest]) (*connect.Response[datasetspb.RetryEvalRunResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	if req.Msg.GetId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("eval run id is required"))
	}

	db, ok := ctx.Value(contextkeys.PrimaryDB).(*sqlx.DB)
	if (!ok || db == nil) && sys.ProjectionManager != nil {
		db = sys.ProjectionManager.DB()
	}
	if db == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("primary db not available"))
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	if tenantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}
	ctx = ensureTenantSchema(ctx, tenantID)
	retryAll := req.Msg.GetRetryAll()

	tx, err := db.Beginx()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	defer func() { _ = tx.Rollback() }()

	schema := database.TenantSchemaFromContext(ctx)
	if schema == "" && tenantID != "" {
		schema = tenantID
	}
	if schema == "" {
		schema = "everstack"
	}
	fallbackAllowed := true
	if !isSafeSchema(schema) {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid schema: %s", schema))
	}
	resolvedSchema, err := resolveEvalSchema(ctx, tx, schema, fallbackAllowed)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	searchPath := pq.QuoteIdentifier(resolvedSchema)
	if resolvedSchema != "public" {
		searchPath = fmt.Sprintf("%s, public", searchPath)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("SET search_path TO %s", searchPath)); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var updated int64
	if retryAll {
		res, err := tx.Exec(`
			UPDATE eval_run_items
			SET status = 'pending',
				output = NULL,
				trace_id = '',
				latency_ms = 0,
				cost = 0,
				token_usage = '{}',
				error = '',
				scores = '{}',
				updated_at = NOW()
			WHERE eval_run_id = $1 AND tenant_id = $2
		`, req.Msg.GetId(), tenantID)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		updated, _ = res.RowsAffected()
	} else {
		res, err := tx.Exec(`
			UPDATE eval_run_items
			SET status = 'pending',
				output = NULL,
				trace_id = '',
				latency_ms = 0,
				cost = 0,
				token_usage = '{}',
				error = '',
				scores = '{}',
				updated_at = NOW()
			WHERE eval_run_id = $1 AND tenant_id = $2 AND status = 'failed'
		`, req.Msg.GetId(), tenantID)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		updated, _ = res.RowsAffected()
	}

	var total, completed, failed int
	if err := tx.QueryRow(`
		SELECT
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE status = 'completed') as completed,
			COUNT(*) FILTER (WHERE status = 'failed') as failed
		FROM eval_run_items
		WHERE eval_run_id = $1 AND tenant_id = $2
	`, req.Msg.GetId(), tenantID).Scan(&total, &completed, &failed); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if _, err := tx.Exec(`
		UPDATE eval_runs
		SET status = 'pending',
			total_items = $2,
			completed_items = $3,
			failed_items = $4,
			started_at = NULL,
			completed_at = NULL,
			lease_owner = NULL,
			lease_expires_at = NULL,
			lease_epoch = lease_epoch + 1,
			updated_at = NOW()
		WHERE id = $1 AND tenant_id = $5
	`, req.Msg.GetId(), total, completed, failed, tenantID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if err := tx.Commit(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if updated == 0 && !retryAll {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("no failed items to retry"))
	}

	q := datasetsquery.NewGetEvalRunQuery(req.Msg.GetId(), tenantID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if res == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("eval run not found"))
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}
	rm, ok := data.(*datasetsquery.EvalRunReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("unexpected data type"))
	}

	return connect.NewResponse(&datasetspb.RetryEvalRunResponse{
		EvalRun: evalRunToProto(rm),
	}), nil
}

func isSafeSchema(schema string) bool {
	if schema == "" {
		return false
	}
	for _, r := range schema {
		if r == 0 {
			return false
		}
	}
	return true
}

func resolveEvalSchema(ctx context.Context, tx *sqlx.Tx, preferred string, fallbackAllowed bool) (string, error) {
	candidates := []string{preferred}
	if fallbackAllowed {
		candidates = append(candidates, "everstack", "public")
	}
	seen := map[string]struct{}{}
	for _, s := range candidates {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		var exists bool
		err := tx.GetContext(ctx, &exists, `
			SELECT EXISTS (
				SELECT 1
				FROM pg_catalog.pg_class c
				JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
				WHERE c.relname = 'eval_run_items' AND n.nspname = $1
			)
		`, s)
		if err != nil {
			return "", err
		}
		if exists {
			return s, nil
		}
	}
	return "", fmt.Errorf("eval_run_items table not found in candidate schemas")
}

func (s *EvalServer) DeleteEvalRun(ctx context.Context, req *connect.Request[datasetspb.DeleteEvalRunRequest]) (*connect.Response[datasetspb.DeleteEvalRunResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	userID := contextkeys.GetUserID(ctx)

	cmd := datasetscmd.NewDeleteEvalRunCommand(req.Msg.GetId(), tenantID, userID, "")
	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&datasetspb.DeleteEvalRunResponse{
		Success: true,
		Message: "Eval run deleted",
	}), nil
}

func (s *EvalServer) GetEvalRunItems(ctx context.Context, req *connect.Request[datasetspb.GetEvalRunItemsRequest]) (*connect.Response[datasetspb.GetEvalRunItemsResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())

	var status *string
	if req.Msg.Status != nil {
		status = req.Msg.Status
	}

	q := datasetsquery.NewListEvalRunItemsQuery(tenantID, req.Msg.GetEvalRunId(), status, int(req.Msg.GetLimit()), int(req.Msg.GetOffset()))
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var items []*datasetspb.EvalRunItem
	if res != nil {
		var data interface{} = res
		if resp, ok := res.(*query.Response); ok {
			data = resp.Data
		}
		if list, ok := data.([]datasetsquery.EvalRunItemReadModel); ok {
			for i := range list {
				items = append(items, evalRunItemToProto(&list[i]))
			}
		}
	}

	return connect.NewResponse(&datasetspb.GetEvalRunItemsResponse{Items: items}), nil
}

func (s *EvalServer) GetEvalRunSummary(ctx context.Context, req *connect.Request[datasetspb.GetEvalRunSummaryRequest]) (*connect.Response[datasetspb.GetEvalRunSummaryResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	ctx = ensureTenantSchema(ctx, tenantID)
	q := datasetsquery.NewGetEvalRunQuery(req.Msg.GetId(), tenantID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if res == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("eval run not found"))
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}

	rm, ok := data.(*datasetsquery.EvalRunReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("unexpected data type"))
	}

	evalRun := evalRunToProto(rm)

	// Parse score_summary as breakdown
	var scoreBreakdown *structpb.Struct
	if len(rm.ScoreSummary) > 0 {
		var m map[string]interface{}
		if err := json.Unmarshal(rm.ScoreSummary, &m); err == nil {
			scoreBreakdown, _ = structpb.NewStruct(m)
		}
	}

	return connect.NewResponse(&datasetspb.GetEvalRunSummaryResponse{
		EvalRun:        evalRun,
		ScoreBreakdown: scoreBreakdown,
	}), nil
}

// toProtoGrade maps the engine's ComparisonGrade string onto the proto enum.
func toProtoGrade(g eval_runner.ComparisonGrade) datasetspb.ComparisonGrade {
	switch g {
	case eval_runner.GradeImprovement:
		return datasetspb.ComparisonGrade_COMPARISON_GRADE_IMPROVEMENT
	case eval_runner.GradeRegression:
		return datasetspb.ComparisonGrade_COMPARISON_GRADE_REGRESSION
	case eval_runner.GradeTradeoff:
		return datasetspb.ComparisonGrade_COMPARISON_GRADE_TRADEOFF
	case eval_runner.GradeTie:
		return datasetspb.ComparisonGrade_COMPARISON_GRADE_TIE
	case eval_runner.GradeInsufficientData:
		return datasetspb.ComparisonGrade_COMPARISON_GRADE_INSUFFICIENT_DATA
	default:
		return datasetspb.ComparisonGrade_COMPARISON_GRADE_UNSPECIFIED
	}
}

// CompareEvalRuns resolves the requested runs and, for exactly two run ids
// (ids[0]=baseline, ids[1]=candidate — the order cmd/everstack-eval sends),
// runs the paired-bootstrap comparison engine and returns the typed verdict.
// Any other id count returns only the legacy score-summary struct.
func (s *EvalServer) CompareEvalRuns(ctx context.Context, req *connect.Request[datasetspb.CompareEvalRunsRequest]) (*connect.Response[datasetspb.CompareEvalRunsResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	if tenantID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("tenant id is required"))
	}
	ctx = ensureTenantSchema(ctx, tenantID)

	ids := req.Msg.GetEvalRunIds()
	var runs []*datasetspb.EvalRun
	for _, runID := range ids {
		q := datasetsquery.NewGetEvalRunQuery(runID, tenantID)
		res, err := sys.QueryBus.Execute(ctx, q)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		var data interface{} = res
		if resp, ok := res.(*query.Response); ok {
			data = resp.Data
		}
		rm, _ := data.(*datasetsquery.EvalRunReadModel)
		if rm == nil {
			// A foreign-tenant run id looks exactly like a missing one:
			// hard error, never a silent skip.
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("eval run %s not found", runID))
		}
		runs = append(runs, evalRunToProto(rm))
	}

	// Legacy untyped comparison struct from score summaries (back-compat).
	comparison := map[string]interface{}{}
	for _, run := range runs {
		if run.ScoreSummary != nil {
			comparison[run.Id] = run.ScoreSummary.AsMap()
		}
	}

	out := &datasetspb.CompareEvalRunsResponse{EvalRuns: runs}

	if len(ids) == 2 {
		if s.db == nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("database not configured for eval service"))
		}
		result, err := eval_runner.ComputeComparison(ctx, s.db, tenantID, ids[0], ids[1], req.Msg.GetPersist())
		if err != nil {
			code := connect.CodeInternal
			if errors.Is(err, eval_runner.ErrRunNotTerminal) {
				code = connect.CodeFailedPrecondition
			}
			return nil, connect.NewError(code, err)
		}

		scorerResults := make([]*datasetspb.ComparisonScorerResult, 0, len(result.ScorerResults))
		for _, sr := range result.ScorerResults {
			scorerResults = append(scorerResults, &datasetspb.ComparisonScorerResult{
				Name:          sr.Name,
				BaselineMean:  sr.BaselineMean,
				CandidateMean: sr.CandidateMean,
				MeanDiff:      sr.MeanDiff,
				CiLow:         sr.CILow,
				CiHigh:        sr.CIHigh,
				PValue:        sr.PValue,
				Verdict:       sr.Verdict,
				N:             int32(sr.N),
			})
		}
		out.ScorerResults = scorerResults
		out.Overall = &datasetspb.ComparisonVerdict{
			Grade:          toProtoGrade(result.Overall),
			Rationale:      result.Rationale,
			LatencyDelta:   result.LatencyDelta,
			CostDelta:      result.CostDelta,
			ErrorRateDelta: result.ErrorRateDelta,
			Coverage:       result.Coverage,
		}
		out.ComparisonId = result.ComparisonID
		out.MatchMode = result.MatchMode

		// CLI bridge: cmd/everstack-eval gates CI on the legacy struct's
		// has_regression flag (readRegressionFlag), so mirror the verdict
		// there alongside the score summaries.
		comparison["has_regression"] = result.Overall == eval_runner.GradeRegression
	}

	comparisonStruct, _ := structpb.NewStruct(comparison)
	out.Comparison = comparisonStruct

	return connect.NewResponse(out), nil
}

// ListComparisonRows returns paginated per-item rows for a materialized
// comparison, re-paired with the same engine that produced the stored
// verdict.
func (s *EvalServer) ListComparisonRows(ctx context.Context, req *connect.Request[datasetspb.ListComparisonRowsRequest]) (*connect.Response[datasetspb.ListComparisonRowsResponse], error) {
	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	if tenantID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("tenant id is required"))
	}
	ctx = ensureTenantSchema(ctx, tenantID)
	if s.db == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("database not configured for eval service"))
	}

	rows, total, err := eval_runner.GetComparisonRows(ctx, s.db, tenantID,
		req.Msg.GetComparisonId(), int(req.Msg.GetLimit()), int(req.Msg.GetOffset()),
		req.Msg.GetOnlyRegressions())
	if err != nil {
		code := connect.CodeInternal
		if errors.Is(err, eval_runner.ErrComparisonNotFound) {
			code = connect.CodeNotFound
		}
		return nil, connect.NewError(code, err)
	}

	pbRows := make([]*datasetspb.ComparisonRow, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		deltas := make([]*datasetspb.ScorerCellDelta, 0, len(row.ScorerDeltas))
		for _, d := range row.ScorerDeltas {
			deltas = append(deltas, &datasetspb.ScorerCellDelta{
				Name:      d.Name,
				Baseline:  d.Baseline,
				Candidate: d.Candidate,
				Delta:     d.Delta,
			})
		}
		pbRows = append(pbRows, &datasetspb.ComparisonRow{
			InputHash:       row.InputHash,
			InputPreview:    row.InputPreview,
			BaselineOutput:  row.BaselineOutput,
			CandidateOutput: row.CandidateOutput,
			ScorerDeltas:    deltas,
			Regression:      row.Regression,
		})
	}

	return connect.NewResponse(&datasetspb.ListComparisonRowsResponse{
		Rows:  pbRows,
		Total: int32(total),
	}), nil
}

// ===== Proto conversion helpers =====

func datasetToProto(rm *datasetsquery.DatasetReadModel) *datasetspb.Dataset {
	ds := &datasetspb.Dataset{
		Id:        rm.ID,
		TenantId:  rm.TenantID,
		Name:      rm.Name,
		CreatedAt: utils.ParseTimestamp(rm.CreatedAt),
		UpdatedAt: utils.ParseTimestamp(rm.UpdatedAt),
	}
	ds.Description = rm.Description

	if len(rm.Metadata) > 0 {
		var m map[string]interface{}
		if err := json.Unmarshal(rm.Metadata, &m); err == nil {
			if s, err := structpb.NewStruct(m); err == nil {
				ds.Metadata = s
			}
		}
	}

	if rm.ArchivedAt.Valid {
		ds.ArchivedAt = utils.ParseTimestamp(rm.ArchivedAt.String)
	}

	return ds
}

func datasetItemToProto(rm *datasetsquery.DatasetItemReadModel) *datasetspb.DatasetItem {
	item := &datasetspb.DatasetItem{
		Id:                  rm.ID,
		DatasetId:           rm.DatasetID,
		TenantId:            rm.TenantID,
		SourceTraceId:       rm.SourceTraceID,
		SourceObservationId: rm.SourceObservationID,
		Status:              rm.Status,
		CreatedAt:           utils.ParseTimestamp(rm.CreatedAt),
		UpdatedAt:           utils.ParseTimestamp(rm.UpdatedAt),
	}

	if len(rm.Input) > 0 {
		var m map[string]interface{}
		if err := json.Unmarshal(rm.Input, &m); err == nil {
			if s, err := structpb.NewStruct(m); err == nil {
				item.Input = s
			}
		}
	}

	if len(rm.ExpectedOutput) > 0 {
		var m map[string]interface{}
		if err := json.Unmarshal(rm.ExpectedOutput, &m); err == nil {
			if s, err := structpb.NewStruct(m); err == nil {
				item.ExpectedOutput = s
			}
		}
	}

	if len(rm.Metadata) > 0 {
		var m map[string]interface{}
		if err := json.Unmarshal(rm.Metadata, &m); err == nil {
			if s, err := structpb.NewStruct(m); err == nil {
				item.Metadata = s
			}
		}
	}

	return item
}

func datasetVersionToProto(row *datasetVersionRow) *datasetspb.DatasetVersion {
	if row == nil {
		return nil
	}
	return &datasetspb.DatasetVersion{
		Id:            row.ID,
		DatasetId:     row.DatasetID,
		TenantId:      row.TenantID,
		VersionNumber: row.VersionNumber,
		Label:         row.Label,
		Note:          row.Note,
		ItemCount:     row.ItemCount,
		CreatedAt:     timestamppb.New(row.CreatedAt),
		CreatedBy:     row.CreatedBy,
	}
}

func scoreConfigToProto(rm *datasetsquery.ScoreConfigReadModel) *datasetspb.ScoreConfig {
	sc := &datasetspb.ScoreConfig{
		Id:             rm.ID,
		TenantId:       rm.TenantID,
		Name:           rm.Name,
		DataType:       rm.DataType,
		Description:    rm.Description,
		EvalPrompt:     rm.EvalPrompt,
		EvalModel:      rm.EvalModel,
		IsArchived:     rm.IsArchived,
		ScorerCode:     rm.ScorerCode,
		ScorerLanguage: rm.ScorerLanguage,
		UseSandbox:     rm.UseSandbox,
		Slug:           rm.Slug,
		ScorerType:     rm.ScorerType,
		OutputType:     rm.OutputType,
		UseCot:         rm.UseCot,
		DagDefinition:  rm.DagDefinition,
		CreatedAt:      utils.ParseTimestamp(rm.CreatedAt),
		UpdatedAt:      utils.ParseTimestamp(rm.UpdatedAt),
	}

	if rm.MinValue.Valid {
		v := rm.MinValue.Float64
		sc.MinValue = &v
	}
	if rm.MaxValue.Valid {
		v := rm.MaxValue.Float64
		sc.MaxValue = &v
	}
	if rm.PassThreshold.Valid {
		v := rm.PassThreshold.Float64
		sc.PassThreshold = &v
	}

	if len(rm.Categories) > 0 {
		var m map[string]interface{}
		if err := json.Unmarshal(rm.Categories, &m); err == nil {
			if s, err := structpb.NewStruct(m); err == nil {
				sc.Categories = s
			}
		}
	}

	if len(rm.Messages) > 0 {
		var messages []*datasetspb.ScoreConfigMessage
		if err := json.Unmarshal(rm.Messages, &messages); err == nil {
			sc.Messages = messages
		}
	}
	if len(rm.ModelParams) > 0 {
		var modelParams datasetspb.ModelParams
		if err := json.Unmarshal(rm.ModelParams, &modelParams); err == nil {
			sc.ModelParams = &modelParams
		}
	}
	if len(rm.ChoiceScores) > 0 {
		var choiceScores []*datasetspb.ChoiceScore
		if err := json.Unmarshal(rm.ChoiceScores, &choiceScores); err == nil {
			sc.ChoiceScores = choiceScores
		}
	}

	return sc
}

func scoreConfigMessagesToCommand(messages []*datasetspb.ScoreConfigMessage) []datasetscmd.ScoreConfigMessagePayload {
	if messages == nil {
		return nil
	}
	out := make([]datasetscmd.ScoreConfigMessagePayload, 0, len(messages))
	for _, message := range messages {
		if message == nil {
			continue
		}
		out = append(out, datasetscmd.ScoreConfigMessagePayload{
			Role:    message.GetRole(),
			Content: message.GetContent(),
		})
	}
	return out
}

func scoreConfigModelParamsToCommand(params *datasetspb.ModelParams) *datasetscmd.ScoreConfigModelParamsPayload {
	if params == nil {
		return nil
	}
	stop := append([]string(nil), params.GetStop()...)
	return &datasetscmd.ScoreConfigModelParamsPayload{
		Temperature: params.Temperature,
		TopP:        params.TopP,
		MaxTokens:   params.MaxTokens,
		Stop:        stop,
		ToolChoice:  params.ToolChoice,
	}
}

func scoreConfigChoiceScoresToCommand(choiceScores []*datasetspb.ChoiceScore) []datasetscmd.ScoreConfigChoiceScorePayload {
	if choiceScores == nil {
		return nil
	}
	out := make([]datasetscmd.ScoreConfigChoiceScorePayload, 0, len(choiceScores))
	for _, choiceScore := range choiceScores {
		if choiceScore == nil {
			continue
		}
		out = append(out, datasetscmd.ScoreConfigChoiceScorePayload{
			Choice: choiceScore.GetChoice(),
			Score:  choiceScore.GetScore(),
		})
	}
	return out
}

func evalRunToProto(rm *datasetsquery.EvalRunReadModel) *datasetspb.EvalRun {
	run := &datasetspb.EvalRun{
		Id:              rm.ID,
		TenantId:        rm.TenantID,
		DatasetId:       rm.DatasetID,
		Name:            rm.Name,
		Description:     rm.Description,
		Status:          rm.Status,
		EvalTargetType:  rm.EvalTargetType,
		EvalTargetId:    rm.EvalTargetID,
		ScorerConfigIds: rm.ScorerConfigIDs,
		TotalItems:      rm.TotalItems,
		CompletedItems:  rm.CompletedItems,
		FailedItems:     rm.FailedItems,
		CreatedAt:       utils.ParseTimestamp(rm.CreatedAt),
		UpdatedAt:       utils.ParseTimestamp(rm.UpdatedAt),
	}

	if len(rm.EvalConfig) > 0 {
		var m map[string]interface{}
		if err := json.Unmarshal(rm.EvalConfig, &m); err == nil {
			if s, err := structpb.NewStruct(m); err == nil {
				run.EvalConfig = s
			}
		}
	}

	if len(rm.ScoreSummary) > 0 {
		var m map[string]interface{}
		if err := json.Unmarshal(rm.ScoreSummary, &m); err == nil {
			if s, err := structpb.NewStruct(m); err == nil {
				run.ScoreSummary = s
			}
		}
	}

	if rm.StartedAt.Valid {
		run.StartedAt = utils.ParseTimestamp(rm.StartedAt.String)
	}
	if rm.CompletedAt.Valid {
		run.CompletedAt = utils.ParseTimestamp(rm.CompletedAt.String)
	}

	run.IsBaseline = rm.IsBaseline
	if rm.BaselineRunID.Valid {
		run.BaselineRunId = rm.BaselineRunID.String
	}

	if len(rm.RegressionResult) > 0 {
		var m map[string]interface{}
		if err := json.Unmarshal(rm.RegressionResult, &m); err == nil {
			if s, err := structpb.NewStruct(m); err == nil {
				run.RegressionResult = s
			}
		}
	}

	return run
}

func evalRunItemToProto(rm *datasetsquery.EvalRunItemReadModel) *datasetspb.EvalRunItem {
	item := &datasetspb.EvalRunItem{
		Id:            rm.ID,
		EvalRunId:     rm.EvalRunID,
		DatasetItemId: rm.DatasetItemID,
		TenantId:      rm.TenantID,
		Status:        rm.Status,
		TraceId:       rm.TraceID,
		LatencyMs:     rm.LatencyMs,
		Cost:          rm.Cost,
		Error:         rm.Error,
		CreatedAt:     utils.ParseTimestamp(rm.CreatedAt),
		UpdatedAt:     utils.ParseTimestamp(rm.UpdatedAt),
	}

	if len(rm.Output) > 0 {
		var m map[string]interface{}
		if err := json.Unmarshal(rm.Output, &m); err == nil {
			if s, err := structpb.NewStruct(m); err == nil {
				item.Output = s
			}
		}
	}

	if len(rm.TokenUsage) > 0 {
		var m map[string]interface{}
		if err := json.Unmarshal(rm.TokenUsage, &m); err == nil {
			if s, err := structpb.NewStruct(m); err == nil {
				item.TokenUsage = s
			}
		}
	}

	if len(rm.Scores) > 0 {
		var m map[string]interface{}
		if err := json.Unmarshal(rm.Scores, &m); err == nil {
			if s, err := structpb.NewStruct(m); err == nil {
				item.Scores = s
			}
		}
	}

	if len(rm.Input) > 0 {
		var m map[string]interface{}
		if err := json.Unmarshal(rm.Input, &m); err == nil {
			if s, err := structpb.NewStruct(m); err == nil {
				item.Input = s
			}
		}
	}

	if len(rm.ExpectedOutput) > 0 {
		var m map[string]interface{}
		if err := json.Unmarshal(rm.ExpectedOutput, &m); err == nil {
			if s, err := structpb.NewStruct(m); err == nil {
				item.ExpectedOutput = s
			}
		}
	}

	return item
}

// Suppress unused import warnings
var _ = timestamppb.Now
