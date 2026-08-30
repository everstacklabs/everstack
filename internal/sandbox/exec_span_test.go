package sandbox

import "testing"

func TestSandboxExecOp(t *testing.T) {
	cases := []struct {
		cmd  []string
		want string
	}{
		{[]string{"git", "clone", "x"}, "git"},
		{[]string{"/usr/bin/python3", "script.py"}, "python3"},
		{[]string{"bash", "-c", "echo hi"}, "bash"},
		{[]string{"./run.sh"}, "run.sh"},
		{nil, "exec"},
		{[]string{}, "exec"},
		{[]string{"/"}, "exec"},
	}
	for _, c := range cases {
		if got := sandboxExecOp(c.cmd); got != c.want {
			t.Errorf("sandboxExecOp(%v) = %q, want %q", c.cmd, got, c.want)
		}
	}
}

func TestTruncateForAttr(t *testing.T) {
	if got := truncateForAttr("short", 512); got != "short" {
		t.Errorf("short string changed: %q", got)
	}
	long := make([]byte, 600)
	for i := range long {
		long[i] = 'a'
	}
	if got := truncateForAttr(string(long), maxSandboxCmdAttr); len(got) != maxSandboxCmdAttr {
		t.Errorf("truncateForAttr len = %d, want %d", len(got), maxSandboxCmdAttr)
	}
}
