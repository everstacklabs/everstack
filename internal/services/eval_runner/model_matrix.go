package eval_runner

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ModelMatrixCell is one column of a model_matrix eval run: a (model,
// prompt_template_id, prompt_version) triple that the runner should execute
// every dataset item against. Each cell produces its own trace + eval_run_item.
//
// Empty PromptTemplateID + PromptVersion means "use the run's default prompt
// for this cell" — useful for matrices that only swap models.
type ModelMatrixCell struct {
	Model            string `json:"model"`
	PromptTemplateID string `json:"prompt_template_id,omitempty"`
	PromptVersion   string `json:"prompt_version,omitempty"`
}

// ModelMatrixConfig is parsed out of eval_runs.eval_config under the
// "model_matrix" key. A non-empty matrix expands the run's item set from
// N to N×len(matrix); each resulting eval_run_item carries a hypothesis_diff
// computed against the run's baseline cell (the first matrix entry).
type ModelMatrixConfig struct {
	Cells []ModelMatrixCell `json:"cells"`
}

// ErrEmptyModelMatrix signals the eval_config has an empty cells list.
var ErrEmptyModelMatrix = errors.New("model_matrix has no cells")

// ParseModelMatrix extracts the matrix from a raw eval_config JSON blob.
// Returns (nil, nil) when no matrix is configured (the runner should fall
// back to single-cell behavior). Returns an error only when the matrix key
// is present but malformed — silent fallback would mask config typos.
func ParseModelMatrix(raw []byte) (*ModelMatrixConfig, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("eval_config not a JSON object: %w", err)
	}
	matRaw, ok := top["model_matrix"]
	if !ok || len(matRaw) == 0 {
		return nil, nil
	}
	var mc ModelMatrixConfig
	if err := json.Unmarshal(matRaw, &mc); err != nil {
		return nil, fmt.Errorf("model_matrix malformed: %w", err)
	}
	if len(mc.Cells) == 0 {
		return nil, ErrEmptyModelMatrix
	}
	// Validate each cell carries at least a model — without it the runner
	// cannot dispatch a request.
	for i, c := range mc.Cells {
		if c.Model == "" {
			return nil, fmt.Errorf("model_matrix.cells[%d].model is required", i)
		}
	}
	return &mc, nil
}

// ExpandItems takes a list of dataset item IDs and a matrix, and returns the
// (item, cell, cell_index) triples the runner should execute. Cells are
// preserved in declaration order so the first cell is the implicit baseline
// for hypothesis-diff computation.
type ExpandedItem struct {
	DatasetItemID string
	Cell          ModelMatrixCell
	CellIndex     int
}

func ExpandItems(itemIDs []string, mc *ModelMatrixConfig) []ExpandedItem {
	if mc == nil || len(mc.Cells) == 0 {
		// Single-cell fallback — caller should treat this as the existing
		// "one row per item" behavior.
		return nil
	}
	out := make([]ExpandedItem, 0, len(itemIDs)*len(mc.Cells))
	for _, id := range itemIDs {
		for i, c := range mc.Cells {
			out = append(out, ExpandedItem{
				DatasetItemID: id,
				Cell:          c,
				CellIndex:     i,
			})
		}
	}
	return out
}
