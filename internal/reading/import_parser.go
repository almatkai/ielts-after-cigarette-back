package reading

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	passageLinePattern  = regexp.MustCompile(`(?i)^#{0,3}\s*PASSAGE\s+(\d+)\s*$`)
	questionLinePattern = regexp.MustCompile(`^(\d+)[\.)]\s*(.+)$`)
	answerLinePattern   = regexp.MustCompile(`^(\d+)\s*:\s*(.+)$`)
	optionLinePattern   = regexp.MustCompile(`^([A-Z])\s*[\.:]\s*(.+)$`)
	groupMarkerPattern  = regexp.MustCompile(`^\[([A-Z0-9_]+)\]$`)
	rangeLinePattern    = regexp.MustCompile(`(?i)^questions?\s+(\d+)\s*[-–]\s*(\d+)\s*$`)
)

type importQuestionRef struct {
	passage  int
	group    int
	question int
	line     int
}

type importAnswer struct {
	raw  string
	line int
}

// ParseImport is deterministic and side-effect free. It accepts the documented
// marker format plus a small set of well-known IELTS instruction phrases.
func ParseImport(input ImportParseInput) ImportResult {
	source := strings.ReplaceAll(strings.ReplaceAll(input.Source, "\r\n", "\n"), "\r", "\n")
	first := ""
	for _, line := range strings.Split(source, "\n") {
		if strings.TrimSpace(line) != "" {
			first = strings.TrimSpace(line)
			break
		}
	}
	if first == "# IELTS_READING_IMPORT_V1" {
		input.Source = source
		return parseImportV1(input)
	}
	if strings.HasPrefix(first, "# IELTS_READING_IMPORT_") {
		return ImportResult{
			FormatVersion: "unknown", Passages: []ImportPassage{}, Warnings: []ImportIssue{}, Info: []ImportIssue{},
			Errors: []ImportIssue{issue("UNSUPPORTED_FORMAT_VERSION", "Unsupported IELTS Reading import format header", 1, 0, 0)},
		}
	}
	input.Source = source
	return parseLegacyImport(input)
}

