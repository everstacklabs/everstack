package v1

import (
	"testing"

	"github.com/everstacklabs/everstack/internal/customcolumns"
	tracespb "github.com/everstacklabs/everstack/pkg/grpc/everstack/traces/v1"
)

func TestCustomAttrColumnsFromDefs(t *testing.T) {
	defs := []customcolumns.StoredColumn{
		{Key: "model_id", Source: customcolumns.SourceAttribute, SourceRef: "gen_ai.request.model"},
		{Key: "tier", Source: customcolumns.SourceMetadata, SourceRef: "customer.tier"}, // metadata: excluded
		{Key: "noref", Source: customcolumns.SourceAttribute, SourceRef: ""},            // empty ref: excluded
		{Key: "bad key", Source: customcolumns.SourceAttribute, SourceRef: "x"},         // invalid key: excluded
		{Key: "region", Source: customcolumns.SourceAttribute, SourceRef: "app.region"},
	}
	got := customAttrColumnsFromDefs(defs)
	if len(got) != 2 {
		t.Fatalf("got %d attr columns, want 2: %+v", len(got), got)
	}
	if got[0].Key != "model_id" || got[0].Ref != "gen_ai.request.model" {
		t.Fatalf("first column = %+v, want model_id -> gen_ai.request.model", got[0])
	}
	if got[1].Key != "region" || got[1].Ref != "app.region" {
		t.Fatalf("second column = %+v, want region -> app.region", got[1])
	}
}

func TestHasScoreSourceDefs(t *testing.T) {
	if hasScoreSourceDefs([]customcolumns.StoredColumn{{Source: customcolumns.SourceMetadata}, {Source: customcolumns.SourceAttribute}}) {
		t.Fatal("no score source present, want false")
	}
	if !hasScoreSourceDefs([]customcolumns.StoredColumn{{Source: customcolumns.SourceMetadata}, {Source: customcolumns.SourceScore}}) {
		t.Fatal("score source present, want true")
	}
}

func TestResolveCustomColumns_Score(t *testing.T) {
	defs := []customcolumns.StoredColumn{
		{Key: "quality", Label: "Quality", ValueType: customcolumns.TypeNumber, Source: customcolumns.SourceScore, SourceRef: "quality"},
		{Key: "tier", Label: "Tier", ValueType: customcolumns.TypeString, Source: customcolumns.SourceMetadata, SourceRef: "tier"},
	}
	pb := &tracespb.Trace{Metadata: map[string]string{"tier": "gold"}}
	resolveCustomColumns(defs, pb, map[string]float64{"quality": 0.85})

	if pb.CustomColumns["quality"] != "0.85" {
		t.Fatalf("quality score column = %q, want 0.85", pb.CustomColumns["quality"])
	}
	if pb.CustomColumns["tier"] != "gold" {
		t.Fatalf("tier metadata column = %q, want gold", pb.CustomColumns["tier"])
	}
}

func TestResolveCustomColumns_ScoreNilSkips(t *testing.T) {
	defs := []customcolumns.StoredColumn{
		{Key: "quality", ValueType: customcolumns.TypeNumber, Source: customcolumns.SourceScore, SourceRef: "quality"},
	}
	pb := &tracespb.Trace{}
	resolveCustomColumns(defs, pb, nil) // no scores fetched
	if _, ok := pb.CustomColumns["quality"]; ok {
		t.Fatal("score column should be absent when no scores were provided")
	}
}

func TestCustomColumnValueString(t *testing.T) {
	cases := []struct {
		val  customcolumns.Value
		vt   customcolumns.ValueType
		want string
	}{
		{customcolumns.Value{String: "gpt-4", Set: true}, customcolumns.TypeString, "gpt-4"},
		{customcolumns.Value{Number: 42, Set: true}, customcolumns.TypeNumber, "42"},
		{customcolumns.Value{Bool: true, Set: true}, customcolumns.TypeBool, "true"},
	}
	for _, c := range cases {
		if got := customColumnValueString(c.val, c.vt); got != c.want {
			t.Errorf("customColumnValueString(%+v, %s) = %q, want %q", c.val, c.vt, got, c.want)
		}
	}
}
