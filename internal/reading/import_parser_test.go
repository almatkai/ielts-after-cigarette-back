package reading

import (
	"strings"
	"testing"
)

const syntheticPassage = "This is a synthetic reading passage written for automated tests. It contains enough original text to satisfy validation without copying examination material."

func TestParseImportSupportedGroups(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		marker      string
		instruction string
		question    string
		answer      string
		wantType    string
	}{
		{"true false not given", "TRUE_FALSE_NOT_GIVEN", "", "A statement about the passage.", "FALSE", QuestionTrueFalseNotGiven},
		{"yes no not given", "YES_NO_NOT_GIVEN", "", "The writer agrees with an idea.", "YES", QuestionYesNoNotGiven},
		{"sentence completion", "SENTENCE_COMPLETION", "Choose ONE WORD ONLY.", "The result was {{answer}}.", "stable", QuestionSentenceCompletion},
		{"summary completion", "SUMMARY_COMPLETION", "Complete the summary.", "The summary contains {{answer}}.", "evidence", QuestionSummaryCompletion},
		{"note completion", "NOTE_COMPLETION", "Complete the notes.", "A note contains {{answer}}.", "detail", QuestionNoteCompletion},
		{"table completion", "TABLE_COMPLETION", "Complete the table.", "A cell contains {{answer}}.", "value", QuestionTableCompletion},
		{"flow chart completion", "FLOW_CHART_COMPLETION", "Complete the flow chart.", "First {{answer}} then continue.", "start", QuestionFlowChartCompletion},
		{"diagram label completion", "DIAGRAM_LABEL_COMPLETION", "Label the diagram.", "Part {{answer}}.", "axis", QuestionDiagramLabelCompletion},
		{"gap fill alias", "GAP_FILL", "Choose ONE WORD AND/OR A NUMBER.", "A gap {{answer}}.", "one", QuestionSentenceCompletion},
		{"short answer", "SHORT_ANSWER", "Answer briefly.", "What was found?", "evidence", QuestionShortAnswer},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := "## PASSAGE 1\ntitle: Synthetic\n### TEXT\n" + syntheticPassage + "\n### QUESTIONS\n[" + test.marker + "]\ninstruction:\n" + test.instruction + "\n1. " + test.question + "\n## ANSWERS\n1: " + test.answer
			result := ParseImport(ImportParseInput{Source: source})
			if len(result.Errors) != 0 {
				t.Fatalf("errors = %#v", result.Errors)
			}
			if got := result.Passages[0].Material.QuestionGroups[0].Type; got != test.wantType {
				t.Fatalf("type = %q, want %q", got, test.wantType)
			}
		})
	}
}

func TestParseImportMultipleChoiceAndMatching(t *testing.T) {
	t.Parallel()
	source := `# READING TEST
title: Synthetic Test
## PASSAGE 1
title: First
### TEXT
` + syntheticPassage + `
### QUESTIONS
[MULTIPLE_CHOICE]
1. Which option is correct?
A. First option
B. Second option
C. Third option
D. Fourth option
[MATCHING_PEOPLE]
options:
A: Alex
B: Blair
C: Casey
reuse: true
2. Identified the first pattern.
3. Confirmed the second pattern.
## ANSWERS
1: C
2: B
3: B
## PASSAGE 2
title: Second
### TEXT
` + syntheticPassage + ` Additional passage content.
### QUESTIONS
[TRUE_FALSE_NOT_GIVEN]
4. This is a synthetic statement.
## ANSWERS
4: TRUE`
	result := ParseImport(ImportParseInput{Source: source})
	if len(result.Errors) != 0 {
		t.Fatalf("errors = %#v", result.Errors)
	}
	if len(result.Passages) != 2 {
		t.Fatalf("passages = %d", len(result.Passages))
	}
	groups := result.Passages[0].Material.QuestionGroups
	if len(groups) != 2 || groups[1].Type != QuestionMatchingFeatures {
		t.Fatalf("groups = %#v", groups)
	}
	if groups[0].Questions[0].Answer["optionId"] != "C" {
		t.Fatalf("mc answer = %#v", groups[0].Questions[0].Answer)
	}
	if groups[1].Questions[1].Content["reuse"] != true {
		t.Fatalf("matching reuse missing: %#v", groups[1].Questions[1].Content)
	}
}

