package eval_runner

import (
	"errors"
	"testing"
)

func TestParseModelMatrix_Missing(t *testing.T) {
	mc, err := ParseModelMatrix([]byte(`{"foo":"bar"}`))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if mc != nil {
		t.Fatalf("expected nil matrix when key absent, got %+v", mc)
	}
}

func TestParseModelMatrix_Empty(t *testing.T) {
	_, err := ParseModelMatrix([]byte(`{"model_matrix":{"cells":[]}}`))
	if !errors.Is(err, ErrEmptyModelMatrix) {
		t.Fatalf("expected ErrEmptyModelMatrix, got %v", err)
	}
}

func TestParseModelMatrix_MissingModelInCell(t *testing.T) {
	_, err := ParseModelMatrix([]byte(`{"model_matrix":{"cells":[{"prompt_template_id":"x"}]}}`))
	if err == nil {
		t.Fatal("expected error for cell missing model")
	}
}

func TestParseModelMatrix_Valid(t *testing.T) {
	mc, err := ParseModelMatrix([]byte(`{"model_matrix":{"cells":[
		{"model":"claude-opus-4-7"},
		{"model":"gpt-5","prompt_version":"2"}
	]}}`))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(mc.Cells) != 2 {
		t.Fatalf("expected 2 cells, got %d", len(mc.Cells))
	}
	if mc.Cells[1].PromptVersion != "2" {
		t.Fatalf("expected prompt_version=2 on cell 2, got %q", mc.Cells[1].PromptVersion)
	}
}

func TestExpandItems_FansOut(t *testing.T) {
	mc := &ModelMatrixConfig{Cells: []ModelMatrixCell{
		{Model: "a"},
		{Model: "b"},
	}}
	items := []string{"item-1", "item-2"}
	got := ExpandItems(items, mc)
	if len(got) != 4 {
		t.Fatalf("expected 4 expansions, got %d", len(got))
	}
	if got[0].DatasetItemID != "item-1" || got[0].Cell.Model != "a" || got[0].CellIndex != 0 {
		t.Errorf("unexpected first expansion: %+v", got[0])
	}
	if got[3].DatasetItemID != "item-2" || got[3].Cell.Model != "b" || got[3].CellIndex != 1 {
		t.Errorf("unexpected last expansion: %+v", got[3])
	}
}

func TestExpandItems_NilMatrix(t *testing.T) {
	if got := ExpandItems([]string{"a"}, nil); got != nil {
		t.Fatalf("nil matrix should return nil, got %+v", got)
	}
}
