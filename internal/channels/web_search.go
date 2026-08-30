package channels

import "strings"

type WebSearchIntent struct {
	Requested bool
	Trigger   string
	Text      string
}

var webSearchPrefixes = []string{
	"/web",
	"/search",
	"search:",
	"web:",
	"lookup:",
}

func DetectWebSearchIntent(input string) WebSearchIntent {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return WebSearchIntent{Text: input}
	}

	lower := strings.ToLower(trimmed)
	for _, prefix := range webSearchPrefixes {
		if lower == prefix || strings.HasPrefix(lower, prefix+" ") {
			cleaned := strings.TrimSpace(trimmed[len(prefix):])
			return WebSearchIntent{Requested: true, Trigger: prefix, Text: cleaned}
		}
	}

	if looksLikeWebSearchRequest(lower) {
		return WebSearchIntent{Requested: true, Trigger: "keyword", Text: trimmed}
	}

	return WebSearchIntent{Text: input}
}

func looksLikeWebSearchRequest(lower string) bool {
	if strings.Contains(lower, "web search") || strings.Contains(lower, "search the web") {
		return true
	}
	if strings.Contains(lower, "search online") || strings.Contains(lower, "find online") {
		return true
	}
	if strings.Contains(lower, "look up") || strings.Contains(lower, "lookup") {
		return true
	}
	if strings.Contains(lower, "google ") || strings.Contains(lower, "bing ") {
		return true
	}
	return false
}
