// Package customcolumns resolves user-defined custom trace columns (traces-module
// -replan section 4.3, M2). Custom columns are distinct from the free-form
// metadata bag: a column is registered with a type and a source (a metadata
// path, a span attribute, or a score name), and at ingest time its typed value
// is resolved and written to the EAV sidecar. This package is the pure,
// persistence-agnostic resolution core; the Postgres registry and the
// observation_custom_values sidecar are the storage layers built on top.
package customcolumns

import (
	"strconv"
	"strings"
	"time"
)

// ValueType is the declared type of a custom column.
type ValueType string

const (
	TypeString ValueType = "string"
	TypeNumber ValueType = "number"
	TypeBool   ValueType = "bool"
	TypeDate   ValueType = "date"
)

// Source identifies where a column's value comes from.
type Source string

const (
	SourceMetadata  Source = "metadata_path"  // dotted path into the metadata JSON
	SourceAttribute Source = "attribute_path" // a span attribute key
	SourceScore     Source = "score_name"     // a score by name
)

// Column is a registered custom column definition.
type Column struct {
	Key       string
	Label     string
	ValueType ValueType
	Source    Source
	SourceRef string
}

// Value is a resolved, typed column value. Set reports whether a value was
// found; the typed field matching the column's ValueType holds the value.
type Value struct {
	String string
	Number float64
	Bool   bool
	Date   time.Time
	Set    bool
}

// Resolve extracts and coerces the column's value from a span's attributes,
// metadata map, and scores map. Returns Set=false when the source is absent or
// the value cannot be coerced to the declared type.
func (c Column) Resolve(attrs map[string]string, metadata map[string]interface{}, scores map[string]float64) Value {
	var raw interface{}
	switch c.Source {
	case SourceAttribute:
		if v, ok := attrs[c.SourceRef]; ok {
			raw = v
		}
	case SourceMetadata:
		raw = lookupPath(metadata, c.SourceRef)
	case SourceScore:
		if v, ok := scores[c.SourceRef]; ok {
			raw = v
		}
	}
	if raw == nil {
		return Value{}
	}
	return coerce(raw, c.ValueType)
}

// lookupPath walks a dotted path into a nested map[string]interface{}.
func lookupPath(m map[string]interface{}, path string) interface{} {
	if m == nil || path == "" {
		return nil
	}
	parts := strings.Split(path, ".")
	var cur interface{} = m
	for _, p := range parts {
		asMap, ok := cur.(map[string]interface{})
		if !ok {
			return nil
		}
		cur, ok = asMap[p]
		if !ok {
			return nil
		}
	}
	return cur
}

// coerce converts a raw value (string, number, or bool) to the declared type.
func coerce(raw interface{}, t ValueType) Value {
	switch t {
	case TypeNumber:
		if n, ok := toFloat(raw); ok {
			return Value{Number: n, Set: true}
		}
	case TypeBool:
		if b, ok := toBool(raw); ok {
			return Value{Bool: b, Set: true}
		}
	case TypeDate:
		if d, ok := toDate(raw); ok {
			return Value{Date: d, Set: true}
		}
	case TypeString:
		return Value{String: toString(raw), Set: true}
	}
	return Value{}
}

func toFloat(raw interface{}) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	case bool:
		if v {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

func toBool(raw interface{}) (bool, bool) {
	switch v := raw.(type) {
	case bool:
		return v, true
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		return b, err == nil
	case float64:
		return v != 0, true
	}
	return false, false
}

func toDate(raw interface{}) (time.Time, bool) {
	s, ok := raw.(string)
	if !ok {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02", time.RFC3339Nano} {
		if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func toString(raw interface{}) string {
	switch v := raw.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	}
	return ""
}
