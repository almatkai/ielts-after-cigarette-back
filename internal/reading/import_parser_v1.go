package reading

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const importFormatV1 = "IELTS_READING_IMPORT_V1"

var (
	v1PassagePattern     = regexp.MustCompile(`(?i)^##\s+PASSAGE\s+(\d+)\s*$`)
	v1GroupPattern       = regexp.MustCompile(`(?i)^###\s+GROUP\s+(\d+)\s*$`)
	v1ExplanationPattern = regexp.MustCompile(`^###\s+(\d+)\s*$`)
	v1RangePattern       = regexp.MustCompile(`^(\d+)\s*[-\x{2013}]\s*(\d+)$`)
	v1PlaceholderPattern = regexp.MustCompile(`\{\{(\d+)\}\}`)
)

type v1QuestionBuilder struct {
	number  int
	prompt  string
	line    int
	options []any
}

type v1GroupBuilder struct {
	passage          int
	number           int
	line             int
	rangeStart       int
	rangeEnd         int
	typeID           string
	instructions     []string
	answerLimit      string
	answerLimitWords int
	answerLimitNum   *bool
	reuseOptions     bool
	sharedOptions    []any
	questions        []v1QuestionBuilder
	contentLines     []string
	contentLineStart int
}

type v1Explanation struct {
	lines []string
	line  int
}