func TestParseImportCompletionMetadata(t *testing.T) {
	t.Parallel()
	source := "## PASSAGE 1\n### TEXT\n" + syntheticPassage + "\n### QUESTIONS\n[SENTENCE_COMPLETION]\ninstruction: Choose NO MORE THAN TWO WORDS AND/OR A NUMBER.\n1. The result was {{answer}}.\n2. Placeholder is absent.\n## ANSWERS\n1: stable result\n2: evidence"
	result := ParseImport(ImportParseInput{Source: source})
	if len(result.Errors) != 0 {
		t.Fatalf("errors = %#v", result.Errors)
	}
	rule, ok := result.Passages[0].Material.QuestionGroups[0].Questions[0].Content["completionRule"].(map[string]any)
	if !ok || rule["maxWords"] != 2 || rule["allowNumber"] != true {
		t.Fatalf("completion rule = %#v", rule)
	}
	if !hasImportIssue(result, "PLACEHOLDER_MISSING") {
		t.Fatalf("result = %#v, want placeholder warning", result)
	}
}

func TestParseImportValidationIssues(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, questions, answers, want string }{
		{"missing answer", "[TRUE_FALSE_NOT_GIVEN]\n1. Statement.", "", "ANSWER_MISSING"},
		{"duplicate answer", "[TRUE_FALSE_NOT_GIVEN]\n1. Statement.", "1: TRUE\n1: FALSE", "DUPLICATE_ANSWER"},
		{"unknown answer number", "[TRUE_FALSE_NOT_GIVEN]\n1. Statement.", "2: TRUE", "UNKNOWN_QUESTION_NUMBER"},
		{"duplicate question", "[TRUE_FALSE_NOT_GIVEN]\n1. First.\n1. Second.", "1: TRUE", "DUPLICATE_QUESTION"},
		{"invalid enum", "[TRUE_FALSE_NOT_GIVEN]\n1. Statement.", "1: MAYBE", "INVALID_ANSWER"},
		{"invalid matching option", "[MATCHING_HEADINGS]\noptions:\nA: One\nB: Two\n1. Heading.", "1: D", "INVALID_ANSWER"},
		{"malformed group", "[NOT_A_REAL_TYPE]\n1. Statement.", "1: A", "UNKNOWN_QUESTION_TYPE"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := "## PASSAGE 1\n### TEXT\n" + syntheticPassage + "\n### QUESTIONS\n" + test.questions
			if test.answers != "" {
				source += "\n## ANSWERS\n" + test.answers
			}
			result := ParseImport(ImportParseInput{Source: source})
			if !hasImportIssue(result, test.want) {
				t.Fatalf("result = %#v, want issue %s", result, test.want)
			}
		})
	}
}

func TestParseImportMissingAnswerIsFatal(t *testing.T) {
	t.Parallel()
	source := "## PASSAGE 1\n### TEXT\n" + syntheticPassage + "\n### QUESTIONS\n[TRUE_FALSE_NOT_GIVEN]\n1. Statement."
	result := ParseImport(ImportParseInput{Source: source})
	for _, item := range result.Errors {
		if item.Code == "ANSWER_MISSING" {
			return
		}
	}
	t.Fatalf("errors = %#v, want ANSWER_MISSING", result.Errors)
}

func TestParseImportNaturalInstructionsCRLFAndMarkdown(t *testing.T) {
	t.Parallel()
	source := "# READING TEST\r\n## PASSAGE 1\r\n### TEXT\r\n" + syntheticPassage + "\r\n### QUESTIONS\r\nQuestions 1-1\r\nDo the following statements agree with the information given in the reading passage?\r\n1. A statement.\r\n## ANSWERS\r\n1: NOT GIVEN"
	result := ParseImport(ImportParseInput{Source: source})
	if len(result.Errors) != 0 {
		t.Fatalf("errors = %#v", result.Errors)
	}
	if result.Passages[0].Material.QuestionGroups[0].Type != QuestionTrueFalseNotGiven {
		t.Fatalf("auto detection failed: %#v", result)
	}
}

func TestParseImportNeverPanicsOnPartialInput(t *testing.T) {
	t.Parallel()
	inputs := []string{"", "[MULTIPLE_CHOICE]", "## PASSAGE 1\n### QUESTIONS\n1.", "## ANSWERS\n1: A", strings.Repeat("\n", 20)}
	for _, source := range inputs {
		_ = ParseImport(ImportParseInput{Source: source})
	}
}

func hasImportIssue(result ImportResult, code string) bool {
	for _, issue := range append(result.Errors, result.Warnings...) {
		if issue.Code == code {
			return true
		}
	}
	return false
}
