package compact

import (
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// strPtrTest is a test-local helper since strPtr is unexported and
// living in summarize.go (different file but same package). We just
// keep tests self-contained.
func strPtrTest(s string) *string { return &s }

func msgUser(text string) gw.Message {
	return gw.Message{
		Role:    gw.RoleUser,
		Content: []gw.ContentPart{{Type: "text", Text: strPtrTest(text)}},
	}
}

func msgAssistant(text string) gw.Message {
	return gw.Message{
		Role:    gw.RoleAssistant,
		Content: []gw.ContentPart{{Type: "text", Text: strPtrTest(text)}},
	}
}

func msgSystem(text string) gw.Message {
	return gw.Message{
		Role:    gw.RoleSystem,
		Content: []gw.ContentPart{{Type: "text", Text: strPtrTest(text)}},
	}
}

// makeChat builds a system + alternating user/assistant transcript of
// the requested length (in non-system messages).
func makeChat(nonSystem int) []gw.Message {
	out := []gw.Message{msgSystem("system prompt")}
	for i := 0; i < nonSystem; i++ {
		if i%2 == 0 {
			out = append(out, msgUser("user message body for index "+itoa(i)))
		} else {
			out = append(out, msgAssistant("assistant reply body for index "+itoa(i)))
		}
	}
	return out
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := ""
	for n := i; n > 0; n /= 10 {
		digits = string(rune('0'+n%10)) + digits
	}
	return digits
}

func TestDecide_DisabledIsNoop(t *testing.T) {
	c := New(Config{Enabled: false, MaxContextTokens: 1000})
	got := c.Decide(makeChat(50), 99999)
	if got.Action != ActionNone {
		t.Fatalf("disabled config should always return ActionNone, got %v", got.Action)
	}
}

func TestDecide_BelowAllThresholds(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	c := New(cfg)
	// 50% utilization — well below background (80%).
	got := c.Decide(makeChat(20), cfg.MaxContextTokens/2)
	if got.Action != ActionNone {
		t.Fatalf("below-threshold should return ActionNone, got %v", got)
	}
}

func TestDecide_BackgroundTier(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.MaxContextTokens = 1000
	c := New(cfg)
	// 82% utilization — just over background, below aggressive.
	got := c.Decide(makeChat(20), 820)
	if got.Action != ActionSummarize {
		t.Fatalf("expected ActionSummarize at background tier, got %v", got)
	}
	if got.Tier != TierBackground {
		t.Fatalf("expected TierBackground, got %v", got.Tier)
	}
	// 30% of 20 non-system = 6 messages, starting at index 1 (system at 0).
	if got.ReplaceStart != 1 || got.ReplaceEnd != 7 {
		t.Fatalf("background fraction wrong: ReplaceStart=%d ReplaceEnd=%d (want 1,7)", got.ReplaceStart, got.ReplaceEnd)
	}
}

func TestDecide_AggressiveTier(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.MaxContextTokens = 1000
	c := New(cfg)
	// 88% utilization — over aggressive, below emergency.
	got := c.Decide(makeChat(20), 880)
	if got.Action != ActionSummarize {
		t.Fatalf("expected ActionSummarize at aggressive tier, got %v", got)
	}
	if got.Tier != TierAggressive {
		t.Fatalf("expected TierAggressive, got %v", got.Tier)
	}
	// 60% of 20 non-system = 12 messages.
	if got.ReplaceStart != 1 || got.ReplaceEnd != 13 {
		t.Fatalf("aggressive fraction wrong: ReplaceStart=%d ReplaceEnd=%d (want 1,13)", got.ReplaceStart, got.ReplaceEnd)
	}
}

func TestDecide_EmergencyTier_Truncates(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.MaxContextTokens = 1000
	c := New(cfg)
	// 99% utilization — emergency tier.
	got := c.Decide(makeChat(50), 990)
	if got.Action != ActionTruncate {
		t.Fatalf("expected ActionTruncate at emergency tier, got %v", got)
	}
	if got.Tier != TierEmergency {
		t.Fatalf("expected TierEmergency, got %v", got.Tier)
	}
	// Total messages = 51 (1 system + 50 non-system). Keep last 20 +
	// system, so keepStart = 51 - 20 = 31. Replace [1, 31).
	if got.ReplaceStart != 1 || got.ReplaceEnd != 31 {
		t.Fatalf("emergency keepStart math wrong: ReplaceStart=%d ReplaceEnd=%d (want 1,31)", got.ReplaceStart, got.ReplaceEnd)
	}
}

func TestDecide_EmergencyTier_TooShortIsNoop(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.MaxContextTokens = 1000
	c := New(cfg)
	// Emergency utilization but only 10 messages — already short
	// enough that truncating would be a no-op.
	got := c.Decide(makeChat(10), 990)
	if got.Action != ActionNone {
		t.Fatalf("emergency on short transcript should be no-op, got %v", got)
	}
}

func TestDecide_BackgroundTooFewMessagesIsNoop(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.MaxContextTokens = 1000
	c := New(cfg)
	// 82% utilization with only 4 non-system messages — 30% of 4 = 1
	// (rounded), below the 2-message minimum.
	got := c.Decide(makeChat(4), 820)
	if got.Action != ActionNone {
		t.Fatalf("background on too-short transcript should be no-op, got %v", got)
	}
}

func TestDecide_NoSystemMessage(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.MaxContextTokens = 1000
	c := New(cfg)
	// Conversation with no system prompt — startIdx must be 0.
	msgs := []gw.Message{}
	for i := 0; i < 20; i++ {
		msgs = append(msgs, msgUser("hello "+itoa(i)))
	}
	got := c.Decide(msgs, 820)
	if got.ReplaceStart != 0 {
		t.Fatalf("no-system-message: expected ReplaceStart=0, got %d", got.ReplaceStart)
	}
}

func TestEstimate_NonZero(t *testing.T) {
	got := Estimate(makeChat(5))
	if got <= 0 {
		t.Fatalf("Estimate should return positive token count for non-empty transcript, got %d", got)
	}
}

func TestEstimate_Empty(t *testing.T) {
	if got := Estimate(nil); got != 0 {
		t.Fatalf("Estimate(nil) = %d, want 0", got)
	}
}

func TestConfig_Validate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{"defaults are valid", func(c *Config) {}, false},
		{"max context zero", func(c *Config) { c.MaxContextTokens = 0 }, true},
		{"background out of range", func(c *Config) { c.BackgroundThreshold = 1.2 }, true},
		{"aggressive below background", func(c *Config) { c.AggressiveThreshold = 0.5 }, true},
		{"emergency below aggressive", func(c *Config) { c.EmergencyThreshold = 0.7 }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tc.mutate(&cfg)
			err := cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate(): err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestConfig_IsProviderAllowed(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.IsProviderAllowed("anthropic") {
		t.Fatalf("empty allowlist should allow every provider")
	}
	cfg.EnabledForProviders = []string{"anthropic", "openai"}
	if !cfg.IsProviderAllowed("anthropic") {
		t.Fatalf("anthropic should be allowed")
	}
	if cfg.IsProviderAllowed("google") {
		t.Fatalf("google should be denied when not in allowlist")
	}
}
