package util

import "strings"

// truncateForHistory truncates text for history storage.
func truncateForHistory(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 500 {
		return s[:500] + "..."
	}
	return s
}
