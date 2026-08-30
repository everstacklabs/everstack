package v1

import (
	"context"
	"encoding/json"
	"time"

	datasetspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/datasets/v1"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"google.golang.org/protobuf/types/known/structpb"
)

func insertGeneratedDatasetItem(
	ctx context.Context,
	tx *sqlx.Tx,
	datasetID string,
	tenantID string,
	input map[string]interface{},
	expectedOutput map[string]interface{},
	metadata map[string]interface{},
	now time.Time,
) (*datasetspb.DatasetItem, error) {
	inputJSON, _ := json.Marshal(input)
	expectedOutputJSON, _ := json.Marshal(expectedOutput)
	metadataJSON, _ := json.Marshal(metadata)
	if expectedOutput == nil {
		expectedOutputJSON = nil
	}

	id := uuid.New().String()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO dataset_items (
			id, dataset_id, tenant_id, input, expected_output, metadata,
			source_trace_id, source_observation_id, status, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4::jsonb, $5::jsonb, $6::jsonb,
			'', '', 'active', $7, $8
		)
	`, id, datasetID, tenantID, string(inputJSON), jsonOrNil(expectedOutputJSON), string(metadataJSON), now, now); err != nil {
		return nil, err
	}

	inputStruct, _ := structpb.NewStruct(input)
	metadataStruct, _ := structpb.NewStruct(metadata)
	item := &datasetspb.DatasetItem{
		Id:        id,
		DatasetId: datasetID,
		TenantId:  tenantID,
		Input:     inputStruct,
		Metadata:  metadataStruct,
		Status:    "active",
		CreatedAt: timestamppbNow(now),
		UpdatedAt: timestamppbNow(now),
	}
	if expectedOutput != nil {
		item.ExpectedOutput, _ = structpb.NewStruct(expectedOutput)
	}
	return item, nil
}