func parseImportV1(input ImportParseInput) ImportResult {
	result := ImportResult{
		FormatVersion: importFormatV1,
		Passages:      []ImportPassage{},
		Warnings:      []ImportIssue{},
		Errors:        []ImportIssue{},
		Info:          []ImportIssue{},
	}
	input.Difficulty = strings.ToLower(strings.TrimSpace(input.Difficulty))
	if input.Difficulty == "" {
		input.Difficulty = "intermediate"
	}
	input.ExamType = strings.ToLower(strings.TrimSpace(input.ExamType))
	if input.ExamType == "" {
		input.ExamType = "academic"
	}

	lines := strings.Split(input.Source, "\n")
	groups := []v1GroupBuilder{}
	answers := map[int]importAnswer{}
	explanations := map[int]v1Explanation{}
	currentPassage := -1
	currentGroup := -1
	currentQuestion := -1
	currentExplanation := 0
	mode := "meta"
	sharedOptionsMode := false

	for index, raw := range lines {
		lineNumber := index + 1
		line := strings.TrimSpace(raw)
		if line == "# "+importFormatV1 {
			continue
		}
		if line == "" {
			switch mode {
			case "text":
				if currentPassage >= 0 {
					result.Passages[currentPassage].Material.Body += "\n"
				}
			case "instruction":
				if currentGroup >= 0 && len(groups[currentGroup].instructions) > 0 {
					groups[currentGroup].instructions = append(groups[currentGroup].instructions, "")
				}
			case "content":
				if currentGroup >= 0 && len(groups[currentGroup].contentLines) > 0 {
					groups[currentGroup].contentLines = append(groups[currentGroup].contentLines, "")
				}
			case "explanation":
				entry := explanations[currentExplanation]
				if len(entry.lines) > 0 {
					entry.lines = append(entry.lines, "")
					explanations[currentExplanation] = entry
				}
			}
			continue
		}

		if match := v1PassagePattern.FindStringSubmatch(line); match != nil {
			number, _ := strconv.Atoi(match[1])
			result.Passages = append(result.Passages, ImportPassage{Number: number, Material: SaveInput{
				ExamType: input.ExamType, Difficulty: input.Difficulty, QuestionGroups: []QuestionGroup{},
			}})
			currentPassage = len(result.Passages) - 1
			currentGroup, currentQuestion, currentExplanation = -1, -1, 0
			mode = "passage_meta"
			continue
		}
		if match := v1GroupPattern.FindStringSubmatch(line); match != nil {
			if currentPassage < 0 {
				result.Errors = append(result.Errors, issue("GROUP_WITHOUT_PASSAGE", "Question group must belong to a passage", lineNumber, 0, 0))
				continue
			}
			number, _ := strconv.Atoi(match[1])
			groups = append(groups, v1GroupBuilder{passage: currentPassage, number: number, line: lineNumber})
			currentGroup = len(groups) - 1
			currentQuestion, currentExplanation = -1, 0
			mode = "group"
			sharedOptionsMode = false
			continue
		}
		upper := strings.ToUpper(strings.TrimSpace(line))
		switch upper {
		case "### TEXT":
			if currentPassage < 0 {
				result.Errors = append(result.Errors, issue("TEXT_WITHOUT_PASSAGE", "TEXT section must belong to a passage", lineNumber, 0, 0))
			} else {
				mode = "text"
			}
			continue
		case "## ANSWERS":
			mode = "answers"
			currentGroup, currentQuestion, currentExplanation = -1, -1, 0
			continue
		case "## EXPLANATIONS":
			mode = "explanations"
			currentGroup, currentQuestion, currentExplanation = -1, -1, 0
			continue
		}

		if mode == "explanations" || mode == "explanation" {
			if match := v1ExplanationPattern.FindStringSubmatch(line); match != nil {
				number, _ := strconv.Atoi(match[1])
				if previous, exists := explanations[number]; exists {
					result.Errors = append(result.Errors, issue("DUPLICATE_EXPLANATION", fmt.Sprintf("Duplicate explanation; first declared on line %d", previous.line), lineNumber, 0, number))
				} else {
					explanations[number] = v1Explanation{line: lineNumber}
				}
				currentExplanation = number
				mode = "explanation"
				continue
			}
			if currentExplanation == 0 {
				result.Errors = append(result.Errors, issue("EXPLANATION_HEADING_MISSING", "Explanation text must follow a '### question-number' heading", lineNumber, 0, 0))
				continue
			}
			entry := explanations[currentExplanation]
			entry.lines = append(entry.lines, raw)
			explanations[currentExplanation] = entry
			continue
		}

		if mode == "answers" {
			match := answerLinePattern.FindStringSubmatch(line)
			if match == nil {
				result.Errors = append(result.Errors, issue("INVALID_ANSWER_LINE", "Answer line must use 'number: value'", lineNumber, 0, 0))
				continue
			}
			number, _ := strconv.Atoi(match[1])
			if previous, exists := answers[number]; exists {
				result.Errors = append(result.Errors, issue("DUPLICATE_ANSWER", fmt.Sprintf("Duplicate answer; first declared on line %d", previous.line), lineNumber, 0, number))
				continue
			}
			answers[number] = importAnswer{raw: strings.TrimSpace(match[2]), line: lineNumber}
			continue
		}

		if mode == "text" {
			passage := &result.Passages[currentPassage]
			if passage.Material.Body != "" && !strings.HasSuffix(passage.Material.Body, "\n") {
				passage.Material.Body += "\n"
			}
			passage.Material.Body += raw
			continue
		}

		if currentGroup >= 0 {
			group := &groups[currentGroup]
			if value, ok := v1Field(line, "range"); ok {
				match := v1RangePattern.FindStringSubmatch(value)
				if match == nil {
					result.Errors = append(result.Errors, issue("INVALID_GROUP_RANGE", "range must use start-end", lineNumber, result.Passages[group.passage].Number, 0))
				} else {
					group.rangeStart, _ = strconv.Atoi(match[1])
					group.rangeEnd, _ = strconv.Atoi(match[2])
				}
				mode = "group"
				continue
			}
			if value, ok := v1Field(line, "type"); ok {
				group.typeID = strings.ToLower(strings.TrimSpace(value))
				if !containsQuestionType(group.typeID) {
					result.Errors = append(result.Errors, issue("UNKNOWN_QUESTION_TYPE", "Unknown canonical question type: "+value, lineNumber, result.Passages[group.passage].Number, 0))
				}
				mode = "group"
				continue
			}
			if value, ok := v1Field(line, "reuse_options"); ok {
				switch strings.ToLower(value) {
				case "true":
					group.reuseOptions = true
				case "false":
					group.reuseOptions = false
				default:
					result.Errors = append(result.Errors, issue("INVALID_REUSE_OPTIONS", "reuse_options must be true or false", lineNumber, result.Passages[group.passage].Number, 0))
				}
				continue
			}
			if value, ok := v1Field(line, "answer_limit"); ok {
				group.answerLimit = strings.ToUpper(strings.TrimSpace(value))
				if group.answerLimit == "" {
					mode = "answer_limit"
				} else {
					mode = "group"
				}
				continue
			}
			if mode == "answer_limit" {
				if value, ok := v1Field(line, "max_words"); ok {
					words, err := strconv.Atoi(value)
					if err != nil || words < 1 || words > 10 {
						result.Errors = append(result.Errors, issue("INVALID_ANSWER_LIMIT", "max_words must be between 1 and 10", lineNumber, result.Passages[group.passage].Number, 0))
					} else {
						group.answerLimitWords = words
					}
					continue
				}
				if value, ok := v1Field(line, "allow_number"); ok {
					parsed, err := strconv.ParseBool(strings.ToLower(value))
					if err != nil {
						result.Errors = append(result.Errors, issue("INVALID_ANSWER_LIMIT", "allow_number must be true or false", lineNumber, result.Passages[group.passage].Number, 0))
					} else {
						group.answerLimitNum = &parsed
					}
					continue
				}
				mode = "group"
			}
			if strings.EqualFold(line, "instruction:") {
				mode = "instruction"
				continue
			}
			if value, ok := v1Field(line, "instruction"); ok {
				group.instructions = append(group.instructions, value)
				mode = "instruction"
				continue
			}
			if strings.EqualFold(line, "options:") {
				mode = "options"
				sharedOptionsMode = true
				continue
			}
			if strings.EqualFold(line, "content:") {
				mode = "content"
				group.contentLineStart = lineNumber + 1
				continue
			}
			if match := questionLinePattern.FindStringSubmatch(line); match != nil {
				number, _ := strconv.Atoi(match[1])
				group.questions = append(group.questions, v1QuestionBuilder{number: number, prompt: match[2], line: lineNumber})
				currentQuestion = len(group.questions) - 1
				mode = "questions"
				sharedOptionsMode = false
				continue
			}
			if option := optionLinePattern.FindStringSubmatch(line); option != nil && (mode == "options" || mode == "questions") {
				entry := map[string]any{"id": option[1], "text": option[2]}
				if sharedOptionsMode || currentQuestion < 0 {
					group.sharedOptions = append(group.sharedOptions, entry)
				} else {
					group.questions[currentQuestion].options = append(group.questions[currentQuestion].options, entry)
				}
				continue
			}
			switch mode {
			case "instruction":
				group.instructions = append(group.instructions, raw)
			case "content":
				group.contentLines = append(group.contentLines, raw)
			default:
				result.Errors = append(result.Errors, issue("UNRECOGNIZED_GROUP_LINE", "Unrecognized line inside question group", lineNumber, result.Passages[group.passage].Number, 0))
			}
			continue
		}

		if value, ok := v1Field(line, "title"); ok {
			if currentPassage >= 0 {
				result.Passages[currentPassage].Material.Title = strings.TrimSpace(value)
			} else {
				result.Title = strings.TrimSpace(value)
			}
			continue
		}
		if value, ok := v1Field(line, "exam_type"); ok {
			normalized := strings.ToLower(strings.TrimSpace(value))
			if normalized != "academic" && normalized != "general" {
				result.Errors = append(result.Errors, issue("INVALID_EXAM_TYPE", "exam_type must be ACADEMIC or GENERAL", lineNumber, 0, 0))
			} else {
				input.ExamType = normalized
				for passageIndex := range result.Passages {
					result.Passages[passageIndex].Material.ExamType = normalized
				}
			}
			continue
		}
		if value, ok := v1Field(line, "duration_minutes"); ok {
			duration, err := strconv.Atoi(value)
			if err != nil || duration < 1 || duration > 300 {
				result.Errors = append(result.Errors, issue("INVALID_DURATION", "duration_minutes must be between 1 and 300", lineNumber, 0, 0))
			} else {
				result.DurationMinutes = duration
			}
			continue
		}
		result.Errors = append(result.Errors, issue("UNRECOGNIZED_LINE", "Unrecognized canonical format line", lineNumber, 0, 0))
	}

	questionRefs := buildV1Groups(&result, groups)
	applyV1AnswersAndExplanations(&result, questionRefs, answers, explanations)
	finalizeV1Result(&result)
	return result
}

