package stringutil

// CutString is a helper function to limit string length
func CutString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
