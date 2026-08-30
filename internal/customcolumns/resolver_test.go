package customcolumns

import (
	"testing"
	"time"
)

func TestResolveFromAttribute(t *testing.T) {
	c := Column{Key: "tier", ValueType: TypeString, Source: SourceAttribute, SourceRef: "customer.tier"}
	v := c.Resolve(map[string]string{"customer.tier": "enterprise"}, nil, nil)
	if !v.Set || v.String != "enterprise" {
		t.Errorf("attribute resolve = %+v, want enterprise", v)
	}
	// Absent attribute -> not set.
	if got := c.Resolve(map[string]string{}, nil, nil); got.Set {
		t.Errorf("absent attribute should be unset, got %+v", got)
	}
}

func TestResolveNumberFromAttributeString(t *testing.T) {
	c := Column{ValueType: TypeNumber, Source: SourceAttribute, SourceRef: "retries"}
	v := c.Resolve(map[string]string{"retries": "3"}, nil, nil)
	if !v.Set || v.Number != 3 {
		t.Errorf("number coerce = %+v, want 3", v)
	}
	// Non-numeric string -> not set.
	if got := (Column{ValueType: TypeNumber, Source: SourceAttribute, SourceRef: "x"}).
		Resolve(map[string]string{"x": "abc"}, nil, nil); got.Set {
		t.Errorf("non-numeric should be unset, got %+v", got)
	}
}

func TestResolveFromMetadataPath(t *testing.T) {
	c := Column{ValueType: TypeBool, Source: SourceMetadata, SourceRef: "flags.beta"}
	md := map[string]interface{}{"flags": map[string]interface{}{"beta": true}}
	v := c.Resolve(nil, md, nil)
	if !v.Set || !v.Bool {
		t.Errorf("metadata path resolve = %+v, want true", v)
	}
	// Missing nested key -> not set.
	if got := c.Resolve(nil, map[string]interface{}{"flags": map[string]interface{}{}}, nil); got.Set {
		t.Errorf("missing nested key should be unset, got %+v", got)
	}
}

func TestResolveFromScore(t *testing.T) {
	c := Column{ValueType: TypeNumber, Source: SourceScore, SourceRef: "helpfulness"}
	v := c.Resolve(nil, nil, map[string]float64{"helpfulness": 0.8})
	if !v.Set || v.Number != 0.8 {
		t.Errorf("score resolve = %+v, want 0.8", v)
	}
}

func TestResolveDate(t *testing.T) {
	c := Column{ValueType: TypeDate, Source: SourceAttribute, SourceRef: "released"}
	v := c.Resolve(map[string]string{"released": "2026-06-21"}, nil, nil)
	if !v.Set || v.Date.Year() != 2026 || v.Date.Month() != time.June {
		t.Errorf("date resolve = %+v, want 2026-06", v)
	}
}
