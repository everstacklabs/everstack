package shellframe

import (
	"bytes"
	"io"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		typ     Type
		payload []byte
	}{
		{"stdin small", TypeStdin, []byte("ls -la\n")},
		{"stdout big", TypeStdout, bytes.Repeat([]byte("x"), 8000)},
		{"resize", TypeResize, EncodeResize(40, 132)},
		{"exit", TypeExit, EncodeExit(0)},
		{"zero payload", TypeStdin, nil},
		{"scrollback", TypeScrollback, []byte("$ echo hello\r\nhello\r\n$ ")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteFrame(&buf, tc.typ, tc.payload); err != nil {
				t.Fatalf("write: %v", err)
			}
			f, err := ReadFrame(&buf)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if f.Type != tc.typ {
				t.Fatalf("type: got %#x, want %#x", f.Type, tc.typ)
			}
			if !bytes.Equal(f.Payload, tc.payload) {
				t.Fatalf("payload mismatch")
			}
		})
	}
}

func TestFrameMultipleSequential(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, TypeStdin, []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := WriteFrame(&buf, TypeResize, EncodeResize(10, 20)); err != nil {
		t.Fatal(err)
	}
	if err := WriteFrame(&buf, TypeStdin, []byte("b")); err != nil {
		t.Fatal(err)
	}

	want := []struct {
		t Type
		p string
	}{
		{TypeStdin, "a"},
		{TypeResize, "\x00\x0a\x00\x14"},
		{TypeStdin, "b"},
	}
	for i, w := range want {
		f, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("read[%d]: %v", i, err)
		}
		if f.Type != w.t {
			t.Fatalf("read[%d] type: got %#x, want %#x", i, f.Type, w.t)
		}
		if string(f.Payload) != w.p {
			t.Fatalf("read[%d] payload: got %q, want %q", i, f.Payload, w.p)
		}
	}
	if _, err := ReadFrame(&buf); err != io.EOF {
		t.Fatalf("trailing read: got %v, want EOF", err)
	}
}

func TestPayloadCap(t *testing.T) {
	var buf bytes.Buffer
	huge := make([]byte, MaxPayload+1)
	err := WriteFrame(&buf, TypeStdout, huge)
	if err == nil {
		t.Fatal("expected error on oversized payload")
	}
}

func TestResizeRoundTrip(t *testing.T) {
	payload := EncodeResize(42, 200)
	rows, cols, err := DecodeResize(payload)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 42 || cols != 200 {
		t.Fatalf("got rows=%d cols=%d", rows, cols)
	}
	if _, _, err := DecodeResize([]byte{1, 2, 3}); err == nil {
		t.Fatal("expected error on short payload")
	}
}

func TestExitRoundTrip(t *testing.T) {
	for _, code := range []int32{0, 1, 127, -1} {
		code := code
		t.Run("", func(t *testing.T) {
			got, err := DecodeExit(EncodeExit(code))
			if err != nil {
				t.Fatal(err)
			}
			if got != code {
				t.Fatalf("got %d want %d", got, code)
			}
		})
	}
}