func parseLegacyImport(input ImportParseInput) ImportResult {
	result := ImportResult{FormatVersion: "legacy", Passages: []ImportPassage{}, Warnings: []ImportIssue{}, Errors: []ImportIssue{}, Info: []ImportIssue{}}
	input.Source = strings.ReplaceAll(strings.ReplaceAll(input.Source, "\r\n", "\n"), "\r", "\n")
	input.ExamType = strings.ToLower(strings.TrimSpace(input.ExamType))
	if input.ExamType == "" {
		input.ExamType = "academic"
	}
	input.Difficulty = strings.ToLower(strings.TrimSpace(input.Difficulty))
	if input.Difficulty == "" {
		input.Difficulty = "intermediate"
	}

	lines := strings.Split(input.Source, "\n")
	questions := map[int]importQuestionRef{}
	answers := map[int]importAnswer{}
	sharedOptions := map[string][]any{}
	groupReuse := map[string]bool{}
	section := ""
	currentPassage := -1
	currentGroup := -1
	currentQuestion := -1
	readingOptions := false

	ensurePassage := func(number int) {
		result.Passages = append(result.Passages, ImportPassage{Number: number, Material: SaveInput{
			ExamType: input.ExamType, Difficulty: input.Difficulty, QuestionGroups: []QuestionGroup{},
		}})
		currentPassage = len(result.Passages) - 1
		currentGroup, currentQuestion = -1, -1
	}

	for index, raw := range lines {
		lineNumber := index + 1
		line := strings.TrimSpace(raw)
		if line == "" {
			if section == "text" && currentPassage >= 0 {
				result.Passages[currentPassage].Material.Body += "\n"
			}
			continue
		}
		if match := passageLinePattern.FindStringSubmatch(line); match != nil {
			number, _ := strconv.Atoi(match[1])
			ensurePassage(number)
			section = "meta"
			readingOptions = false
			continue
		}
		upper := strings.ToUpper(strings.TrimSpace(strings.TrimLeft(line, "# ")))
		switch upper {
		case "TEXT":
			section = "text"
			continue
		case "QUESTIONS":
			section = "questions"
			continue
		case "ANSWERS":
			section = "answers"
			currentGroup, currentQuestion = -1, -1
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "title:") {
			title := strings.TrimSpace(line[len("title:"):])
			if currentPassage >= 0 {
				result.Passages[currentPassage].Material.Title = title
			} else {
				result.Title = title
			}
			continue
		}
		if currentPassage < 0 {
			if strings.HasPrefix(line, "#") {
				continue
			}
			ensurePassage(1)
		}
		passage := &result.Passages[currentPassage]
		if section == "text" {
			if passage.Material.Body != "" && !strings.HasSuffix(passage.Material.Body, "\n") {
				passage.Material.Body += "\n"
			}
			passage.Material.Body += raw
			continue
		}
		if section == "answers" {
			match := answerLinePattern.FindStringSubmatch(line)
			if match == nil {
				result.Warnings = append(result.Warnings, issue("ANSWER_LINE_IGNORED", "Answer line must use 'number: value'", lineNumber, passage.Number, 0))
				continue
			}
			number, _ := strconv.Atoi(match[1])
			if previous, exists := answers[number]; exists {
				result.Errors = append(result.Errors, issue("DUPLICATE_ANSWER", fmt.Sprintf("Duplicate answer; first declared on line %d", previous.line), lineNumber, passage.Number, number))
				continue
			}
			answers[number] = importAnswer{raw: strings.TrimSpace(match[2]), line: lineNumber}
			continue
		}

		if marker := groupMarkerPattern.FindStringSubmatch(upper); marker != nil {
			questionType, ok := importQuestionType(marker[1])
			if !ok {
				result.Errors = append(result.Errors, issue("UNKNOWN_QUESTION_TYPE", "Unknown question type: "+marker[1], lineNumber, passage.Number, 0))
				currentGroup = -1
				continue
			}
			passage.Material.QuestionGroups = append(passage.Material.QuestionGroups, QuestionGroup{Position: len(passage.Material.QuestionGroups) + 1, Type: questionType, Questions: []Question{}})
			currentGroup = len(passage.Material.QuestionGroups) - 1
			currentQuestion = -1
			section = "questions"
			readingOptions = false
			continue
		}
		if rangeLinePattern.MatchString(line) {
			section = "questions"
			continue
		}
		if strings.EqualFold(line, "options:") {
			readingOptions = true
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "reuse:") {
			if currentGroup >= 0 {
				groupReuse[groupKey(currentPassage, currentGroup)] = strings.EqualFold(strings.TrimSpace(strings.SplitN(line, ":", 2)[1]), "true")
			}
			continue
		}

		if currentGroup < 0 && section == "questions" {
			if detected := detectQuestionType(line); detected != "" {
				passage.Material.QuestionGroups = append(passage.Material.QuestionGroups, QuestionGroup{Position: len(passage.Material.QuestionGroups) + 1, Type: detected, Instructions: line, Questions: []Question{}})
				currentGroup = len(passage.Material.QuestionGroups) - 1
				continue
			}
		}
		if currentGroup >= 0 {
			group := &passage.Material.QuestionGroups[currentGroup]
			if option := optionLinePattern.FindStringSubmatch(line); option != nil {
				entry := map[string]any{"id": option[1], "text": option[2]}
				if group.Type == QuestionMultipleChoice && currentQuestion >= 0 && !readingOptions {
					appendQuestionOption(&group.Questions[currentQuestion], entry)
				} else {
					key := groupKey(currentPassage, currentGroup)
					sharedOptions[key] = append(sharedOptions[key], entry)
				}
				continue
			}
			if match := questionLinePattern.FindStringSubmatch(line); match != nil {
				number, _ := strconv.Atoi(match[1])
				if previous, exists := questions[number]; exists {
					result.Errors = append(result.Errors, issue("DUPLICATE_QUESTION", fmt.Sprintf("Duplicate question; first declared on line %d", previous.line), lineNumber, passage.Number, number))
					continue
				}
				content := map[string]any{"number": number}
				if options := sharedOptions[groupKey(currentPassage, currentGroup)]; len(options) > 0 {
					content["options"] = options
				}
				if groupReuse[groupKey(currentPassage, currentGroup)] {
					content["reuse"] = true
				}
				group.Questions = append(group.Questions, Question{Position: len(group.Questions) + 1, Prompt: match[2], Content: content, Answer: map[string]any{}, Points: 1})
				currentQuestion = len(group.Questions) - 1
				questions[number] = importQuestionRef{passage: currentPassage, group: currentGroup, question: currentQuestion, line: lineNumber}
				readingOptions = false
				continue
			}
			if strings.HasPrefix(strings.ToLower(line), "instruction:") {
				line = strings.TrimSpace(line[len("instruction:"):])
			}
			if group.Instructions != "" {
				group.Instructions += "\n"
			}
			group.Instructions += line
		}
	}

	enrichImportedCompletionQuestions(&result)
	applyImportAnswers(&result, questions, answers)
	for passageIndex := range result.Passages {
		passage := &result.Passages[passageIndex]
		passage.Material.Body = strings.TrimSpace(passage.Material.Body)
		if passage.Material.Title == "" {
			passage.Material.Title = fmt.Sprintf("%s — Passage %d", fallbackTitle(result.Title), passage.Number)
		}
		if passage.Material.Description == "" {
			passage.Material.Description = "Imported Reading passage"
		}
		if len([]rune(passage.Material.Body)) < 50 {
			result.Errors = append(result.Errors, issue("PASSAGE_TOO_SHORT", "Passage text must contain at least 50 characters", 0, passage.Number, 0))
		}
		if len(passage.Material.QuestionGroups) == 0 {
			result.Warnings = append(result.Warnings, issue("QUESTIONS_MISSING", "No question groups detected", 0, passage.Number, 0))
		}
	}
	if len(result.Passages) == 0 {
		result.Errors = append(result.Errors, issue("PASSAGE_MISSING", "No passage detected", 0, 0, 0))
	}
	return result
}

