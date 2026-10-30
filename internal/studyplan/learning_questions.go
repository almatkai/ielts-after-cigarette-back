package studyplan

import (
	"encoding/json"
	"fmt"
	"html"
	"sort"
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

var learningPlainPolicy = bluemonday.StrictPolicy()

func plainLearningText(raw string) string {
	raw = strings.NewReplacer("<br>", "\n", "<br/>", "\n", "<br />", "\n", "</p>", "\n", "</li>", "\n", "</div>", "\n").Replace(raw)
	return strings.TrimSpace(html.UnescapeString(learningPlainPolicy.Sanitize(raw)))
}

// Only explicitly recognized presentation fields are rendered. Answer, hint and
// hidden editor fields are never copied from arbitrary content into public tasks.
// Letter aliases bind visible options to original opaque IDs in the private key.
func readableQuestion(content json.RawMessage, key json.RawMessage) (string, json.RawMessage) {
	var data map[string]any
	_ = json.Unmarshal(content, &data)
	lines := []string{}
	for _, k := range []string{"text", "context", "passage", "sentence"} {
		if v, ok := data[k].(string); ok && v != "" {
			lines = append(lines, plainLearningText(v))
		}
	}
	aliases := map[string]string{}
	options, _ := data["options"].([]any)
	for i, raw := range options {
		if i >= 26 {
			break
		}
		label := string(rune('A' + i))
		id, text := "", ""
		switch o := raw.(type) {
		case map[string]any:
			id, _ = o["id"].(string)
			text, _ = o["text"].(string)
			if text == "" {
				text, _ = o["label"].(string)
			}
		case string:
			id = o
			text = o
		}
		if id == "" || text == "" {
			continue
		}
		aliases[label] = id
		lines = append(lines, label+") "+plainLearningText(text))
	}
	if len(aliases) > 0 {
		lines = append(lines, "Укажите букву варианта; для нескольких — буквы через запятую (например A, C).")
	}
	var correct map[string]any
	if json.Unmarshal(key, &correct) == nil && len(aliases) > 0 {
		correct["_learningAliases"] = aliases
		key, _ = json.Marshal(correct)
	}
	return strings.Join(lines, "\n"), key
}
func objectiveGiven(correct map[string]any, text string) map[string]any {
	var given map[string]any
	if json.Unmarshal([]byte(text), &given) == nil && given != nil {
		return given
	}
	aliases, _ := correct["_learningAliases"].(map[string]any)
	resolve := func(v string) string {
		v = strings.TrimSpace(v)
		if id, ok := aliases[strings.ToUpper(v)].(string); ok {
			return id
		}
		return v
	}
	given = map[string]any{"value": text, "optionId": resolve(text)}
	if _, ok := correct["optionIds"]; ok {
		parts := strings.Split(text, ",")
		options := []any{}
		for _, part := range parts {
			options = append(options, resolve(part))
		}
		given["optionIds"] = options
	}
	return given
}
func readableOriginalAnswer(raw, key json.RawMessage) string {
	var answer, correct map[string]any
	if json.Unmarshal(raw, &answer) != nil || json.Unmarshal(key, &correct) != nil {
		return answerText(raw)
	}
	aliases, _ := correct["_learningAliases"].(map[string]any)
	label := func(id string) string {
		keys := []string{}
		for k, v := range aliases {
			if v == id {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		if len(keys) > 0 {
			return keys[0]
		}
		return id
	}
	if id, ok := answer["optionId"].(string); ok {
		return label(id)
	}
	if values, ok := answer["optionIds"].([]any); ok {
		labels := []string{}
		for _, v := range values {
			labels = append(labels, label(fmt.Sprint(v)))
		}
		return strings.Join(labels, ", ")
	}
	return answerText(raw)
}
