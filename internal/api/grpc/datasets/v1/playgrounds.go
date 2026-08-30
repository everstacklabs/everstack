package v1

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	datasetspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/datasets/v1"
)

// playgroundRow is the scan target for playground SELECTs. config is scanned as
// raw JSON bytes and decoded into a structpb.Struct for the wire response.
type playgroundRow struct {
	ID        string    `db:"id"`
	Name      string    `db:"name"`
	Config    []byte    `db:"config"`
	CreatedBy string    `db:"created_by"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// toProto converts a scanned row into the wire Playground message. tenantID is
// supplied by the caller (always the authenticated tenant) rather than scanned,
// since every query is already scoped to it.
func (r *playgroundRow) toProto(tenantID string) (*datasetspb.Playground, error) {
	var m map[string]interface{}
	if len(r.Config) > 0 {
		if err := json.Unmarshal(r.Config, &m); err != nil {
			return nil, fmt.Errorf("decode config: %w", err)
		}
	}
	if m == nil {
		m = map[string]interface{}{}
	}
	cfg, err := structpb.NewStruct(m)
	if err != nil {
		return nil, fmt.Errorf("encode config: %w", err)
	}
	return &datasetspb.Playground{
		Id:        r.ID,
		TenantId:  tenantID,
		Name:      r.Name,
		Config:    cfg,
		CreatedBy: r.CreatedBy,
		CreatedAt: timestamppb.New(r.CreatedAt),
		UpdatedAt: timestamppb.New(r.UpdatedAt),
	}, nil
}

const playgroundSelectCols = `id, name, config, created_by, created_at, updated_at`

// CreatePlayground inserts a new playground document scoped to the caller's
// tenant. The request tenant_id is ignored; tenant + creator come from the
// authenticated context.
func (s *PlaygroundServer) CreatePlayground(
	ctx context.Context,
	req *connect.Request[datasetspb.CreatePlaygroundRequest],
) (*connect.Response[datasetspb.CreatePlaygroundResponse], error) {
	tenantID := getTenantID(ctx, "")
	if tenantID == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("tenant not resolved"))
	}

	configJSON, err := json.Marshal(req.Msg.GetConfig().AsMap())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("encode config: %w", err))
	}

	id := uuid.NewString()
	createdBy := contextkeys.GetUserID(ctx)

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO playgrounds (id, tenant_id, name, config, created_by, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, NOW(), NOW())`,
		id, tenantID, req.Msg.GetName(), configJSON, createdBy,
	)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("insert playground: %w", err))
	}

	pg, err := s.fetchPlayground(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&datasetspb.CreatePlaygroundResponse{Playground: pg}), nil
}

// GetPlayground returns a single non-archived playground for the tenant.
func (s *PlaygroundServer) GetPlayground(
	ctx context.Context,
	req *connect.Request[datasetspb.GetPlaygroundRequest],
) (*connect.Response[datasetspb.GetPlaygroundResponse], error) {
	tenantID := getTenantID(ctx, "")
	if tenantID == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("tenant not resolved"))
	}

	pg, err := s.fetchPlayground(ctx, tenantID, req.Msg.GetId())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&datasetspb.GetPlaygroundResponse{Playground: pg}), nil
}

// ListPlaygrounds returns non-archived playgrounds for the tenant, newest first.
func (s *PlaygroundServer) ListPlaygrounds(
	ctx context.Context,
	req *connect.Request[datasetspb.ListPlaygroundsRequest],
) (*connect.Response[datasetspb.ListPlaygroundsResponse], error) {
	tenantID := getTenantID(ctx, "")
	if tenantID == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("tenant not resolved"))
	}

	limit := int(req.Msg.GetLimit())
	if limit <= 0 {
		limit = 100
	}
	offset := int(req.Msg.GetOffset())
	if offset < 0 {
		offset = 0
	}

	rows := []playgroundRow{}
	if err := s.db.SelectContext(ctx, &rows,
		`SELECT `+playgroundSelectCols+`
		 FROM playgrounds
		 WHERE tenant_id = $1 AND archived_at IS NULL
		 ORDER BY created_at DESC
		 LIMIT $2 OFFSET $3`,
		tenantID, limit, offset,
	); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list playgrounds: %w", err))
	}

	playgrounds := make([]*datasetspb.Playground, 0, len(rows))
	for i := range rows {
		pg, err := rows[i].toProto(tenantID)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		playgrounds = append(playgrounds, pg)
	}

	var total int
	if err := s.db.GetContext(ctx, &total,
		`SELECT count(*) FROM playgrounds WHERE tenant_id = $1 AND archived_at IS NULL`,
		tenantID,
	); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("count playgrounds: %w", err))
	}

	return connect.NewResponse(&datasetspb.ListPlaygroundsResponse{
		Playgrounds: playgrounds,
		Total:       int32(total),
	}), nil
}

