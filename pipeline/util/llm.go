package util

import "regexp"

func TruncateSummaryForLLM(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}

func ExtractJSONObject(content string) string {
	start := -1
	end := -1
	braceCount := 0
	for i, c := range content {
		if c == '{' {
			if start == -1 {
				start = i
			}
			braceCount++
		} else if c == '}' {
			braceCount--
			if braceCount == 0 && start != -1 {
				end = i
				break
			}
		}
	}
	if start >= 0 && end > start {
		return content[start : end+1]
	}
	return ""
}

func ExtractJSONFromMarkdown(content string) string {
	re := regexp.MustCompile("(?s)```(?:json)?\\s*\\n(.*?)\\n```")
	matches := re.FindStringSubmatch(content)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}
