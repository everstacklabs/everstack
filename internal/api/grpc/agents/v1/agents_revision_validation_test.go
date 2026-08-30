package v1

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1/agentsconnect"
	"google.golang.org/protobuf/proto"
)

func TestAgentRevisionRequestsEnforceTransportBounds(t *testing.T) {
	validFile := func() *agentspb.AgentRevisionFile {
		return &agentspb.AgentRevisionFile{
			Path:    "agent.yaml",
			Content: []byte("name: reviewer\n"),
			Mode:    0o644,
		}
	}

	tests := []struct {
		name    string
		message interface{ Validate() error }
	}{
		{
			name: "missing agent id",
			message: &agentspb.CreateAgentRevisionRequest{
				Files: []*agentspb.AgentRevisionFile{validFile()},
			},
		},
		{
			name:    "missing files",
			message: &agentspb.CreateAgentRevisionRequest{AgentId: "agent-1"},
		},
		{
			name: "oversized file",
			message: &agentspb.CreateAgentRevisionRequest{
				AgentId: "agent-1",
				Files: []*agentspb.AgentRevisionFile{{
					Path: "large.ts", Content: bytes.Repeat([]byte("x"), 2<<20+1), Mode: 0o644,
				}},
			},
		},
		{
			name: "invalid file mode",
			message: &agentspb.CreateAgentRevisionRequest{
				AgentId: "agent-1",
				Files:   []*agentspb.AgentRevisionFile{{Path: "agent.yaml", Mode: 0o1000}},
			},
		},
		{
			name: "invalid function runtime",
			message: &agentspb.CreateAgentRevisionRequest{
				AgentId: "agent-1",
				Files:   []*agentspb.AgentRevisionFile{validFile()},
				Functions: []*agentspb.AgentProjectFunction{{
					Name: "review", Path: "agent.yaml", Runtime: "shell",
				}},
			},
		},
		{
			name:    "missing revision id",
			message: &agentspb.GetAgentRevisionRequest{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.message.Validate(); err == nil {
				t.Fatal("invalid request passed generated validation")
			}
		})
	}

	valid := &agentspb.CreateAgentRevisionRequest{
		AgentId: "agent-1",
		Files:   []*agentspb.AgentRevisionFile{validFile()},
		Functions: []*agentspb.AgentProjectFunction{{
			Name: "review", Path: "agent.yaml", Runtime: "deno",
		}},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid request failed generated validation: %v", err)
	}
}

func TestCreateAgentRevisionRejectsOversizedWireMessageBeforeHandler(t *testing.T) {
	files := make([]*agentspb.AgentRevisionFile, 11)
	for i := range files {
		files[i] = &agentspb.AgentRevisionFile{
			Path:    "source/file-" + string(rune('a'+i)) + ".ts",
			Content: bytes.Repeat([]byte("x"), 2<<20),
			Mode:    0o644,
		}
	}
	request := &agentspb.CreateAgentRevisionRequest{
		AgentId: "agent-1",
		Files:   files,
	}
	if size := proto.Size(request); size <= maxCreateAgentRevisionReadBytes {
		t.Fatalf("test request is %d bytes, want more than %d", size, maxCreateAgentRevisionReadBytes)
	}

	pattern, handler := CreateServer().RegisterConnectServer()
	mux := http.NewServeMux()
	mux.Handle(pattern, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := agentsconnect.NewAgentsServiceClient(server.Client(), server.URL)
	_, err := client.CreateAgentRevision(context.Background(), connect.NewRequest(request))
	if code := connect.CodeOf(err); code != connect.CodeResourceExhausted {
		t.Fatalf("CreateAgentRevision() code = %v, want %v (err = %v)", code, connect.CodeResourceExhausted, err)
	}
}