// UpdatePlayground applies the provided optional fields (name and/or config),
// always bumping updated_at, scoped to the tenant. Returns the updated row.
func (s *PlaygroundServer) UpdatePlayground(
	ctx context.Context,
	req *connect.Request[datasetspb.UpdatePlaygroundRequest],
) (*connect.Response[datasetspb.UpdatePlaygroundResponse], error) {
	tenantID := getTenantID(ctx, "")
	if tenantID == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("tenant not resolved"))
	}

	id := req.Msg.GetId()

	// Build a dynamic SET clause from only the provided optional fields.
	sets := []string{"updated_at = NOW()"}
	args := []interface{}{}
	argN := 1

	if req.Msg.Name != nil {
		sets = append(sets, fmt.Sprintf("name = $%d", argN))
		args = append(args, req.Msg.GetName())
		argN++
	}
	if req.Msg.Config != nil {
		configJSON, err := json.Marshal(req.Msg.GetConfig().AsMap())
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("encode config: %w", err))
		}
		sets = append(sets, fmt.Sprintf("config = $%d", argN))
		args = append(args, configJSON)
		argN++
	}

	// If neither name nor config was provided, skip the write and re-fetch.
	if req.Msg.Name == nil && req.Msg.Config == nil {
		pg, err := s.fetchPlayground(ctx, tenantID, id)
		if err != nil {
			return nil, err
		}
		return connect.NewResponse(&datasetspb.UpdatePlaygroundResponse{Playground: pg}), nil
	}

	query := fmt.Sprintf(
		`UPDATE playgrounds SET %s WHERE id = $%d AND tenant_id = $%d AND archived_at IS NULL`,
		joinSets(sets), argN, argN+1,
	)
	args = append(args, id, tenantID)

	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("update playground: %w", err))
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("playground not found"))
	}

	pg, err := s.fetchPlayground(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&datasetspb.UpdatePlaygroundResponse{Playground: pg}), nil
}

// DeletePlayground soft-deletes a playground by stamping archived_at.
func (s *PlaygroundServer) DeletePlayground(
	ctx context.Context,
	req *connect.Request[datasetspb.DeletePlaygroundRequest],
) (*connect.Response[datasetspb.DeletePlaygroundResponse], error) {
	tenantID := getTenantID(ctx, "")
	if tenantID == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("tenant not resolved"))
	}

	res, err := s.db.ExecContext(ctx,
		`UPDATE playgrounds SET archived_at = NOW()
		 WHERE id = $1 AND tenant_id = $2 AND archived_at IS NULL`,
		req.Msg.GetId(), tenantID,
	)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("delete playground: %w", err))
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("playground not found"))
	}

	return connect.NewResponse(&datasetspb.DeletePlaygroundResponse{Success: true}), nil
}

// fetchPlayground loads a single non-archived playground scoped to the tenant,
// returning a connect NotFound error when no row matches.
func (s *PlaygroundServer) fetchPlayground(ctx context.Context, tenantID, id string) (*datasetspb.Playground, error) {
	var row playgroundRow
	err := s.db.GetContext(ctx, &row,
		`SELECT `+playgroundSelectCols+`
		 FROM playgrounds
		 WHERE id = $1 AND tenant_id = $2 AND archived_at IS NULL`,
		id, tenantID,
	)
	if err == sql.ErrNoRows {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("playground not found"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("fetch playground: %w", err))
	}
	pg, err := row.toProto(tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return pg, nil
}

// joinSets joins SET clause fragments with ", ".
func joinSets(sets []string) string {
	out := ""
	for i, s := range sets {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
