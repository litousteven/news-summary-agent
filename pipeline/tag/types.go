package tag

import "encoding/json"

type TagResultItem struct {
	ID            string          `json:"id"`
	DisplayTitle  string          `json:"display_title"`
	Category      string          `json:"category"`
	TopicTags     json.RawMessage `json:"topic_tags"`
	Region        string          `json:"region"`
	InterestScore json.RawMessage `json:"interest_score"`
	IsDuplicate   json.RawMessage `json:"is_duplicate"`
	Selected      json.RawMessage `json:"selected"`
	WhySelected   string          `json:"why_selected"`
}

type CategoryChanges struct {
	Add    []CategoryDef `json:"add"`
	Remove []string      `json:"remove"`
	Split  []SplitChange `json:"split"`
	Merge  []MergeChange `json:"merge"`
}

type CategoryDef struct {
	Name        string   `json:"name"`
	Keywords    []string `json:"keywords"`
	Boundary    string   `json:"boundary"`
	NotBoundary string   `json:"not_boundary"`
}

type SplitChange struct {
	From string        `json:"from"`
	Into []CategoryDef `json:"into"`
}

type MergeChange struct {
	From []string    `json:"from"`
	Into CategoryDef `json:"into"`
}