func enrichImportedCompletionQuestions(result *ImportResult) {
	for passageIndex := range result.Passages {
		passage := &result.Passages[passageIndex]
		for groupIndex := range passage.Material.QuestionGroups {
			group := &passage.Material.QuestionGroups[groupIndex]
			if !isCompletionType(group.Type) {
				continue
			}
			maxWords, allowNumber, detected := detectCompletionRule(group.Instructions)
			for questionIndex := range group.Questions {
				question := &group.Questions[questionIndex]
				if detected {
					question.Content["completionRule"] = map[string]any{"maxWords": maxWords, "allowNumber": allowNumber}
				}
				if !strings.Contains(question.Prompt, "{{answer}}") {
					number, _ := question.Content["number"].(int)
					result.Warnings = append(result.Warnings, issue("PLACEHOLDER_MISSING", "Completion question does not contain {{answer}}", 0, passage.Number, number))
				}
			}
			if !detected {
				result.Warnings = append(result.Warnings, issue("WORD_LIMIT_MISSING", "Completion instruction has no recognized word limit", 0, passage.Number, 0))
			}
		}
	}
}

func isCompletionType(questionType string) bool {
	switch questionType {
	case QuestionSentenceCompletion, QuestionSummaryCompletion, QuestionNoteCompletion,
		QuestionTableCompletion, QuestionFlowChartCompletion, QuestionDiagramLabelCompletion:
		return true
	default:
		return false
	}
}