func buildV1Groups(result *ImportResult, builders []v1GroupBuilder) map[int]importQuestionRef {
	refs := map[int]importQuestionRef{}
	placeholders := map[int]int{}
	for _, builder := range builders {
		passageNumber := result.Passages[builder.passage].Number
		if builder.rangeStart < 1 || builder.rangeEnd < builder.rangeStart {
			result.Errors = append(result.Errors, issue("GROUP_RANGE_MISSING", "Every V1 group needs a valid range", builder.line, passageNumber, 0))
		}
		if builder.typeID == "" {
			result.Errors = append(result.Errors, issue("GROUP_TYPE_MISSING", "Every V1 group needs a type", builder.line, passageNumber, 0))
		}
		questions := append([]v1QuestionBuilder{}, builder.questions...)
		contextText := strings.TrimSpace(strings.Join(builder.contentLines, "\n"))
		placeholderLines := map[int]int{}
		for lineOffset, contentLine := range builder.contentLines {
			matches := v1PlaceholderPattern.FindAllStringSubmatch(contentLine, -1)
			for _, match := range matches {
				number, _ := strconv.Atoi(match[1])
				lineNumber := builder.contentLineStart + lineOffset
				if previous, exists := placeholderLines[number]; exists {
					result.Errors = append(result.Errors, issue("DUPLICATE_PLACEHOLDER", fmt.Sprintf("Placeholder {{%d}} was first declared on line %d", number, previous), lineNumber, passageNumber, number))
				}
				placeholderLines[number] = lineNumber
				questions = append(questions, v1QuestionBuilder{number: number, prompt: strings.TrimSpace(contentLine), line: lineNumber})
			}
		}
		sort.SliceStable(questions, func(i, j int) bool { return questions[i].number < questions[j].number })

		group := QuestionGroup{Position: len(result.Passages[builder.passage].Material.QuestionGroups) + 1, Type: builder.typeID, Instructions: strings.TrimSpace(strings.Join(builder.instructions, "\n")), Questions: []Question{}}
		maxWords, allowNumber, limitOK := canonicalAnswerLimit(builder.answerLimit)
		if builder.answerLimitWords > 0 && builder.answerLimitNum != nil {
			maxWords, allowNumber, limitOK = builder.answerLimitWords, *builder.answerLimitNum, true
		}
		hasAnswerLimit := builder.answerLimit != "" || builder.answerLimitWords > 0 || builder.answerLimitNum != nil
		if isCompletionType(builder.typeID) && !hasAnswerLimit {
			result.Warnings = append(result.Warnings, issue("ANSWER_LIMIT_MISSING", "Completion group has no answer_limit", builder.line, passageNumber, 0))
		} else if isCompletionType(builder.typeID) && (builder.answerLimitWords == 0) != (builder.answerLimitNum == nil) {
			result.Errors = append(result.Errors, issue("INVALID_ANSWER_LIMIT", "Structured answer_limit needs max_words and allow_number", builder.line, passageNumber, 0))
		} else if isCompletionType(builder.typeID) && !limitOK {
			result.Errors = append(result.Errors, issue("INVALID_ANSWER_LIMIT", "Unsupported answer_limit: "+builder.answerLimit, builder.line, passageNumber, 0))
		} else if builder.answerLimit != "" && !isCompletionType(builder.typeID) {
			result.Warnings = append(result.Warnings, issue("ANSWER_LIMIT_IGNORED", "answer_limit is only used by completion groups", builder.line, passageNumber, 0))
		}

		actualNumbers := map[int]bool{}
		for _, built := range questions {
			if previous, exists := refs[built.number]; exists {
				result.Errors = append(result.Errors, issue("DUPLICATE_QUESTION", fmt.Sprintf("Duplicate question; first declared on line %d", previous.line), built.line, passageNumber, built.number))
				continue
			}
			actualNumbers[built.number] = true
			content := map[string]any{"number": built.number}
			options := built.options
			if len(options) == 0 {
				options = builder.sharedOptions
			}
			if len(options) > 0 {
				content["options"] = options
			}
			if builder.reuseOptions {
				content["reuse"] = true
			}
			if contextText != "" {
				content["context"] = contextText
			}
			prompt := built.prompt
			if isCompletionType(builder.typeID) {
				matches := v1PlaceholderPattern.FindAllStringSubmatch(prompt, -1)
				if len(matches) != 1 || matches[0][1] != strconv.Itoa(built.number) {
					result.Errors = append(result.Errors, issue("INVALID_PLACEHOLDER", fmt.Sprintf("Question %d must contain exactly {{%d}}", built.number, built.number), built.line, passageNumber, built.number))
				} else {
					if previousLine, duplicate := placeholders[built.number]; duplicate {
						result.Errors = append(result.Errors, issue("DUPLICATE_PLACEHOLDER", fmt.Sprintf("Placeholder {{%d}} was first declared on line %d", built.number, previousLine), built.line, passageNumber, built.number))
					} else {
						placeholders[built.number] = built.line
					}
					prompt = strings.Replace(prompt, matches[0][0], "{{answer}}", 1)
				}
				if limitOK {
					content["completionRule"] = map[string]any{"maxWords": maxWords, "allowNumber": allowNumber}
				}
			}
			group.Questions = append(group.Questions, Question{Position: len(group.Questions) + 1, Prompt: prompt, Content: content, Answer: map[string]any{}, Points: 1})
			refs[built.number] = importQuestionRef{passage: builder.passage, group: len(result.Passages[builder.passage].Material.QuestionGroups), question: len(group.Questions) - 1, line: built.line}
		}
		if builder.rangeStart > 0 && builder.rangeEnd >= builder.rangeStart {
			for number := builder.rangeStart; number <= builder.rangeEnd; number++ {
				if !actualNumbers[number] {
					result.Errors = append(result.Errors, issue("RANGE_QUESTION_MISSING", fmt.Sprintf("Group range includes missing question %d", number), builder.line, passageNumber, number))
				}
			}
			for number := range actualNumbers {
				if number < builder.rangeStart || number > builder.rangeEnd {
					result.Errors = append(result.Errors, issue("QUESTION_OUTSIDE_RANGE", fmt.Sprintf("Question %d is outside group range %d-%d", number, builder.rangeStart, builder.rangeEnd), builder.line, passageNumber, number))
				}
			}
		}
		result.Passages[builder.passage].Material.QuestionGroups = append(result.Passages[builder.passage].Material.QuestionGroups, group)
	}
	return refs
}

