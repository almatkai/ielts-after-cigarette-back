package listening

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

const ImportFormatV1 = "IELTS_LISTENING_IMPORT_V1"

var (
	partPattern        = regexp.MustCompile(`(?i)^##\s+PART\s+(\d+)\s*$`)
	groupPattern       = regexp.MustCompile(`(?i)^###\s+GROUP\s+(\d+)\s*$`)
	questionPattern    = regexp.MustCompile(`^(\d+)[\.)]\s*(.+)$`)
	answerPattern      = regexp.MustCompile(`^(\d+)\s*:\s*(.+)$`)
	optionPattern      = regexp.MustCompile(`^([A-Z])\s*[\.:]\s*(.+)$`)
	rangePattern       = regexp.MustCompile(`^(\d+)\s*[-\x{2013}]\s*(\d+)$`)
	placeholderPattern = regexp.MustCompile(`\{\{(\d+)\}\}`)
	explanationPattern = regexp.MustCompile(`^###\s+(\d+)\s*$`)
)

type importGroup struct {
	part, line, start, end                    int
	typeID, instruction, context, answerLimit string
	imageID                                   *uuid.UUID
	reuse                                     bool
	sharedOptions                             []any
	questions                                 []Question
}
type importRef struct{ part, group, question, line int }
type importValue struct {
	value string
	line  int
}