func detectCompletionRule(instruction string) (maxWords int, allowNumber, detected bool) {
	upper := strings.ToUpper(instruction)
	allowNumber = strings.Contains(upper, "NUMBER")
	wordNumbers := map[string]int{"ONE": 1, "TWO": 2, "THREE": 3, "FOUR": 4, "FIVE": 5}
	for word, number := range wordNumbers {
		if strings.Contains(upper, word+" WORD") {
			return number, allowNumber, true
		}
	}
	return 0, allowNumber, false
}

func importQuestionType(marker string) (string, bool) {
	mapping := map[string]string{
		"MULTIPLE_CHOICE": QuestionMultipleChoice, "MULTIPLE_CHOICE_MULTIPLE_ANSWERS": QuestionMultipleChoice,
		"TRUE_FALSE_NOT_GIVEN": QuestionTrueFalseNotGiven, "YES_NO_NOT_GIVEN": QuestionYesNoNotGiven,
		"MATCHING_HEADINGS": QuestionMatchingHeadings, "MATCHING_INFORMATION": QuestionMatchingInformation,
		"MATCHING_FEATURES": QuestionMatchingFeatures, "MATCHING_PEOPLE": QuestionMatchingFeatures,
		"MATCHING_ENDINGS": QuestionMatchingSentenceEnds, "MATCHING_SENTENCE_ENDINGS": QuestionMatchingSentenceEnds,
		"SENTENCE_COMPLETION": QuestionSentenceCompletion, "SUMMARY_COMPLETION": QuestionSummaryCompletion,
		"NOTE_COMPLETION": QuestionNoteCompletion, "TABLE_COMPLETION": QuestionTableCompletion,
		"FLOW_CHART_COMPLETION": QuestionFlowChartCompletion, "DIAGRAM_LABEL_COMPLETION": QuestionDiagramLabelCompletion,
		"GAP_FILL": QuestionSentenceCompletion, "SHORT_ANSWER": QuestionShortAnswer,
	}
	value, ok := mapping[marker]
	return value, ok
}

func detectQuestionType(line string) string {
	value := strings.ToLower(line)
	switch {
	case strings.Contains(value, "claims of the writer"):
		return QuestionYesNoNotGiven
	case strings.Contains(value, "statements agree with the information"):
		return QuestionTrueFalseNotGiven
	case strings.Contains(value, "choose the correct letter"):
		return QuestionMultipleChoice
	case strings.Contains(value, "which section contains"):
		return QuestionMatchingInformation
	case strings.Contains(value, "correct heading"):
		return QuestionMatchingHeadings
	case strings.Contains(value, "correct person") || strings.Contains(value, "correct feature"):
		return QuestionMatchingFeatures
	case strings.Contains(value, "correct ending"):
		return QuestionMatchingSentenceEnds
	case strings.Contains(value, "complete the notes"):
		return QuestionNoteCompletion
	case strings.Contains(value, "complete the summary"):
		return QuestionSummaryCompletion
	case strings.Contains(value, "complete the table"):
		return QuestionTableCompletion
	case strings.Contains(value, "complete the flow chart"):
		return QuestionFlowChartCompletion
	case strings.Contains(value, "complete the sentences") || strings.Contains(value, "complete each sentence"):
		return QuestionSentenceCompletion
	case strings.Contains(value, "short answer"):
		return QuestionShortAnswer
	default:
		return ""
	}
}