func applyV1AnswersAndExplanations(result *ImportResult, refs map[int]importQuestionRef, answers map[int]importAnswer, explanations map[int]v1Explanation) {
	answerNumbers := sortedImportNumbers(answers)
	for _, number := range answerNumbers {
		answer := answers[number]
		ref, exists := refs[number]
		if !exists {
			result.Errors = append(result.Errors, issue("UNKNOWN_QUESTION_NUMBER", "Answer references an unknown question", answer.line, 0, number))
			continue
		}
		group := &result.Passages[ref.passage].Material.QuestionGroups[ref.group]
		question := &group.Questions[ref.question]
		if message := setImportedAnswer(group.Type, question, answer.raw); message != "" {
			result.Errors = append(result.Errors, issue("INVALID_ANSWER", message, answer.line, result.Passages[ref.passage].Number, number))
		} else if group.Type == QuestionMultipleChoice {
			expected := multipleChoiceAnswerCount(group.Instructions)
			actual := 1
			if values, ok := question.Answer["optionIds"].([]any); ok {
				actual = len(values)
			}
			if actual != expected {
				result.Errors = append(result.Errors, issue("INVALID_ANSWER_COUNT", fmt.Sprintf("Question expects %d answer(s), received %d", expected, actual), answer.line, result.Passages[ref.passage].Number, number))
			}
		}
	}
	questionNumbers := make([]int, 0, len(refs))
	for number := range refs {
		questionNumbers = append(questionNumbers, number)
	}
	sort.Ints(questionNumbers)
	for _, number := range questionNumbers {
		ref := refs[number]
		if _, exists := answers[number]; !exists {
			result.Errors = append(result.Errors, issue("ANSWER_MISSING", "No answer provided", ref.line, result.Passages[ref.passage].Number, number))
		}
		if explanation, exists := explanations[number]; exists {
			result.Passages[ref.passage].Material.QuestionGroups[ref.group].Questions[ref.question].Explanation = strings.TrimSpace(strings.Join(explanation.lines, "\n"))
		}
	}
	for number, explanation := range explanations {
		if _, exists := refs[number]; !exists {
			result.Errors = append(result.Errors, issue("UNKNOWN_EXPLANATION_QUESTION", "Explanation references an unknown question", explanation.line, 0, number))
		}
	}
}

