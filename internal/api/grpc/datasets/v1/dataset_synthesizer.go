package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	datasetspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/datasets/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type generatedDatasetItem struct {
	Input          interface{} `json:"input"`
	ExpectedOutput interface{} `json:"expected_output"`
}

func (s *DatasetServer) GenerateDatasetItems(
	ctx context.Context,
	req *connect.Request[datasetspb.GenerateDatasetItemsRequest],
) (*connect.Response[datasetspb.GenerateDatasetItemsResponse], error) {
	tenantID, err := requireTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	datasetID := strings.TrimSpace(req.Msg.GetDatasetId())
	if datasetID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("dataset id is required"))
	}
	ctx = ensureTenantSchema(ctx, tenantID)

	count := clampDatasetGenerationCount(req.Msg.GetCount())
	instructions := strings.TrimSpace(req.Msg.GetInstructions())
	if instructions == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("instructions are required"))
	}

	db, err := s.getPrimaryDB(ctx)
	if err != nil {
		return nil, err
	}
	var exists bool
	if err := db.GetContext(ctx, &exists, `
		SELECT EXISTS (
			SELECT 1 FROM datasets
			WHERE id = $1 AND tenant_id = $2 AND archived_at IS NULL
		)
	`, datasetID, tenantID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if !exists {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("dataset not found"))
	}

	model := strings.TrimSpace(req.Msg.GetModel())
	if model == "" {
		model = defaultDatasetGeneratorModel()
	}
	content, err := generateChatCompletionContent(ctx, tenantID, map[string]interface{}{
		"model": model,
		"messages": []map[string]interface{}{
			{"role": "system", "content": datasetGeneratorSystemPrompt()},
			{"role": "user", "content": buildDatasetGeneratorPrompt(count, instructions, req.Msg.GetContext(), req.Msg.GetStyle())},
		},
		"stream": false,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	generated, err := parseGeneratedDatasetItems(content)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("parse model output: %w", err))
	}

	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	var items []*datasetspb.DatasetItem
	for _, gen := range generated {
		input, ok := generatedValueToMap(gen.Input)
		if !ok {
			continue
		}
		expectedOutput, _ := generatedValueToMap(gen.ExpectedOutput)
		metadata := map[string]interface{}{
			"generated": true,
			"generator": "synthesizer",
			"model":     model,
		}
		if req.Msg.GetStyle() != "" {
			metadata["style"] = req.Msg.GetStyle()
		}

		item, err := insertGeneratedDatasetItem(ctx, tx, datasetID, tenantID, input, expectedOutput, metadata, now)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		items = append(items, item)
	}

	if len(items) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("model returned no usable dataset items"))
	}
	if err := tx.Commit(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&datasetspb.GenerateDatasetItemsResponse{
		ItemsCreated: int32(len(items)),
		Items:        items,
	}), nil
}

func parseGeneratedDatasetItems(content string) ([]generatedDatasetItem, error) {
	content = stripJSONCodeFence(content)

	var items []generatedDatasetItem
	if err := json.Unmarshal([]byte(content), &items); err == nil {
		return items, nil
	}

	var wrapped struct {
		Items []generatedDatasetItem `json:"items"`
	}
	if err := json.Unmarshal([]byte(content), &wrapped); err != nil {
		return nil, err
	}
	if wrapped.Items == nil {
		return nil, errors.New("model response missing JSON array")
	}
	return wrapped.Items, nil
}

func generatedValueToMap(v interface{}) (map[string]interface{}, bool) {
	if v == nil {
		return nil, false
	}
	if m, ok := v.(map[string]interface{}); ok {
		if len(m) == 0 {
			return nil, false
		}
		return m, true
	}
	if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
		return nil, false
	}
	return map[string]interface{}{"value": v}, true
}

func clampDatasetGenerationCount(count int32) int32 {
	if count < 1 {
		return 1
	}
	if count > 50 {
		return 50
	}
	return count
}

func buildDatasetGeneratorPrompt(count int32, instructions, contextText, style string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Generate exactly %d dataset items.\n\n", count))
	b.WriteString("Instructions:\n")
	b.WriteString(instructions)
	if strings.TrimSpace(contextText) != "" {
		b.WriteString("\n\nContext:\n")
		b.WriteString(contextText)
	}
	if strings.TrimSpace(style) != "" {
		b.WriteString("\n\nStyle:\n")
		b.WriteString(style)
	}
	b.WriteString("\n\nReturn only the JSON array now.")
	return b.String()
}

func datasetGeneratorSystemPrompt() string {
	return `You generate evaluation dataset items. Return ONLY a JSON array, with no markdown fences or preamble. Each array element must be an object with exactly these fields:

  "input": an object containing the test input for the system under evaluation.
  "expected_output": an object containing the ideal or expected result.

Make the items diverse, realistic, and directly aligned with the user's instructions.`
}

func defaultDatasetGeneratorModel() string {
	if v := os.Getenv("MF_DATASET_GEN_MODEL"); v != "" {
		return v
	}
	return defaultScorerModel()
}

func jsonOrNil(data []byte) interface{} {
	if data == nil {
		return nil
	}
	return string(data)
}

func timestamppbNow(t time.Time) *timestamppb.Timestamp {
	return timestamppb.New(t)
}
