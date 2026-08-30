package eval_runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/everstacklabs/everstack/internal/api/internalauth"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Multi-turn agent simulation.
//
// Drives a synthetic user persona through N turns against a target
// chat-completion model (or agent), recording every turn as it goes.
// The resulting trace is queryable in /observability/traces and scorable
// via any sampling-eval rule or one-off eval run that filters on the
// scenario id / tags.
//
// Direct attack on Braintrust's multi-turn simulation wedge. Langfuse
// has no equivalent.

// SimulationScenario describes a single multi-turn test case.
type SimulationScenario struct {
	// Unique-ish id, surfaced as trace metadata for filtering later.
	ID string

	// Free-text persona / role-play prompt. Drives the simulated user.
	// Example:
	//   "You are a frustrated customer trying to cancel an annual
	//    subscription. The agent will try to retain you. Push back
	//    politely but firmly. Refuse to give a credit card."
	Persona string

	// Optional kick-off message the persona sends on turn 1. If empty,
	// the persona model is asked to write its own opener.
	InitialMessage string

	// Max number of full back-and-forth turns. 1 turn = persona then
	// target. Defaults to 5.
	MaxTurns int

	// Termination heuristic: if either side outputs a string from this
	// list (case-insensitive), the simulation stops early. Defaults to
	// ["[END]", "<<END>>"].
	StopOnMatch []string

	// Target chat model that the persona is talking *to*. Required.
	// Example: "@openai/gpt-4o-mini", "@anthropic/claude-sonnet-4-6".
	TargetModel string

	// Model used to act as the persona. Defaults to the same target
	// for symmetric tests; can be a different model to avoid
	// self-grading bias.
	PersonaModel string

	// Optional metadata stamped onto the resulting trace.
	Tags map[string]string
}

// SimulationResult is what a Run returns.
type SimulationResult struct {
	ScenarioID    string
	TraceID       string
	Turns         []SimulationTurn
	StoppedReason string // "max_turns", "stop_match", "error"
	Error         string
}

// SimulationTurn is one persona→target exchange.
type SimulationTurn struct {
	TurnIndex     int
	PersonaText   string
	TargetText    string
	PersonaTokens int64
	TargetTokens  int64
}

// RunSimulation drives the scenario through to completion.
//
// Tenant id and auth flow through the same gateway-fronted chat
// completion path the eval runner uses; both calls share the same
// trace because we pass a stable correlation id in the headers.
func RunSimulation(ctx context.Context, tenantID string, scenario SimulationScenario) (*SimulationResult, error) {
	if scenario.TargetModel == "" {
		return nil, fmt.Errorf("scenario.TargetModel required")
	}
	if scenario.MaxTurns <= 0 {
		scenario.MaxTurns = 5
	}
	if scenario.PersonaModel == "" {
		scenario.PersonaModel = scenario.TargetModel
	}
	if len(scenario.StopOnMatch) == 0 {
		scenario.StopOnMatch = []string{"[END]", "<<END>>"}
	}
	if scenario.ID == "" {
		scenario.ID = fmt.Sprintf("sim-%d", time.Now().UnixNano())
	}

	personaSystem := scenario.Persona
	if personaSystem == "" {
		personaSystem = "You are a synthetic test user. Keep messages short and on-topic."
	}
	personaSystem += "\n\nReply only as the user. When the conversation has reached a natural end, output [END] alone on a line."

	result := &SimulationResult{
		ScenarioID:    scenario.ID,
		StoppedReason: "max_turns",
	}

	// Conversation history shared between both sides — each model sees
	// the other's last message as the latest user/assistant turn.
	personaHistory := []chatMsg{{Role: "system", Content: personaSystem}}
	targetHistory := []chatMsg{}

	// Optional kick-off bypasses the first persona call.
	var nextPersonaText string
	if scenario.InitialMessage != "" {
		nextPersonaText = scenario.InitialMessage
	}

	for i := 0; i < scenario.MaxTurns; i++ {
		// 1) Persona turn — generated unless we have a pre-seeded
		// initial message.
		personaText := nextPersonaText
		nextPersonaText = ""
		if personaText == "" {
			personaHistory = append(personaHistory, chatMsg{Role: "user", Content: lastAssistantOrSeed(targetHistory)})
			out, tok, err := chatCompletion(ctx, tenantID, scenario.PersonaModel, personaHistory, scenario.ID, scenario.Tags)
			if err != nil {
				result.Error = "persona model: " + err.Error()
				result.StoppedReason = "error"
				return result, err
			}
			personaText = out
			personaHistory = append(personaHistory, chatMsg{Role: "assistant", Content: personaText})
			result.Turns = append(result.Turns, SimulationTurn{TurnIndex: i, PersonaText: personaText, PersonaTokens: tok})
		} else {
			result.Turns = append(result.Turns, SimulationTurn{TurnIndex: i, PersonaText: personaText})
		}

		if matchesStop(personaText, scenario.StopOnMatch) {
			result.StoppedReason = "stop_match"
			break
		}

		// 2) Target turn.
		targetHistory = append(targetHistory, chatMsg{Role: "user", Content: personaText})
		out, tok, err := chatCompletion(ctx, tenantID, scenario.TargetModel, targetHistory, scenario.ID, scenario.Tags)
		if err != nil {
			result.Error = "target model: " + err.Error()
			result.StoppedReason = "error"
			return result, err
		}
		targetHistory = append(targetHistory, chatMsg{Role: "assistant", Content: out})
		result.Turns[len(result.Turns)-1].TargetText = out
		result.Turns[len(result.Turns)-1].TargetTokens = tok

		if matchesStop(out, scenario.StopOnMatch) {
			result.StoppedReason = "stop_match"
			break
		}
	}
	return result, nil
}

type chatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func lastAssistantOrSeed(history []chatMsg) string {
	// Persona expects the "user" role for the target's last message.
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "assistant" {
			return history[i].Content
		}
	}
	return "(start of conversation — initiate your role)"
}

func matchesStop(s string, needles []string) bool {
	low := strings.ToLower(s)
	for _, n := range needles {
		if strings.Contains(low, strings.ToLower(n)) {
			return true
		}
	}
	return false
}

// chatCompletion calls the gateway's /v1/chat/completions exactly like
// the eval runner does — same auth, same tenant header, same trace path.
// Returns the assistant text and total tokens.
func chatCompletion(ctx context.Context, tenantID, model string, messages []chatMsg, scenarioID string, tags map[string]string) (string, int64, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"model":    model,
		"messages": messages,
		"stream":   false,
		"metadata": withDefaultTags(tags, scenarioID),
	})
	url := evalRunnerGatewayURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := os.Getenv("MF_EVAL_RUNNER_API_KEY"); key != "" {
		req.Header.Set("x-evs-api-key", key)
		req.Header.Set("x-mf-api-key", key) // legacy alias (rolling-deploy safe)
	} else {
		internalauth.SetHeader(req.Header)
	}
	req.Header.Set("x-tenant-id", tenantID)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, fmt.Errorf("upstream %d: %s", resp.StatusCode, string(rb))
	}
	var m map[string]interface{}
	if err := json.Unmarshal(rb, &m); err != nil {
		return "", 0, err
	}
	out := extractChatOutput(m)
	tok := extractTotalTokens(m)
	// extractChatOutput returns the message map (or a fallback); pull the
	// content string out for the chat-history threading.
	if msg, ok := out.(map[string]interface{}); ok {
		if s, ok := msg["content"].(string); ok {
			return s, tok, nil
		}
	}
	return fmt.Sprintf("%v", out), tok, nil
}

func withDefaultTags(tags map[string]string, scenarioID string) map[string]string {
	out := map[string]string{
		"everstack.scenario_id": scenarioID,
		"everstack.source":      "simulation",
	}
	for k, v := range tags {
		out[k] = v
	}
	return out
}

func extractTotalTokens(resp map[string]interface{}) int64 {
	if u, ok := resp["usage"].(map[string]interface{}); ok {
		if v, ok := u["total_tokens"].(float64); ok {
			return int64(v)
		}
	}
	return 0
}