func ParseImport(input ImportParseInput) ImportResult {
	source := strings.ReplaceAll(strings.ReplaceAll(input.Source, "\r\n", "\n"), "\r", "\n")
	result := ImportResult{FormatVersion: ImportFormatV1, Test: SaveInput{ExamType: strings.ToLower(strings.TrimSpace(input.ExamType)), DurationMinutes: 40, Parts: []Part{}}, Errors: []ImportIssue{}, Warnings: []ImportIssue{}, Info: []ImportIssue{}}
	if result.Test.ExamType == "" {
		result.Test.ExamType = "academic"
	}
	first := ""
	for _, line := range strings.Split(source, "\n") {
		if strings.TrimSpace(line) != "" {
			first = strings.TrimSpace(line)
			break
		}
	}
	if first != "# "+ImportFormatV1 {
		result.Errors = append(result.Errors, importIssue("INVALID_FORMAT_HEADER", "First non-empty line must be # "+ImportFormatV1, 1, 0, 0))
		return result
	}

	lines := strings.Split(source, "\n")
	groups := []importGroup{}
	answers := map[int]importValue{}
	explanations := map[int]importValue{}
	part, group, currentQuestion := -1, -1, -1
	mode := "meta"
	explanationNumber := 0
	explanationLines := []string{}
	flushExplanation := func() {
		if explanationNumber > 0 {
			entry := explanations[explanationNumber]
			entry.value = strings.TrimSpace(strings.Join(explanationLines, "\n"))
			explanations[explanationNumber] = entry
		}
		explanationNumber = 0
		explanationLines = nil
	}
	for index, raw := range lines {
		lineNo := index + 1
		line := strings.TrimSpace(raw)
		if line == "# "+ImportFormatV1 {
			continue
		}
		if line == "" {
			if mode == "context" && group >= 0 {
				groups[group].context += "\n"
			}
			if mode == "explanation" {
				explanationLines = append(explanationLines, "")
			}
			continue
		}
		if match := partPattern.FindStringSubmatch(line); match != nil {
			flushExplanation()
			number, _ := strconv.Atoi(match[1])
			result.Test.Parts = append(result.Test.Parts, Part{Position: number, Groups: []QuestionGroup{}})
			part = len(result.Test.Parts) - 1
			group, currentQuestion = -1, -1
			mode = "part"
			continue
		}
		if match := groupPattern.FindStringSubmatch(line); match != nil {
			flushExplanation()
			if part < 0 {
				result.Errors = append(result.Errors, importIssue("GROUP_WITHOUT_PART", "Group must belong to a part", lineNo, 0, 0))
				continue
			}
			groups = append(groups, importGroup{part: part, line: lineNo})
			group = len(groups) - 1
			currentQuestion = -1
			mode = "group"
			continue
		}
		switch strings.ToUpper(line) {
		case "## ANSWERS":
			flushExplanation()
			mode = "answers"
			group = -1
			continue
		case "## EXPLANATIONS":
			flushExplanation()
			mode = "explanations"
			group = -1
			continue
		}
		if mode == "answers" {
			m := answerPattern.FindStringSubmatch(line)
			if m == nil {
				result.Errors = append(result.Errors, importIssue("INVALID_ANSWER_LINE", "Use number: answer", lineNo, 0, 0))
				continue
			}
			n, _ := strconv.Atoi(m[1])
			if previous, ok := answers[n]; ok {
				result.Errors = append(result.Errors, importIssue("DUPLICATE_ANSWER", fmt.Sprintf("First answer is on line %d", previous.line), lineNo, 0, n))
			} else {
				answers[n] = importValue{value: strings.TrimSpace(m[2]), line: lineNo}
			}
			continue
		}
		if mode == "explanations" || mode == "explanation" {
			if m := explanationPattern.FindStringSubmatch(line); m != nil {
				flushExplanation()
				n, _ := strconv.Atoi(m[1])
				if previous, ok := explanations[n]; ok {
					result.Errors = append(result.Errors, importIssue("DUPLICATE_EXPLANATION", fmt.Sprintf("First explanation is on line %d", previous.line), lineNo, 0, n))
				}
				explanationNumber = n
				explanations[n] = importValue{line: lineNo}
				mode = "explanation"
			} else if explanationNumber > 0 {
				explanationLines = append(explanationLines, raw)
			} else {
				result.Errors = append(result.Errors, importIssue("EXPLANATION_HEADING_MISSING", "Use ### question-number", lineNo, 0, 0))
			}
			continue
		}
		if part >= 0 && group < 0 {
			if value, ok := field(line, "title"); ok {
				result.Test.Parts[part].Title = value
				continue
			}
			if value, ok := field(line, "audio_asset_id"); ok {
				id, err := uuid.Parse(value)
				if err != nil {
					result.Errors = append(result.Errors, importIssue("INVALID_AUDIO_ASSET_ID", "audio_asset_id must be UUID", lineNo, result.Test.Parts[part].Position, 0))
				} else {
					result.Test.Parts[part].AudioAssetID = &id
				}
				continue
			}
		}
		if group >= 0 {
			g := &groups[group]
			partNo := result.Test.Parts[g.part].Position
			if value, ok := field(line, "range"); ok {
				m := rangePattern.FindStringSubmatch(value)
				if m == nil {
					result.Errors = append(result.Errors, importIssue("INVALID_RANGE", "range must use start-end", lineNo, partNo, 0))
				} else {
					g.start, _ = strconv.Atoi(m[1])
					g.end, _ = strconv.Atoi(m[2])
				}
				continue
			}
			if value, ok := field(line, "type"); ok {
				g.typeID = strings.ToLower(value)
				if !supported(g.typeID) {
					result.Errors = append(result.Errors, importIssue("UNKNOWN_QUESTION_TYPE", "Unsupported type: "+value, lineNo, partNo, 0))
				}
				continue
			}
			if value, ok := field(line, "reuse_options"); ok {
				parsed, err := strconv.ParseBool(strings.ToLower(value))
				if err != nil {
					result.Errors = append(result.Errors, importIssue("INVALID_REUSE_OPTIONS", "reuse_options must be true or false", lineNo, partNo, 0))
				} else {
					g.reuse = parsed
				}
				continue
			}
			if value, ok := field(line, "image_asset_id"); ok {
				id, err := uuid.Parse(value)
				if err != nil {
					result.Errors = append(result.Errors, importIssue("INVALID_IMAGE_ASSET_ID", "image_asset_id must be UUID", lineNo, partNo, 0))
				} else {
					g.imageID = &id
				}
				continue
			}
			if value, ok := field(line, "answer_limit"); ok {
				g.answerLimit = strings.ToUpper(value)
				continue
			}
			if strings.EqualFold(line, "instruction:") {
				mode = "instruction"
				continue
			}
			if strings.EqualFold(line, "content:") {
				mode = "context"
				continue
			}
			if strings.EqualFold(line, "options:") {
				mode = "options"
				continue
			}
			if m := questionPattern.FindStringSubmatch(line); m != nil {
				n, _ := strconv.Atoi(m[1])
				g.questions = append(g.questions, Question{Number: n, Prompt: m[2], Content: map[string]any{}, Answer: map[string]any{}, Points: 1})
				currentQuestion = len(g.questions) - 1
				mode = "questions"
				continue
			}
			if m := optionPattern.FindStringSubmatch(line); m != nil && (mode == "options" || mode == "questions") {
				option := map[string]any{"id": m[1], "text": m[2]}
				if mode == "options" || currentQuestion < 0 {
					g.sharedOptions = append(g.sharedOptions, option)
				} else {
					options, _ := g.questions[currentQuestion].Content["options"].([]any)
					g.questions[currentQuestion].Content["options"] = append(options, option)
				}
				continue
			}
			switch mode {
			case "instruction":
				if g.instruction != "" {
					g.instruction += "\n"
				}
				g.instruction += raw
			case "context":
				if g.context != "" && !strings.HasSuffix(g.context, "\n") {
					g.context += "\n"
				}
				g.context += raw
			default:
				result.Errors = append(result.Errors, importIssue("UNRECOGNIZED_GROUP_LINE", "Unrecognized line in group", lineNo, partNo, 0))
			}
			continue
		}
		if value, ok := field(line, "title"); ok {
			result.Test.Title = value
			continue
		}
		if value, ok := field(line, "exam_type"); ok {
			result.Test.ExamType = strings.ToLower(value)
			if result.Test.ExamType != "academic" && result.Test.ExamType != "general" {
				result.Errors = append(result.Errors, importIssue("INVALID_EXAM_TYPE", "exam_type must be ACADEMIC or GENERAL", lineNo, 0, 0))
			}
			continue
		}
		if value, ok := field(line, "duration_minutes"); ok {
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || n > 180 {
				result.Errors = append(result.Errors, importIssue("INVALID_DURATION", "duration_minutes must be 1-180", lineNo, 0, 0))
			} else {
				result.Test.DurationMinutes = n
			}
			continue
		}
		result.Errors = append(result.Errors, importIssue("UNRECOGNIZED_LINE", "Unrecognized format line", lineNo, 0, 0))
	}
	flushExplanation()
	refs := buildGroups(&result, groups)
	applyAnswers(&result, refs, answers, explanations)
	finalizeImport(&result)
	return result
}

