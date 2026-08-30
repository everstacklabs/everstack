package eval_runner

import (
	"strings"
	"testing"
)

func mustCanonicalize(t *testing.T, raw string) (string, string) {
	t.Helper()
	canonical, hash, err := CanonicalizeInput([]byte(raw))
	if err != nil {
		t.Fatalf("CanonicalizeInput(%q) returned error: %v", raw, err)
	}
	if len(hash) != 64 || strings.ToLower(hash) != hash {
		t.Fatalf("CanonicalizeInput(%q) hash %q is not lowercase hex sha256", raw, hash)
	}
	return string(canonical), hash
}

func TestCanonicalizeInput_HashEquivalence(t *testing.T) {
	tests := []struct {
		name      string
		a, b      string
		wantEqual bool
	}{
		{
			name:      "key order independence",
			a:         `{"a":1,"b":2}`,
			b:         `{"b":2,"a":1}`,
			wantEqual: true,
		},
		{
			name:      "scalar root wraps as text",
			a:         `"hello"`,
			b:         `{"text":"hello"}`,
			wantEqual: true,
		},
		{
			name:      "array root is NOT wrapped",
			a:         `[{"role":"user"}]`,
			b:         `{"text":[{"role":"user"}]}`,
			wantEqual: false,
		},
		{
			name:      "large ints above 2^53 do not collide",
			a:         `{"id": 9007199254740993}`,
			b:         `{"id": 9007199254740992}`,
			wantEqual: false,
		},
		{
			name:      "null root differs from empty text",
			a:         `null`,
			b:         `{"text":""}`,
			wantEqual: false,
		},
		{
			name:      "null root wraps as text null",
			a:         `null`,
			b:         `{"text":null}`,
			wantEqual: true,
		},
		{
			name:      "nested key sorting is recursive",
			a:         `{"outer":{"z":1,"a":{"y":2,"b":3}},"list":[{"d":4,"c":5}]}`,
			b:         `{"list":[{"c":5,"d":4}],"outer":{"a":{"b":3,"y":2},"z":1}}`,
			wantEqual: true,
		},
		{
			name:      "array element order is preserved",
			a:         `[1,2]`,
			b:         `[2,1]`,
			wantEqual: false,
		},
		{
			name:      "whitespace inside strings is meaningful",
			a:         `{"text":"a b"}`,
			b:         `{"text":"a  b"}`,
			wantEqual: false,
		},
		{
			name:      "insignificant whitespace outside strings is ignored",
			a:         `{ "a" : 1 , "b" : [ 1 , 2 ] }`,
			b:         `{"a":1,"b":[1,2]}`,
			wantEqual: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ca, ha := mustCanonicalize(t, tt.a)
			cb, hb := mustCanonicalize(t, tt.b)
			if (ha == hb) != tt.wantEqual {
				t.Errorf("hash equality = %v, want %v\n a: %s -> %s (%s)\n b: %s -> %s (%s)",
					ha == hb, tt.wantEqual, tt.a, ca, ha, tt.b, cb, hb)
			}
			// Hash equality must track canonical-bytes equality exactly.
			if (ca == cb) != (ha == hb) {
				t.Errorf("canonical bytes equality (%v) disagrees with hash equality (%v)", ca == cb, ha == hb)
			}
		})
	}
}

func TestCanonicalizeInput_CanonicalForm(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "sorted compact object", raw: `{"b": 2, "a": 1}`, want: `{"a":1,"b":2}`},
		{name: "scalar string root", raw: `"hi"`, want: `{"text":"hi"}`},
		{name: "scalar number root preserves literal", raw: `9007199254740993`, want: `{"text":9007199254740993}`},
		{name: "bool root", raw: `true`, want: `{"text":true}`},
		{name: "null root", raw: `null`, want: `{"text":null}`},
		{name: "array root untouched", raw: ` [ 1 , "x" ] `, want: `[1,"x"]`},
		{name: "non-root scalar not wrapped", raw: `{"a":"x"}`, want: `{"a":"x"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := mustCanonicalize(t, tt.raw)
			if got != tt.want {
				t.Errorf("canonical = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestCanonicalizeInput_Deterministic(t *testing.T) {
	raw := `{"z":{"b":[1,2,{"y":true,"a":null}],"a":"s"},"m":9007199254740993}`
	c1, h1 := mustCanonicalize(t, raw)
	for i := 0; i < 20; i++ {
		c2, h2 := mustCanonicalize(t, raw)
		if c1 != c2 || h1 != h2 {
			t.Fatalf("non-deterministic output on iteration %d: %s / %s vs %s / %s", i, c1, h1, c2, h2)
		}
	}
}

func TestCanonicalizeInput_InvalidJSON(t *testing.T) {
	for _, raw := range []string{``, `{`, `{"a":}`, `hello`, `{"a":1} trailing`} {
		t.Run(raw, func(t *testing.T) {
			if _, _, err := CanonicalizeInput([]byte(raw)); err == nil {
				t.Errorf("CanonicalizeInput(%q) = nil error, want error", raw)
			}
		})
	}
}
