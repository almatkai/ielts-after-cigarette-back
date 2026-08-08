package reading

import (
	"strings"
	"testing"
)

const v1SyntheticImport = `# IELTS_READING_IMPORT_V1
title: Synthetic Reading Test
exam_type: ACADEMIC
duration_minutes: 60

## PASSAGE 1
title: Synthetic birds
### TEXT
This original synthetic passage discusses birds and contains enough characters to pass material validation without using examination content.

### GROUP 1
range: 1-2
type: TRUE_FALSE_NOT_GIVEN
instruction:
Do the statements agree with the information?
1. The passage is synthetic.
2. The passage is copied from an examination.

### GROUP 2
range: 3-4
type: NOTE_COMPLETION
answer_limit: ONE_WORD_AND_OR_NUMBER
instruction:
Complete the notes.
3. The passage discusses {{3}}.
4. It is sufficiently {{4}}.

## PASSAGE 2
title: Synthetic researchers
### TEXT
This second original passage describes three fictional researchers and is deliberately long enough for deterministic import validation.

### GROUP 3
range: 5-6
type: MATCHING_FEATURES
reuse_options: true
instruction:
Match each statement with a researcher.
options:
A: Alex
B: Blair
C: Casey
5. Found the first result.
6. Confirmed the second result.

### GROUP 4
range: 7-7
type: MULTIPLE_CHOICE
instruction:
Choose the correct letter.
7. Why was the passage written?
A: To provide a synthetic example
B: To report a real test
C: To advertise a course
D: To describe an audio task

## ANSWERS
1: true
2: Not given
3: birds
4: long
5: b
6: B
7: a

## EXPLANATIONS
### 1
The passage explicitly calls itself synthetic.

This second line verifies multiline explanations.
### 3
The passage says it discusses birds.`

func TestParseImportV1FullSynthetic(t *testing.T) {
	t.Parallel()
	result := ParseImport(ImportParseInput{Source: v1SyntheticImport, Difficulty: "advanced"})
	if len(result.Errors) != 0 {
		t.Fatalf("errors = %#v", result.Errors)
	}
	if result.FormatVersion != importFormatV1 || result.DurationMinutes != 60 || len(result.Passages) != 2 {
		t.Fatalf("metadata = %#v", result)
	}
	if result.Passages[0].Material.ExamType != "academic" || result.Passages[0].Material.Difficulty != "advanced" {
		t.Fatalf("material metadata = %#v", result.Passages[0].Material)
	}
	q1 := v1Question(t, result, 1)
	if q1.Answer["value"] != "TRUE" || !strings.Contains(q1.Explanation, "second line") {
		t.Fatalf("question 1 = %#v", q1)
	}
	q3 := v1Question(t, result, 3)
	if q3.Prompt != "The passage discusses {{answer}}." {
		t.Fatalf("completion prompt = %q", q3.Prompt)
	}
	rule := q3.Content["completionRule"].(map[string]any)
	if rule["maxWords"] != 1 || rule["allowNumber"] != true {
		t.Fatalf("completion rule = %#v", rule)
	}
	q5 := v1Question(t, result, 5)
	if q5.Answer["optionId"] != "B" || q5.Content["reuse"] != true {
		t.Fatalf("matching question = %#v", q5)
	}
	q7 := v1Question(t, result, 7)
	if q7.Answer["optionId"] != "A" {
		t.Fatalf("multiple choice = %#v", q7.Answer)
	}
}

func TestParseImportV1YNNGAndSentenceEndings(t *testing.T) {
	t.Parallel()
	source := v1SingleGroup("YES_NO_NOT_GIVEN", "1. The writer agrees.", "1: not given", "")
	result := ParseImport(ImportParseInput{Source: source})
	if len(result.Errors) != 0 || v1Question(t, result, 1).Answer["value"] != "NOT_GIVEN" {
		t.Fatalf("YNNG result = %#v", result)
	}

	source = v1SingleGroup("MATCHING_SENTENCE_ENDINGS", "options:\nA: ends first\nB: ends second\n1. This sentence", "1: B", "reuse_options: false")
	result = ParseImport(ImportParseInput{Source: source})
	if len(result.Errors) != 0 || v1Question(t, result, 1).Answer["optionId"] != "B" {
		t.Fatalf("sentence endings result = %#v", result)
	}
}

func TestParseImportV1StructuredAnswerLimitAndContentBlanks(t *testing.T) {
	t.Parallel()
	extra := "answer_limit:\n  max_words: 2\n  allow_number: true"
	source := v1SingleGroup("SUMMARY_COMPLETION", "content:\nSummary heading\n- first {{1}}", "1: two words", extra)
	result := ParseImport(ImportParseInput{Source: source})
	if len(result.Errors) != 0 {
		t.Fatalf("errors = %#v", result.Errors)
	}
	question := v1Question(t, result, 1)
	rule := question.Content["completionRule"].(map[string]any)
	if rule["maxWords"] != 2 || rule["allowNumber"] != true || !strings.Contains(question.Content["context"].(string), "Summary heading") {
		t.Fatalf("question = %#v", question)
	}
}

