package v1

import (
	"encoding/json"
	"reflect"
	"testing"

	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// TestToGatewayRequestThreadsOpenAIFields decodes the proto JSON the REST
// transform produces exactly as the grpc-gateway does (DiscardUnknown) and
// checks that every threaded OpenAI field reaches the gateway request.
func TestToGatewayRequestThreadsOpenAIFields(t *testing.T) {
	in := `{
		"model": "gpt-4o",
		"messages": [
			{"role": "ROLE_USER", "name": "alice", "content": [{"type": "image_url", "image_url": "https://img", "image_detail": "low"}]}
		],
		"tools": [{"type": "function", "function": {"name": "lookup", "parameters": {"type": "object"}, "strict": true}}],
		"sampling": {"seed": 7, "top_k": 5, "verbosity": "low", "logprobs": true, "top_logprobs": 3, "logit_bias": {"50256": -100}},
		"response_format": {"type": "json_schema", "json_schema": {"name": "answer", "description": "d", "schema": {"type": "object"}, "strict": true}},
		"service_tier": "flex"
	}`
	var pb gatewaypb.ChatCompletionRequest
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal([]byte(in), &pb); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(toGatewayRequest(&pb))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}

	var want map[string]interface{}
	if err := json.Unmarshal([]byte(`{
		"message": {"role": "user", "name": "alice", "content": [{"type": "image_url", "image_url": "https://img", "image_detail": "low"}]},
		"tool": {"type": "function", "function": {"name": "lookup", "parameters": {"type": "object"}, "strict": true}},
		"sampling": {"seed": 7, "top_k": 5, "verbosity": "low", "logprobs": true, "top_logprobs": 3, "logit_bias": {"50256": -100}},
		"response_format": {"type": "json_schema", "json_schema": {"name": "answer", "description": "d", "schema": {"type": "object"}, "strict": true}},
		"service_tier": "flex"
	}`), &want); err != nil {
		t.Fatal(err)
	}
	messages, _ := got["messages"].([]interface{})
	tools, _ := got["tools"].([]interface{})
	var gotMessage, gotTool interface{}
	if len(messages) == 1 {
		gotMessage = messages[0]
	}
	if len(tools) == 1 {
		gotTool = tools[0]
	}
	checks := map[string][2]interface{}{
		"message":         {gotMessage, want["message"]},
		"tool":            {gotTool, want["tool"]},
		"sampling":        {got["sampling"], want["sampling"]},
		"response_format": {got["response_format"], want["response_format"]},
		"service_tier":    {got["service_tier"], want["service_tier"]},
	}
	for name, pair := range checks {
		if !reflect.DeepEqual(pair[0], pair[1]) {
			g, _ := json.Marshal(pair[0])
			w, _ := json.Marshal(pair[1])
			t.Errorf("%s\n got: %s\nwant: %s", name, g, w)
		}
	}
}

func TestToGatewayRequestJSONObjectResponseFormat(t *testing.T) {
	var pb gatewaypb.ChatCompletionRequest
	if err := protojson.Unmarshal([]byte(`{"model":"m","response_format":{"type":"json_object"}}`), &pb); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(toGatewayRequest(&pb))
	var got map[string]interface{}
	_ = json.Unmarshal(raw, &got)
	if !reflect.DeepEqual(got["response_format"], map[string]interface{}{"type": "json_object"}) {
		t.Fatalf("response_format = %#v", got["response_format"])
	}
}