func multipleChoiceAnswerCount(instruction string) int {
	upper := strings.ToUpper(instruction)
	switch {
	case strings.Contains(upper, "CHOOSE FOUR"):
		return 4
	case strings.Contains(upper, "CHOOSE THREE"):
		return 3
	case strings.Contains(upper, "CHOOSE TWO"):
		return 2
	default:
		return 1
	}
}

func finalizeV1Result(result *ImportResult) {
	totalGroups, totalQuestions, totalExplanations := 0, 0, 0
	allNumbers := []int{}
	for passageIndex := range result.Passages {
		passage := &result.Passages[passageIndex]
		passage.Material.Body = strings.TrimSpace(passage.Material.Body)
		if passage.Material.Title == "" {
			passage.Material.Title = fmt.Sprintf("%s - Passage %d", fallbackTitle(result.Title), passage.Number)
			result.Warnings = append(result.Warnings, issue("PASSAGE_TITLE_MISSING", "Passage title is missing; a fallback title was generated", 0, passage.Number, 0))
		}
		passage.Material.Description = "Imported with " + importFormatV1
		if len([]rune(passage.Material.Body)) < 50 {
			result.Errors = append(result.Errors, issue("PASSAGE_TOO_SHORT", "Passage text must contain at least 50 characters", 0, passage.Number, 0))
		}
		totalGroups += len(passage.Material.QuestionGroups)
		for _, group := range passage.Material.QuestionGroups {
			totalQuestions += len(group.Questions)
			for _, question := range group.Questions {
				if question.Explanation != "" {
					totalExplanations++
				}
				if number, ok := question.Content["number"].(int); ok {
					allNumbers = append(allNumbers, number)
				}
			}
		}
	}
	if result.Title == "" {
		result.Title = "IELTS Reading Import"
		result.Warnings = append(result.Warnings, issue("TEST_TITLE_MISSING", "Test title is missing; a fallback title was generated", 0, 0, 0))
	}
	if len(result.Passages) == 0 {
		result.Errors = append(result.Errors, issue("PASSAGE_MISSING", "No passage detected", 0, 0, 0))
	}
	sort.Ints(allNumbers)
	for index := 1; index < len(allNumbers); index++ {
		if allNumbers[index] != allNumbers[index-1]+1 {
			result.Warnings = append(result.Warnings, issue("NON_CONTIGUOUS_NUMBERING", fmt.Sprintf("Question numbering jumps from %d to %d", allNumbers[index-1], allNumbers[index]), 0, 0, 0))
			break
		}
	}
	if totalQuestions != 40 {
		result.Warnings = append(result.Warnings, issue("UNUSUAL_QUESTION_COUNT", fmt.Sprintf("IELTS Reading usually contains 40 questions; detected %d", totalQuestions), 0, 0, 0))
	}
	result.Info = append(result.Info,
		issue("PASSAGE_COUNT", fmt.Sprintf("Passages: %d", len(result.Passages)), 0, 0, 0),
		issue("GROUP_COUNT", fmt.Sprintf("Groups: %d", totalGroups), 0, 0, 0),
		issue("QUESTION_COUNT", fmt.Sprintf("Questions: %d", totalQuestions), 0, 0, 0),
		issue("EXPLANATION_COUNT", fmt.Sprintf("Explanations: %d/%d", totalExplanations, totalQuestions), 0, 0, 0),
	)
}

func canonicalAnswerLimit(value string) (int, bool, bool) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "ONE_WORD_ONLY":
		return 1, false, true
	case "ONE_WORD_AND_OR_NUMBER":
		return 1, true, true
	case "NO_MORE_THAN_TWO_WORDS":
		return 2, false, true
	case "NO_MORE_THAN_TWO_WORDS_AND_OR_NUMBER":
		return 2, true, true
	case "NO_MORE_THAN_THREE_WORDS":
		return 3, false, true
	case "NO_MORE_THAN_THREE_WORDS_AND_OR_NUMBER":
		return 3, true, true
	default:
		return 0, false, false
	}
}

func sortedImportNumbers(values map[int]importAnswer) []int {
	result := make([]int, 0, len(values))
	for number := range values {
		result = append(result, number)
	}
	sort.Ints(result)
	return result
}

func v1Field(line, name string) (string, bool) {
	prefix := name + ":"
	if len(line) < len(prefix) || !strings.EqualFold(line[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(line[len(prefix):]), true
}