func buildGroups(result *ImportResult, builders []importGroup) map[int]importRef {
	refs := map[int]importRef{}
	for _, b := range builders {
		partNo := result.Test.Parts[b.part].Position
		if b.start < 1 || b.end < b.start {
			result.Errors = append(result.Errors, importIssue("GROUP_RANGE_MISSING", "Group needs a valid range", b.line, partNo, 0))
		}
		if b.typeID == "" {
			result.Errors = append(result.Errors, importIssue("GROUP_TYPE_MISSING", "Group needs a type", b.line, partNo, 0))
		}
		questions := append([]Question{}, b.questions...)
		for _, line := range strings.Split(b.context, "\n") {
			for _, match := range placeholderPattern.FindAllStringSubmatch(line, -1) {
				n, _ := strconv.Atoi(match[1])
				questions = append(questions, Question{Number: n, Prompt: strings.TrimSpace(line), Content: map[string]any{}, Answer: map[string]any{}, Points: 1})
			}
		}
		sort.SliceStable(questions, func(i, j int) bool { return questions[i].Number < questions[j].Number })
		config := map[string]any{}
		if b.reuse {
			config["reuseOptions"] = true
		}
		if len(b.sharedOptions) > 0 {
			config["options"] = b.sharedOptions
		}
		if maxWords, allowNumber, ok := answerLimit(b.answerLimit); ok {
			config["answerLimit"] = map[string]any{"maxWords": maxWords, "allowNumber": allowNumber}
		} else if completion(b.typeID) && b.answerLimit == "" {
			result.Warnings = append(result.Warnings, importIssue("ANSWER_LIMIT_MISSING", "Completion group has no answer_limit", b.line, partNo, 0))
		} else if b.answerLimit != "" {
			result.Errors = append(result.Errors, importIssue("INVALID_ANSWER_LIMIT", "Unsupported answer_limit", b.line, partNo, 0))
		}
		group := QuestionGroup{Position: len(result.Test.Parts[b.part].Groups) + 1, Type: b.typeID, Instructions: strings.TrimSpace(b.instruction), Context: strings.TrimSpace(b.context), Config: config, ImageAssetID: b.imageID, Questions: []Question{}}
		seen := map[int]bool{}
		for _, q := range questions {
			if ref, ok := refs[q.Number]; ok {
				result.Errors = append(result.Errors, importIssue("DUPLICATE_QUESTION", fmt.Sprintf("First question is on line %d", ref.line), b.line, partNo, q.Number))
				continue
			}
			if seen[q.Number] {
				result.Errors = append(result.Errors, importIssue("DUPLICATE_PLACEHOLDER", "Duplicate placeholder", b.line, partNo, q.Number))
				continue
			}
			seen[q.Number] = true
			if q.Number < b.start || q.Number > b.end {
				result.Errors = append(result.Errors, importIssue("QUESTION_OUTSIDE_RANGE", "Question is outside group range", b.line, partNo, q.Number))
			}
			if completion(b.typeID) {
				matches := placeholderPattern.FindAllStringSubmatch(q.Prompt, -1)
				if len(matches) != 1 || matches[0][1] != strconv.Itoa(q.Number) {
					result.Errors = append(result.Errors, importIssue("INVALID_PLACEHOLDER", fmt.Sprintf("Question needs exactly {{%d}}", q.Number), b.line, partNo, q.Number))
				} else {
					q.Prompt = strings.Replace(q.Prompt, matches[0][0], "{{answer}}", 1)
				}
			}
			if len(q.Content) == 0 && len(b.sharedOptions) > 0 {
				q.Content["options"] = b.sharedOptions
			}
			q.Position = len(group.Questions) + 1
			group.Questions = append(group.Questions, q)
			refs[q.Number] = importRef{part: b.part, group: len(result.Test.Parts[b.part].Groups), question: len(group.Questions) - 1, line: b.line}
		}
		for n := b.start; n <= b.end; n++ {
			if !seen[n] {
				result.Errors = append(result.Errors, importIssue("RANGE_QUESTION_MISSING", "Range includes missing question", b.line, partNo, n))
			}
		}
		result.Test.Parts[b.part].Groups = append(result.Test.Parts[b.part].Groups, group)
	}
	return refs
}