func TestParseImportV1ExplanationIsOptional(t *testing.T) {
	t.Parallel()
	result := ParseImport(ImportParseInput{Source: v1SingleGroup("TRUE_FALSE_NOT_GIVEN", "1. Statement.", "1: FALSE", "")})
	if len(result.Errors) != 0 || v1Question(t, result, 1).Explanation != "" {
		t.Fatalf("result = %#v", result)
	}
	for _, item := range result.Warnings {
		if strings.Contains(strings.ToLower(item.Message), "explanation") {
			t.Fatalf("missing explanation must not warn: %#v", result.Warnings)
		}
	}
}

func TestParseImportV1Validation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		questions  string
		answers    string
		extra      string
		want       string
		questionTy string
	}{
		{"missing answer", "1. Statement.", "", "", "ANSWER_MISSING", "TRUE_FALSE_NOT_GIVEN"},
		{"duplicate answer", "1. Statement.", "1: TRUE\n1: FALSE", "", "DUPLICATE_ANSWER", "TRUE_FALSE_NOT_GIVEN"},
		{"invalid enum", "1. Statement.", "1: YES", "", "INVALID_ANSWER", "TRUE_FALSE_NOT_GIVEN"},
		{"invalid matching option", "options:\nA: One\nB: Two\n1. Statement.", "1: D", "", "INVALID_ANSWER", "MATCHING_HEADINGS"},
		{"invalid answer count", "1. Choose one.\nA: One\nB: Two\nC: Three", "1: A | B", "", "INVALID_ANSWER_COUNT", "MULTIPLE_CHOICE"},
		{"unknown answer", "1. Statement.", "1: TRUE\n2: FALSE", "", "UNKNOWN_QUESTION_NUMBER", "TRUE_FALSE_NOT_GIVEN"},
		{"duplicate question", "1. First.\n1. Second.", "1: TRUE", "", "DUPLICATE_QUESTION", "TRUE_FALSE_NOT_GIVEN"},
		{"missing placeholder", "1. No numbered blank.", "1: word", "answer_limit: ONE_WORD_ONLY", "INVALID_PLACEHOLDER", "SENTENCE_COMPLETION"},
		{"duplicate placeholder", "content:\n- first {{1}}\n- second {{1}}", "1: word", "answer_limit: ONE_WORD_ONLY", "DUPLICATE_PLACEHOLDER", "SENTENCE_COMPLETION"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := ParseImport(ImportParseInput{Source: v1SingleGroup(test.questionTy, test.questions, test.answers, test.extra)})
			if !hasImportIssue(result, test.want) {
				t.Fatalf("result = %#v, want %s", result, test.want)
			}
		})
	}
}

func TestParseImportV1CRLFMalformedVersionAndLegacy(t *testing.T) {
	t.Parallel()
	crlf := strings.ReplaceAll(v1SingleGroup("TRUE_FALSE_NOT_GIVEN", "1. Statement.", "1: TRUE", ""), "\n", "\r\n")
	if result := ParseImport(ImportParseInput{Source: crlf}); len(result.Errors) != 0 || result.FormatVersion != importFormatV1 {
		t.Fatalf("CRLF result = %#v", result)
	}
	if result := ParseImport(ImportParseInput{Source: "# IELTS_READING_IMPORT_V2\ntitle: no"}); !hasImportIssue(result, "UNSUPPORTED_FORMAT_VERSION") {
		t.Fatalf("version result = %#v", result)
	}
	legacy := "## PASSAGE 1\ntitle: Legacy\n### TEXT\n" + syntheticPassage + "\n### QUESTIONS\n[TRUE_FALSE_NOT_GIVEN]\n1. Statement.\n## ANSWERS\n1: TRUE"
	if result := ParseImport(ImportParseInput{Source: legacy}); len(result.Errors) != 0 || result.FormatVersion != "legacy" {
		t.Fatalf("legacy result = %#v", result)
	}
}

func v1SingleGroup(questionType, questions, answers, extra string) string {
	source := "# IELTS_READING_IMPORT_V1\ntitle: Synthetic\nexam_type: GENERAL\n## PASSAGE 1\ntitle: Passage\n### TEXT\n" + syntheticPassage + "\n### GROUP 1\nrange: 1-1\ntype: " + questionType + "\n"
	if extra != "" {
		source += extra + "\n"
	}
	source += "instruction:\nSynthetic instruction.\n" + questions + "\n## ANSWERS"
	if answers != "" {
		source += "\n" + answers
	}
	return source
}

func v1Question(t *testing.T, result ImportResult, number int) Question {
	t.Helper()
	for _, passage := range result.Passages {
		for _, group := range passage.Material.QuestionGroups {
			for _, question := range group.Questions {
				if question.Content["number"] == number {
					return question
				}
			}
		}
	}
	t.Fatalf("question %d not found", number)
	return Question{}
}
