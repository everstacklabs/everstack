package executors

import (
	"context"
	"encoding/base64"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	"github.com/everstacklabs/everstack/internal/workflows/engine"
)

type recordingBrowserArtifactStore struct {
	body []byte
}

func (s *recordingBrowserArtifactStore) UploadObject(
	_ context.Context,
	_, _, _, _ string,
	data io.Reader,
	_ int64,
	_, _ string,
) (string, error) {
	s.body, _ = io.ReadAll(data)
	return "object-browser-1", nil
}

func TestAgentEventSinkAdapterBoundsBrowserSnapshots(t *testing.T) {
	t.Parallel()

	var events []engine.ExecutionEvent
	adapter := &AgentEventSinkAdapter{
		tenantID:    "tenant-1",
		executionID: "execution-1",
		nodeID:      "agent-node",
		nodeType:    "agent",
		onEvent: func(event engine.ExecutionEvent) error {
			events = append(events, event)
			return nil
		},
	}

	err := adapter.OnEvent(agentrt.Event{
		Type:      agentrt.EventBrowserScreenshot,
		SessionID: "browser-session",
		Timestamp: time.Unix(100, 0),
		Data: map[string]interface{}{
			"image_base64": base64.StdEncoding.EncodeToString([]byte("jpeg")),
			"auto":         true,
		},
	})
	if err != nil {
		t.Fatalf("OnEvent() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}

	event := events[0]
	if event.Type != "agent.browser.snapshot" {
		t.Fatalf("event type = %q", event.Type)
	}
	if _, exists := event.Data["image_base64"]; exists {
		t.Fatal("workflow snapshot event retained inline base64")
	}
	if event.Data["snapshot_status"] != "unavailable" {
		t.Fatalf("snapshot_status = %q", event.Data["snapshot_status"])
	}
	if event.Data["sequence"] != "1" {
		t.Fatalf("sequence = %q, want 1", event.Data["sequence"])
	}
	if event.Data["browser_session_id"] != "browser-session" {
		t.Fatalf("browser_session_id = %q", event.Data["browser_session_id"])
	}
}

func TestAgentEventSinkAdapterStoresBrowserSnapshotAsArtifact(t *testing.T) {
	t.Parallel()

	store := &recordingBrowserArtifactStore{}
	var got engine.ExecutionEvent
	adapter := &AgentEventSinkAdapter{
		tenantID:    "tenant-1",
		executionID: "execution-1",
		nodeID:      "agent-node",
		nodeType:    "agent",
		artifacts:   store,
		onEvent: func(event engine.ExecutionEvent) error {
			got = event
			return nil
		},
	}

	image := []byte("jpeg-image")
	if err := adapter.OnEvent(agentrt.Event{
		Type:      agentrt.EventBrowserScreenshot,
		SessionID: "browser-session",
		Timestamp: time.Unix(100, 0),
		Data: map[string]interface{}{
			"image_base64": base64.StdEncoding.EncodeToString(image),
		},
	}); err != nil {
		t.Fatalf("OnEvent() error = %v", err)
	}

	if string(store.body) != string(image) {
		t.Fatalf("stored body = %q", store.body)
	}
	if got.Data["artifact_id"] != "object-browser-1" {
		t.Fatalf("artifact_id = %q", got.Data["artifact_id"])
	}
	if got.Data["snapshot_status"] != "stored" {
		t.Fatalf("snapshot_status = %q", got.Data["snapshot_status"])
	}
	if _, exists := got.Data["image_base64"]; exists {
		t.Fatal("stored snapshot event retained inline base64")
	}
}

func TestAgentEventSinkAdapterOrdersBrowserLifecycle(t *testing.T) {
	t.Parallel()

	var events []engine.ExecutionEvent
	adapter := &AgentEventSinkAdapter{
		nodeID:   "agent-node",
		nodeType: "agent",
		onEvent: func(event engine.ExecutionEvent) error {
			events = append(events, event)
			return nil
		},
	}

	for _, eventType := range []agentrt.EventType{
		agentrt.EventBrowserStart,
		agentrt.EventBrowserReady,
		agentrt.EventBrowserNavigate,
		agentrt.EventBrowserAction,
		agentrt.EventBrowserClose,
	} {
		if err := adapter.OnEvent(agentrt.Event{
			Type:      eventType,
			SessionID: "session-1",
			Timestamp: time.Now(),
		}); err != nil {
			t.Fatalf("OnEvent(%s) error = %v", eventType, err)
		}
	}

	wantTypes := []string{
		"agent.browser.started",
		"agent.browser.ready",
		"agent.browser.navigate",
		"agent.browser.action",
		"agent.browser.closed",
	}
	for i, wantType := range wantTypes {
		if events[i].Type != wantType {
			t.Fatalf("event %d type = %q, want %q", i, events[i].Type, wantType)
		}
		if events[i].Data["sequence"] != strconv.Itoa(i+1) {
			t.Fatalf("event %d sequence = %q", i, events[i].Data["sequence"])
		}
	}
}

func TestAgentEventSinkAdapterDoesNotRetainBrowserTopology(t *testing.T) {
	t.Parallel()

	var got engine.ExecutionEvent
	adapter := &AgentEventSinkAdapter{
		onEvent: func(event engine.ExecutionEvent) error {
			got = event
			return nil
		},
	}
	if err := adapter.OnEvent(agentrt.Event{
		Type:      agentrt.EventBrowserReady,
		SessionID: "session-1",
		Data: map[string]interface{}{
			"headless":         false,
			"stream_available": false,
			"stream_url":       "ws://10.0.0.12:6080/ws",
			"cdp_url":          "ws://10.0.0.12:9222/devtools/browser/id",
			"pod":              "browser-tenant-1",
		},
	}); err != nil {
		t.Fatalf("OnEvent() error = %v", err)
	}

	for _, key := range []string{"stream_url", "cdp_url", "pod"} {
		if _, exists := got.Data[key]; exists {
			t.Fatalf("workflow browser-ready event retained %s", key)
		}
	}
	if got.Data["stream_available"] != "false" {
		t.Fatalf("stream_available = %q, want false", got.Data["stream_available"])
	}
}

func TestAgentEventSinkAdapterRedactsBrowserTypedText(t *testing.T) {
	t.Parallel()

	var got engine.ExecutionEvent
	adapter := &AgentEventSinkAdapter{
		onEvent: func(event engine.ExecutionEvent) error {
			got = event
			return nil
		},
	}
	if err := adapter.OnEvent(agentrt.Event{
		Type:     agentrt.EventToolCallStart,
		ToolName: "browser_type",
		ToolArgs: `{"selector":"#password","text":"super-secret","submit":true}`,
	}); err != nil {
		t.Fatalf("OnEvent() error = %v", err)
	}
	if strings.Contains(got.Data["tool_args"], "super-secret") {
		t.Fatal("workflow event retained typed browser text")
	}
	if !strings.Contains(got.Data["tool_args"], `"selector":"#password"`) {
		t.Fatalf("tool args lost non-sensitive target: %s", got.Data["tool_args"])
	}
}
