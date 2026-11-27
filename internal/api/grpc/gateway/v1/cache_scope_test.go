package v1

import (
	"bytes"
	"testing"

	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
)

func pbChat(system, user string, tools ...string) *gatewaypb.ChatCompletionRequest {
	msgs := []*gatewaypb.Message{}
	if system != "" {
		msgs = append(msgs, &gatewaypb.Message{Role: gatewaypb.Role_ROLE_SYSTEM, Content: []*gatewaypb.ContentPart{{Type: "text", Data: &gatewaypb.ContentPart_Text{Text: system}}}})
	}
	msgs = append(msgs, &gatewaypb.Message{Role: gatewaypb.Role_ROLE_USER, Content: []*gatewaypb.ContentPart{{Type: "text", Data: &gatewaypb.ContentPart_Text{Text: user}}}})
	req := &gatewaypb.ChatCompletionRequest{Model: "gpt-4o-mini", Messages: msgs}
	for _, name := range tools {
		req.Tools = append(req.Tools, &gatewaypb.Tool{Type: "function", Function: &gatewaypb.ToolFunctionDef{Name: name}})
	}
	return req
}

func TestGRPCCacheFingerprintCoversOutputAffectingFields(t *testing.T) {
	base := (&grpcRequestAdapter{req: pbChat("", "hi")}).CacheFingerprint()
	if base == nil {
		t.Fatal("no fingerprint")
	}
	if bytes.Equal(base, (&grpcRequestAdapter{req: pbChat("secret instructions", "hi")}).CacheFingerprint()) {
		t.Fatal("system prompt missing from exact fingerprint")
	}
	if bytes.Equal(base, (&grpcRequestAdapter{req: pbChat("", "hi", "lookup_customer")}).CacheFingerprint()) {
		t.Fatal("tools missing from exact fingerprint")
	}
	stream := true
	streamed := pbChat("", "hi")
	streamed.Stream = &stream
	if !bytes.Equal(base, (&grpcRequestAdapter{req: streamed}).CacheFingerprint()) {
		t.Fatal("stream hint should not change the fingerprint")
	}
}

func TestGRPCSemanticContextExcludesOnlyTheQuery(t *testing.T) {
	a := (&grpcRequestAdapter{req: pbChat("sys", "question one")}).SemanticContextFingerprint()
	b := (&grpcRequestAdapter{req: pbChat("sys", "question two")}).SemanticContextFingerprint()
	if !bytes.Equal(a, b) {
		t.Fatal("final user text leaked into the semantic context")
	}
	c := (&grpcRequestAdapter{req: pbChat("", "question one")}).SemanticContextFingerprint()
	if bytes.Equal(a, c) {
		t.Fatal("system prompt missing from the semantic context")
	}
	req := pbChat("sys", "question one")
	_ = (&grpcRequestAdapter{req: req}).SemanticContextFingerprint()
	if req.Messages[1].GetContent()[0].GetText() != "question one" {
		t.Fatal("fingerprinting mutated the live request")
	}
}

// Lookups and stores of one request share the adapter, so the proto is cloned
// and marshaled once per fingerprint kind, not once per cache call.
func TestGRPCCacheFingerprintComputedOnce(t *testing.T) {
	a := &grpcRequestAdapter{req: pbChat("sys", "hi")}
	f1, f2 := a.CacheFingerprint(), a.CacheFingerprint()
	s1, s2 := a.SemanticContextFingerprint(), a.SemanticContextFingerprint()
	if &f1[0] != &f2[0] || &s1[0] != &s2[0] {
		t.Fatal("fingerprint recomputed on repeat call")
	}
}
