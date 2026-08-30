package skills

import "encoding/json"

// ParseSkillsConfig extracts the skills array from the agent config JSONB.
// Returns nil if no skills are configured.
func ParseSkillsConfig(agentConfig map[string]interface{}) []SkillDefinition {
	raw, ok := agentConfig["skills"]
	if !ok {
		return nil
	}

	// JSON marshal/unmarshal roundtrip (same pattern as ParseMemoryConfig).
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}

	var skills []SkillDefinition
	if err := json.Unmarshal(data, &skills); err != nil {
		return nil
	}

	// Filter out any entries with empty content
	var valid []SkillDefinition
	for _, s := range skills {
		if s.Content != "" {
			valid = append(valid, s)
		}
	}

	return valid
}
