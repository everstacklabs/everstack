package v1

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	eval_runner "github.com/everstacklabs/everstack/internal/services/eval_runner"
	datasetspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/datasets/v1"
)

func (s *DatasetServer) GenerateRedTeamDataset(
	ctx context.Context,
	req *connect.Request[datasetspb.GenerateRedTeamDatasetRequest],
) (*connect.Response[datasetspb.GenerateRedTeamDatasetResponse], error) {
	tenantID, err := requireTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	datasetID := strings.TrimSpace(req.Msg.GetDatasetId())
	if datasetID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("dataset id is required"))
	}
	ctx = ensureTenantSchema(ctx, tenantID)

	attacksPerCategory := clampRedTeamAttacksPerCategory(req.Msg.GetAttacksPerCategory())
	enhancement := strings.TrimSpace(strings.ToLower(req.Msg.GetEnhancement()))
	if enhancement == "" {
		enhancement = string(eval_runner.RedTeamEnhancementNone)
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

	categories := normalizeRedTeamCategories(req.Msg.GetCategories())
	if len(categories) == 0 {
		categories = eval_runner.RedTeamAttackCategories()
	}

	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	var itemsCreated int32
	var categoriesUsed []string
	for _, category := range categories {
		attacks := eval_runner.RedTeamAttacksByCategory(category)
		if len(attacks) == 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unknown red-team category: "+category))
		}
		if limit := int(attacksPerCategory); limit < len(attacks) {
			attacks = attacks[:limit]
		}

		var categoryCreated int32
		for _, attack := range attacks {
			prompt, ok := eval_runner.ApplyRedTeamEnhancement(attack.PromptTemplate, enhancement)
			if !ok {
				return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unknown red-team enhancement: "+enhancement))
			}
			input := map[string]interface{}{
				"messages": []interface{}{
					map[string]interface{}{
						"role":    "user",
						"content": prompt,
					},
				},
			}
			expectedOutput := map[string]interface{}{
				"expected_behavior": "refuse",
			}
			metadata := map[string]interface{}{
				"redteam_category": category,
				"enhancement":      enhancement,
				"attack_key":       attack.Key,
				"attack_name":      attack.Name,
				"generated":        true,
				"generator":        "redteam",
			}

			if _, err := insertGeneratedDatasetItem(ctx, tx, datasetID, tenantID, input, expectedOutput, metadata, now); err != nil {
				return nil, connect.NewError(connect.CodeInternal, err)
			}
			itemsCreated++
			categoryCreated++
		}
		if categoryCreated > 0 {
			categoriesUsed = append(categoriesUsed, category)
		}
	}

	if itemsCreated == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("no red-team dataset items generated"))
	}
	if err := tx.Commit(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&datasetspb.GenerateRedTeamDatasetResponse{
		ItemsCreated:   itemsCreated,
		CategoriesUsed: categoriesUsed,
	}), nil
}

func normalizeRedTeamCategories(categories []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(categories))
	for _, category := range categories {
		category = strings.TrimSpace(strings.ToLower(category))
		if category == "" || seen[category] {
			continue
		}
		seen[category] = true
		out = append(out, category)
	}
	return out
}

func clampRedTeamAttacksPerCategory(n int32) int32 {
	if n < 1 {
		return 1
	}
	if n > 20 {
		return 20
	}
	return n
}
