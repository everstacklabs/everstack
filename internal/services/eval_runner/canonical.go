package eval_runner

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// CanonicalizeInput converts an arbitrary JSON dataset-item input into a
// deterministic canonical form and returns the canonical bytes plus the hex
// sha256 of those bytes. The hash is the match key for eval-run comparison
// (see docs/design/evaluations-overhaul.md section 7a); v1 uses the identity
// projection, i.e. the whole canonical input is hashed.
//
// Canonical form rules:
//
//   - Objects have their keys sorted ascending (recursively), so key order
//     never affects the hash.
//   - Arrays are kept as arrays with element order preserved. Arrays are
//     deliberately NOT wrapped: a bare messages array and {"text": [...]}
//     are genuinely different model requests (buildChatRequest treats them
//     differently), so they must NOT hash equal. Note the frontend's
//     normalizeJsonObject wraps arrays for storage; the Go canonicalizer
//     intentionally does not mirror that.
//   - A scalar ROOT (string, number, bool, or JSON null) is wrapped as
//     {"text": <value>}, because a bare string input and the frontend's
//     stored-wrapped {"text": "..."} produce byte-identical model requests
//     and must hash equal. A null root becomes {"text": null}, which is
//     distinct from {"text": ""}. Non-root scalars are emitted as-is.
//   - Numbers are decoded with json.Decoder.UseNumber and re-emitted with
//     their original literal text, never through float64: 64-bit ids and
//     timestamps embedded in inputs collide past 2^53 as float64, which
//     would silently match different rows. (Trade-off: equal values with
//     different literals, e.g. 1e2 vs 100, hash differently; an acceptable
//     v1 under-match.)
//   - String values are emitted with standard JSON escaping and are not
//     otherwise normalized; whitespace inside strings is meaningful.
//
// Invalid JSON returns an error; the caller decides the fallback (the runner
// stores NULL hash and keeps the run alive).
func CanonicalizeInput(raw []byte) (canonical []byte, hash string, err error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return nil, "", fmt.Errorf("decode input JSON: %w", err)
	}
	if dec.More() {
		return nil, "", fmt.Errorf("decode input JSON: trailing data after top-level value")
	}

	// Root wrap: scalars (including null) become {"text": value}; objects and
	// arrays pass through unchanged.
	switch v.(type) {
	case map[string]interface{}, []interface{}:
		// keep as-is
	default:
		v = map[string]interface{}{"text": v}
	}

	var buf bytes.Buffer
	if err := writeCanonicalJSON(&buf, v); err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:]), nil
}

// writeCanonicalJSON emits v as compact JSON with object keys sorted
// ascending and json.Number literals preserved verbatim.
func writeCanonicalJSON(buf *bytes.Buffer, v interface{}) error {
	switch t := v.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, err := json.Marshal(k)
			if err != nil {
				return err
			}
			buf.Write(kb)
			buf.WriteByte(':')
			if err := writeCanonicalJSON(buf, t[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
		return nil
	case []interface{}:
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonicalJSON(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
		return nil
	case json.Number:
		// Preserve the original numeric literal exactly.
		buf.WriteString(t.String())
		return nil
	case string:
		sb, err := json.Marshal(t)
		if err != nil {
			return err
		}
		buf.Write(sb)
		return nil
	case bool:
		if t {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
		return nil
	case nil:
		buf.WriteString("null")
		return nil
	default:
		return fmt.Errorf("unexpected JSON value type %T", v)
	}
}
