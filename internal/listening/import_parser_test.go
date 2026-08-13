package listening

import (
	"encoding/json"
	"strings"
	"testing"
)

const syntheticListening = `# IELTS_LISTENING_IMPORT_V1
title: Synthetic Listening
exam_type: ACADEMIC
duration_minutes: 40

## PART 1
title: Booking form
### GROUP 1
range: 1-2
type: FORM_COMPLETION
answer_limit: ONE_WORD_OR_A_NUMBER
instruction:
Complete the form.
1. Customer name: {{1}}
2. Number of guests: {{2}}

## PART 2
title: Local information
### GROUP 2
range: 3-3
type: MULTIPLE_CHOICE
instruction:
Choose the correct letter.
3. Why did the speaker call?
A: To make a booking
B: To cancel a class
C: To ask for directions

### GROUP 3
range: 4-5
type: MATCHING
reuse_options: true
instruction:
Match each statement.
options:
A: Alex
B: Blair
C: Casey
4. Recorded the first result.
5. Checked the second result.

### GROUP 4
range: 6-6
type: MAP_LABELLING
instruction:
Choose the correct map label.
options:
A: North entrance
B: South entrance
6. New traffic lights

## ANSWERS
1: Morgan
2: 4
3: a
4: B
5: B
6: A

## EXPLANATIONS
### 1
The caller gives the name Morgan.
### 3
The speaker says the purpose is a booking.`

func TestParseImportV1(t *testing.T) {
	t.Parallel()
	result := ParseImport(ImportParseInput{Source: syntheticListening})
	if len(result.Errors) > 0 {
		t.Fatalf("errors=%#v", result.Errors)
	}
	if len(result.Test.Parts) != 2 || len(result.Test.Parts[1].Groups) != 3 {
		t.Fatalf("test=%#v", result.Test)
	}
	first := result.Test.Parts[0].Groups[0].Questions[0]
	if first.Prompt != "Customer name: {{answer}}" || first.Answer["accepted"].([]any)[0] != "Morgan" || first.Explanation == "" {
		t.Fatalf("first=%#v", first)
	}
	multiple := result.Test.Parts[1].Groups[0].Questions[0]
	if multiple.Answer["optionId"] != "A" {
		t.Fatalf("multiple=%#v", multiple)
	}
	matching := result.Test.Parts[1].Groups[1]
	if matching.Config["reuseOptions"] != true || matching.Questions[0].Answer["optionId"] != "B" {
		t.Fatalf("matching=%#v", matching)
	}
}

func TestParseImportValidationAndCRLF(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, replace, want string }{
		{"missing answer", "6: A", "ANSWER_MISSING"},
		{"duplicate answer", "6: A", "DUPLICATE_ANSWER"},
		{"unknown answer", "6: A", "UNKNOWN_QUESTION_NUMBER"},
		{"invalid option", "6: A", "INVALID_ANSWER"},
		{"invalid placeholder", "1. Customer name: {{1}}", "INVALID_PLACEHOLDER"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := syntheticListening
			switch tt.name {
			case "missing answer":
				source = strings.Replace(source, "6: A\n\n## EXPLANATIONS", "## EXPLANATIONS", 1)
			case "duplicate answer":
				source = strings.Replace(source, "6: A\n\n## EXPLANATIONS", "6: A\n6: B\n\n## EXPLANATIONS", 1)
			case "unknown answer":
				source = strings.Replace(source, "6: A\n\n## EXPLANATIONS", "6: A\n99: A\n\n## EXPLANATIONS", 1)
			case "invalid option":
				source = strings.Replace(source, "6: A\n\n## EXPLANATIONS", "6: Z\n\n## EXPLANATIONS", 1)
			case "invalid placeholder":
				source = strings.Replace(source, "2. Number of guests: {{2}}", "2. Number of guests without blank", 1)
			}
			result := ParseImport(ImportParseInput{Source: source})
			if !hasIssue(result, tt.want) {
				t.Fatalf("issues=%#v %#v want=%s", result.Errors, result.Warnings, tt.want)
			}
		})
	}
	crlf := ParseImport(ImportParseInput{Source: strings.ReplaceAll(syntheticListening, "\n", "\r\n")})
	if len(crlf.Errors) > 0 {
		t.Fatalf("CRLF errors=%#v", crlf.Errors)
	}
}

func TestPublicDTODoesNotLeakAnswersOrExplanations(t *testing.T) {
	t.Parallel()
	admin := Test{Parts: []Part{{Groups: []QuestionGroup{{Questions: []Question{{Number: 1, Prompt: "Question", Content: map[string]any{"options": []any{"A"}}, Answer: map[string]any{"optionId": "A"}, Explanation: "secret", Points: 1}}}}}}}
	payload, err := json.Marshal(publicTest(admin))
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	if strings.Contains(text, "answer") || strings.Contains(text, "secret") || strings.Contains(text, "explanation") {
		t.Fatalf("student DTO leaks protected data: %s", text)
	}
}

func hasIssue(result ImportResult, code string) bool {
	for _, issue := range result.Errors {
		if issue.Code == code {
			return true
		}
	}
	return false
}