func applyAnswers(result *ImportResult, refs map[int]importRef, answers, explanations map[int]importValue) {
	for n, a := range answers {
		ref, ok := refs[n]
		if !ok {
			result.Errors = append(result.Errors, importIssue("UNKNOWN_QUESTION_NUMBER", "Answer references unknown question", a.line, 0, n))
			continue
		}
		g := &result.Test.Parts[ref.part].Groups[ref.group]
		q := &g.Questions[ref.question]
		values := splitValues(a.value)
		switch {
		case g.Type == TypeMultipleChoice || g.Type == TypeMatching || strings.HasSuffix(g.Type, "labelling"):
			for i := range values {
				values[i] = strings.ToUpper(values[i])
			}
			if len(values) == 1 {
				q.Answer = map[string]any{"optionId": values[0]}
			} else {
				q.Answer = map[string]any{"optionIds": toAny(values)}
			}
			if message := validateOptions(q.Content, g.Config, values); message != "" {
				result.Errors = append(result.Errors, importIssue("INVALID_ANSWER", message, a.line, result.Test.Parts[ref.part].Position, n))
			}
		default:
			q.Answer = map[string]any{"accepted": toAny(values)}
		}
	}
	for n, ref := range refs {
		if _, ok := answers[n]; !ok {
			result.Errors = append(result.Errors, importIssue("ANSWER_MISSING", "Question has no answer", ref.line, result.Test.Parts[ref.part].Position, n))
		}
		if e, ok := explanations[n]; ok {
			result.Test.Parts[ref.part].Groups[ref.group].Questions[ref.question].Explanation = e.value
		}
	}
	for n, e := range explanations {
		if _, ok := refs[n]; !ok {
			result.Errors = append(result.Errors, importIssue("UNKNOWN_EXPLANATION_QUESTION", "Explanation references unknown question", e.line, 0, n))
		}
	}
}
func finalizeImport(r *ImportResult) {
	if r.Test.Title == "" {
		r.Test.Title = "Listening import"
		r.Warnings = append(r.Warnings, importIssue("TITLE_MISSING", "A fallback title was generated", 0, 0, 0))
	}
	if len(r.Test.Parts) == 0 {
		r.Errors = append(r.Errors, importIssue("PART_MISSING", "No parts detected", 0, 0, 0))
	}
	groups, questions, explanations := 0, 0, 0
	for pi := range r.Test.Parts {
		p := &r.Test.Parts[pi]
		if p.Title == "" {
			p.Title = fmt.Sprintf("Part %d", p.Position)
		}
		groups += len(p.Groups)
		for _, g := range p.Groups {
			questions += len(g.Questions)
			for _, q := range g.Questions {
				if q.Explanation != "" {
					explanations++
				}
			}
		}
	}
	if questions != 40 {
		r.Warnings = append(r.Warnings, importIssue("UNUSUAL_QUESTION_COUNT", fmt.Sprintf("Listening usually has 40 questions; detected %d", questions), 0, 0, 0))
	}
	r.Info = append(r.Info, importIssue("PART_COUNT", fmt.Sprintf("Parts: %d", len(r.Test.Parts)), 0, 0, 0), importIssue("GROUP_COUNT", fmt.Sprintf("Groups: %d", groups), 0, 0, 0), importIssue("QUESTION_COUNT", fmt.Sprintf("Questions: %d", questions), 0, 0, 0), importIssue("EXPLANATION_COUNT", fmt.Sprintf("Explanations: %d/%d", explanations, questions), 0, 0, 0))
}
func answerLimit(v string) (int, bool, bool) {
	switch strings.ToUpper(v) {
	case "ONE_WORD_ONLY":
		return 1, false, true
	case "ONE_WORD_OR_A_NUMBER", "ONE_WORD_AND_OR_NUMBER":
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
func completion(v string) bool { return strings.HasSuffix(v, "completion") }
func splitValues(v string) []string {
	parts := strings.Split(v, "|")
	out := []string{}
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
func toAny(v []string) []any {
	out := make([]any, len(v))
	for i := range v {
		out[i] = v[i]
	}
	return out
}
func validateOptions(content, config map[string]any, answers []string) string {
	options, _ := content["options"].([]any)
	if len(options) == 0 {
		options, _ = config["options"].([]any)
	}
	allowed := map[string]bool{}
	for _, raw := range options {
		if option, ok := raw.(map[string]any); ok {
			allowed[fmt.Sprint(option["id"])] = true
		}
	}
	if len(allowed) == 0 {
		return "Options are missing"
	}
	for _, answer := range answers {
		if !allowed[answer] {
			return "Unknown option " + answer
		}
	}
	return ""
}
func field(line, name string) (string, bool) {
	prefix := name + ":"
	if len(line) < len(prefix) || !strings.EqualFold(line[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(line[len(prefix):]), true
}
func importIssue(code, message string, line, part, question int) ImportIssue {
	return ImportIssue{Code: code, Message: message, Line: line, Part: part, QuestionNumber: question}
}
