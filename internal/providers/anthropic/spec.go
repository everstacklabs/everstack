package anthropic

// DefaultSpec returns common Anthropic API defaults.
func DefaultSpec() struct{ BaseURL, APIVersion, Docs string } {
	return struct{ BaseURL, APIVersion, Docs string }{
		BaseURL:    "https://api.anthropic.com",
		APIVersion: "2023-06-01",
		Docs:       "https://docs.anthropic.com/",
	}
}
