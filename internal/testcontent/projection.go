// Package testcontent separates student-facing question content from answer keys.
package testcontent

import (
	"html"
	"strings"
)

// AnswerKey is returned only by administrator preview endpoints, never by
// public material or in-progress attempt endpoints.
type AnswerKey struct {
	Answer      map[string]any `json:"answer"`
	Explanation string         `json:"explanation"`
	Quote       string         `json:"quote"`
	Hint        string         `json:"hint"`
}

func NewAnswerKey(answer map[string]any, explanation string, content map[string]any) AnswerKey {
	quote, _ := content["quote"].(string)
	hint, _ := content["hint"].(string)
	return AnswerKey{Answer: answer, Explanation: html.UnescapeString(explanation), Quote: html.UnescapeString(quote), Hint: html.UnescapeString(hint)}
}

// PublicContent recursively copies JSON objects/arrays, excluding answer-key
// fields and evidence. It must not mutate the version used for grading/preview.
func PublicContent(content map[string]any) map[string]any {
	if content == nil {
		return nil
	}
	return publicValue(content).(map[string]any)
}

func publicValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, item := range value {
			switch strings.ToLower(key) {
			case "answer", "answers", "correctanswer", "correctanswers", "explanation", "explanations", "quote", "hint", "iscorrect":
				continue
			}
			result[key] = publicValue(item)
		}
		return result
	case []any:
		result := make([]any, len(value))
		for index, item := range value {
			result[index] = publicValue(item)
		}
		return result
	default:
		return value
	}
}
