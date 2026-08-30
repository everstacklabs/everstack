package firecracker

import (
	"strconv"
	"strings"
)

func splitLines(s string) []string { return strings.Split(s, "\n") }

// fieldValue parses "Label:   1234" style dumpe2fs output.
func fieldValue(line, label string) (int64, bool) {
	if !strings.HasPrefix(strings.TrimSpace(line), label) {
		return 0, false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), label))
	v, err := strconv.ParseInt(strings.Fields(rest)[0], 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
