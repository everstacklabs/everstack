package metrics

import (
	"strings"
)

// BusinessMetrics contains classified business intelligence attributes
type BusinessMetrics struct {
	UseCase          string  // qa_simple, chat, summarization, code_generation, etc.
	Domain           string  // geography, science, code, business, etc.
	QueryType        string  // factual, creative, analytical
	QueryComplexity  string  // simple, moderate, complex
	DomainConfidence string  // high, medium, low
	QualityScore     float64 // 0.0-1.0
}

// ClassifyRequest analyzes a request and infers business metrics
func ClassifyRequest(messages []string, responseLength int, hasCode bool) BusinessMetrics {
	metrics := BusinessMetrics{
		QualityScore:     0.0,
		DomainConfidence: "low",
	}

	if len(messages) == 0 {
		return metrics
	}

	// Combine all messages for analysis
	combinedText := strings.ToLower(strings.Join(messages, " "))

	// Classify use case
	metrics.UseCase = classifyUseCase(combinedText, len(messages), hasCode)

	// Classify domain
	metrics.Domain, metrics.DomainConfidence = classifyDomain(combinedText)

	// Classify query type
	metrics.QueryType = classifyQueryType(combinedText, len(messages))

	// Classify complexity
	metrics.QueryComplexity = classifyComplexity(combinedText, len(messages), responseLength)

	// Calculate quality score (simple heuristic)
	metrics.QualityScore = calculateQualityScore(responseLength, metrics.QueryComplexity)

	return metrics
}

// classifyUseCase determines the use case from the request
func classifyUseCase(text string, messageCount int, hasCode bool) string {
	// Code generation patterns
	if hasCode || strings.Contains(text, "write code") || strings.Contains(text, "implement") ||
		strings.Contains(text, "function") || strings.Contains(text, "class") {
		return "code_generation"
	}

	// Summarization patterns
	if strings.Contains(text, "summarize") || strings.Contains(text, "summary") ||
		strings.Contains(text, "tldr") || strings.Contains(text, "key points") {
		return "summarization"
	}

	// Translation patterns
	if strings.Contains(text, "translate") || strings.Contains(text, "translation") {
		return "translation"
	}

	// Question answering patterns
	if strings.HasSuffix(text, "?") || strings.Contains(text, "what is") ||
		strings.Contains(text, "how to") || strings.Contains(text, "why") {
		if len(text) < 100 && messageCount == 1 {
			return "qa_simple"
		}
		return "qa_complex"
	}

	// Multi-turn conversation
	if messageCount > 3 {
		return "chat_conversation"
	}

	// Default to chat
	return "chat"
}

// classifyDomain determines the domain from the content
func classifyDomain(text string) (string, string) {
	// Geography keywords
	geographyKeywords := []string{"country", "city", "ocean", "mountain", "river", "continent", "earth", "planet", "radius", "distance", "location", "capital"}
	if containsAny(text, geographyKeywords) {
		return "geography", "high"
	}

	// Science keywords
	scienceKeywords := []string{"atom", "molecule", "physics", "chemistry", "biology", "experiment", "theory", "hypothesis", "scientific"}
	if containsAny(text, scienceKeywords) {
		return "science", "high"
	}

	// Code/programming keywords
	codeKeywords := []string{"code", "function", "variable", "class", "method", "algorithm", "programming", "software", "bug", "debug"}
	if containsAny(text, codeKeywords) {
		return "code", "high"
	}

	// Math keywords
	mathKeywords := []string{"calculate", "equation", "formula", "math", "number", "sum", "average", "percentage"}
	if containsAny(text, mathKeywords) {
		return "mathematics", "high"
	}

	// Business keywords
	businessKeywords := []string{"business", "market", "revenue", "profit", "customer", "sales", "strategy", "company"}
	if containsAny(text, businessKeywords) {
		return "business", "medium"
	}

	// History keywords
	historyKeywords := []string{"history", "historical", "century", "ancient", "war", "civilization"}
	if containsAny(text, historyKeywords) {
		return "history", "medium"
	}

	// Medical keywords
	medicalKeywords := []string{"medical", "health", "disease", "symptom", "treatment", "doctor", "patient"}
	if containsAny(text, medicalKeywords) {
		return "medical", "medium"
	}

	return "general", "low"
}

// classifyQueryType determines if the query is factual, creative, or analytical
func classifyQueryType(text string, messageCount int) string {
	// Creative patterns
	creativeKeywords := []string{"write a story", "create", "imagine", "creative", "poem", "song", "fiction"}
	if containsAny(text, creativeKeywords) {
		return "creative"
	}

	// Analytical patterns
	analyticalKeywords := []string{"analyze", "compare", "evaluate", "assess", "pros and cons", "advantages", "disadvantages"}
	if containsAny(text, analyticalKeywords) {
		return "analytical"
	}

	// Factual patterns (questions, definitions)
	if strings.HasSuffix(text, "?") || strings.Contains(text, "what is") ||
		strings.Contains(text, "define") || strings.Contains(text, "explain") {
		return "factual"
	}

	// Default based on message count
	if messageCount > 3 {
		return "conversational"
	}

	return "factual"
}

// classifyComplexity determines query complexity
func classifyComplexity(text string, messageCount int, responseLength int) string {
	// Simple: short query, short response
	if len(text) < 50 && responseLength < 200 && messageCount == 1 {
		return "simple"
	}

	// Complex: long query or multi-turn or long response
	if len(text) > 500 || messageCount > 5 || responseLength > 1000 {
		return "complex"
	}

	// Moderate: everything else
	return "moderate"
}

// calculateQualityScore calculates a simple quality score based on response characteristics
func calculateQualityScore(responseLength int, complexity string) float64 {
	baseScore := 0.7

	// Adjust for response length
	switch {
	case responseLength < 50:
		baseScore = 0.5 // Very short responses might be incomplete
	case responseLength > 100 && responseLength < 1000:
		baseScore = 0.9 // Good length
	case responseLength >= 1000:
		baseScore = 0.85 // Long but comprehensive
	}

	// Adjust for complexity match
	switch complexity {
	case "simple":
		baseScore += 0.1 // Simple queries should have quick answers
	case "complex":
		if responseLength > 500 {
			baseScore += 0.05 // Complex queries need detailed answers
		}
	}

	// Cap at 1.0
	if baseScore > 1.0 {
		baseScore = 1.0
	}

	return baseScore
}

// containsAny checks if text contains any of the keywords
func containsAny(text string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

// ExtractMessagesText extracts text content from messages for classification
// This is a helper that can be called from the gateway
func ExtractMessagesText(messages interface{}) []string {
	// This would need to be implemented based on your message structure
	// For now, return empty slice - implement based on actual message types
	return []string{}
}
