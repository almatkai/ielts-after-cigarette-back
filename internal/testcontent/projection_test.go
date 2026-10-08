package testcontent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPublicContentRedactsNestedAnswerKeysWithoutMutation(t *testing.T) {
	content := map[string]any{
		"options":   []any{map[string]any{"id": "A", "text": "Keep me", "isCorrect": true}},
		"nested":    []any{map[string]any{"answer": "secret-answer", "explanation": "secret-explanation", "quote": "secret-evidence", "hint": "secret-hint", "visible": "Keep nested"}},
		"wordLimit": 2,
	}
	before, _ := json.Marshal(content)
	public := PublicContent(content)
	encoded, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-", "isCorrect", "answer", "explanation", "quote", "hint"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("public content leaked %q: %s", secret, encoded)
		}
	}
	if !strings.Contains(string(encoded), "Keep me") || !strings.Contains(string(encoded), "Keep nested") {
		t.Fatalf("public content lost options/context: %s", encoded)
	}
	public["nested"].([]any)[0].(map[string]any)["visible"] = "changed"
	after, _ := json.Marshal(content)
	if string(after) != string(before) {
		t.Fatal("public projection mutated the saved version")
	}
	if PublicContent(nil) != nil {
		t.Fatal("nil content must remain nil")
	}
}

func TestAnswerKeyPreservesAuthorDataAsText(t *testing.T) {
	key := NewAnswerKey(map[string]any{"optionIds": []string{"A", "C"}}, "It&#8217;s explained &lt;script&gt;", map[string]any{"quote": "It&#8217;s here.", "hint": "First paragraph"})
	if key.Quote != "It’s here." || key.Explanation != "It’s explained <script>" || key.Hint != "First paragraph" {
		t.Fatalf("unexpected key: %#v", key)
	}
	if len(key.Answer["optionIds"].([]string)) != 2 {
		t.Fatal("lost multiple-choice answer")
	}
}
