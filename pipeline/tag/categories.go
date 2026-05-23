package tag

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

func ParseCategoryChanges(suggestion string) (*CategoryChanges, error) {
	var changes CategoryChanges
	if err := json.Unmarshal([]byte(suggestion), &changes); err == nil {
		return &changes, nil
	}

	re := regexp.MustCompile("(?s)```(?:json)?\\s*\\n(.*?)\\n```")
	matches := re.FindStringSubmatch(suggestion)
	if len(matches) >= 2 {
		if err := json.Unmarshal([]byte(matches[1]), &changes); err == nil {
			return &changes, nil
		}
	}

	start := strings.Index(suggestion, "{")
	end := strings.LastIndex(suggestion, "}")
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(suggestion[start:end+1]), &changes); err == nil {
			return &changes, nil
		}
	}

	return nil, fmt.Errorf("failed to parse category changes from LLM response")
}
