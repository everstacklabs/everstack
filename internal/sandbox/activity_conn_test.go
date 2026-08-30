package sandbox

import (
	"bytes"
	"io"
	"testing"
)

type testReadWriteCloser struct {
	bytes.Buffer
}

func (c *testReadWriteCloser) Close() error {
	return nil
}

func TestActivityConnTouchesOnlyOnWrite(t *testing.T) {
	t.Parallel()

	var touches int
	base := &testReadWriteCloser{}
	conn := &activityConn{
		ReadWriteCloser: base,
		touch: func() {
			touches++
		},
	}

	if _, err := conn.Write([]byte("echo hi\n")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if touches != 1 {
		t.Fatalf("touches after write = %d, want 1", touches)
	}

	buf := make([]byte, 32)
	if _, err := conn.Read(buf); err != nil && err != io.EOF {
		t.Fatalf("Read() error = %v", err)
	}
	if touches != 1 {
		t.Fatalf("touches after read = %d, want unchanged 1", touches)
	}
}