func applyImportAnswers(result *ImportResult, questions map[int]importQuestionRef, answers map[int]importAnswer) {
	numbers := make([]int, 0, len(answers))
	for number := range answers {
		numbers = append(numbers, number)
	}
	sort.Ints(numbers)
	for _, number := range numbers {
		answer := answers[number]
		ref, ok := questions[number]
		if !ok {
			result.Errors = append(result.Errors, issue("UNKNOWN_QUESTION_NUMBER", "Answer references an unknown question", answer.line, 0, number))
			continue
		}
		group := &result.Passages[ref.passage].Material.QuestionGroups[ref.group]
		question := &group.Questions[ref.question]
		if message := setImportedAnswer(group.Type, question, answer.raw); message != "" {
			result.Errors = append(result.Errors, issue("INVALID_ANSWER", message, answer.line, result.Passages[ref.passage].Number, number))
		}
	}
	questionNumbers := make([]int, 0, len(questions))
	for number := range questions {
		questionNumbers = append(questionNumbers, number)
	}
	sort.Ints(questionNumbers)
	for _, number := range questionNumbers {
		if _, ok := answers[number]; !ok {
			ref := questions[number]
			result.Errors = append(result.Errors, issue("ANSWER_MISSING", "No answer provided", ref.line, result.Passages[ref.passage].Number, number))
		}
	}
}

func setImportedAnswer(questionType string, question *Question, raw string) string {
	raw = strings.TrimSpace(raw)
	if questionType == QuestionTrueFalseNotGiven || questionType == QuestionYesNoNotGiven {
		value := strings.ReplaceAll(strings.ToUpper(raw), " ", "_")
		allowed := []string{"TRUE", "FALSE", "NOT_GIVEN"}
		if questionType == QuestionYesNoNotGiven {
			allowed = []string{"YES", "NO", "NOT_GIVEN"}
		}
		if !oneOf(value, allowed...) {
			return "Answer must be one of " + strings.Join(allowed, ", ")
		}
		question.Answer = map[string]any{"value": value}
		return ""
	}
	if questionType == QuestionMultipleChoice {
		values := parseAnswerValues(raw)
		for index := range values {
			values[index] = strings.ToUpper(values[index])
		}
		if len(values) == 0 {
			return "Multiple choice answer is empty"
		}
		if len(values) == 1 {
			question.Answer = map[string]any{"optionId": values[0]}
		} else {
			question.Answer = map[string]any{"optionIds": stringsToAny(values)}
		}
		return validateOptionAnswers(question.Content["options"], values)
	}
	if strings.HasPrefix(questionType, "matching_") {
		values := parseAnswerValues(raw)
		for index := range values {
			values[index] = strings.ToUpper(values[index])
		}
		if len(values) != 1 {
			return "Matching answer must contain exactly one option"
		}
		question.Answer = map[string]any{"optionId": values[0]}
		return validateOptionAnswers(question.Content["options"], values)
	}
	values := parseAnswerValues(raw)
	if len(values) == 0 {
		return "Answer is empty"
	}
	question.Answer = map[string]any{"accepted": stringsToAny(values)}
	return ""
}

func parseAnswerValues(raw string) []string {
	var values []string
	if strings.HasPrefix(raw, "[") && json.Unmarshal([]byte(raw), &values) == nil {
		return values
	}
	for _, part := range strings.Split(raw, "|") {
		if value := strings.Trim(strings.TrimSpace(part), `"`); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func appendQuestionOption(question *Question, option map[string]any) {
	options, _ := question.Content["options"].([]any)
	question.Content["options"] = append(options, option)
}
func groupKey(passage, group int) string { return fmt.Sprintf("%d:%d", passage, group) }
func validateOptionAnswers(options any, answers []string) string {
	list, _ := options.([]any)
	allowed := map[string]bool{}
	for _, item := range list {
		if option, ok := item.(map[string]any); ok {
			allowed[fmt.Sprint(option["id"])] = true
		}
	}
	if len(allowed) == 0 {
		return "Question options are missing"
	}
	for _, answer := range answers {
		if !allowed[answer] {
			return "Answer " + answer + " does not exist in options"
		}
	}
	return ""
}
func stringsToAny(values []string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}
func fallbackTitle(title string) string {
	if strings.TrimSpace(title) != "" {
		return strings.TrimSpace(title)
	}
	return "Imported Reading"
}
func issue(code, message string, line, passage, question int) ImportIssue {
	return ImportIssue{Code: code, Message: message, Line: line, Passage: passage, QuestionNumber: question}
}
