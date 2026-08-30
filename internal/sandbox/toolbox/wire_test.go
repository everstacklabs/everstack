package toolbox

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExecRequestWireNames(t *testing.T) {
	req := ExecRequest{
		ID:        "exec-1",
		Command:   []string{"sh", "-lc", "pwd"},
		WorkDir:   "/workspace",
		Env:       map[string]string{"A": "B"},
		TimeoutMS: 5000,
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	for _, want := range []string{"\"timeout_ms\":5000", "\"work_dir\":\"/workspace\"", "\"command\""} {
		if !strings.Contains(got, want) {
			t.Fatalf("wire JSON %s missing %s", got, want)
		}
	}
}

func TestMountConfigWireNames(t *testing.T) {
	m := MountConfig{
		Type:            "r2",
		Bucket:          "bucket-a",
		MountPath:       "/mnt/data",
		AccessKeyID:     "key",
		SecretAccessKey: "secret",
		SessionToken:    "token",
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	for _, want := range []string{"\"mount_path\":\"/mnt/data\"", "\"access_key_id\":\"key\"", "\"secret_access_key\":\"secret\"", "\"session_token\":\"token\""} {
		if !strings.Contains(got, want) {
			t.Fatalf("wire JSON %s missing %s", got, want)
		}
	}
}

func TestSessionWireNames(t *testing.T) {
	list := SessionListResponse{
		NowUnix: 123,
		Sessions: []SessionInfo{{
			ID:               "sess-a",
			Attached:         true,
			CreatedUnix:      100,
			LastActivityUnix: 120,
		}},
	}
	b, err := json.Marshal(list)
	if err != nil {
		t.Fatalf("marshal list: %v", err)
	}
	got := string(b)
	for _, want := range []string{"\"now_unix\":123", "\"created_unix\":100", "\"last_activity_unix\":120"} {
		if !strings.Contains(got, want) {
			t.Fatalf("wire JSON %s missing %s", got, want)
		}
	}

	b, err = json.Marshal(SessionKillRequest{SessionID: "sess-a"})
	if err != nil {
		t.Fatalf("marshal kill: %v", err)
	}
	if got := string(b); got != "{\"session_id\":\"sess-a\"}" {
		t.Fatalf("kill wire JSON = %s", got)
	}
}
