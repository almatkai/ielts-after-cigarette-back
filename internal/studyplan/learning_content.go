package studyplan

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed content/academic_drafts.json
var draftBank []byte

type drillUnit struct {
	Drill    Drill
	Key      json.RawMessage
	Feedback string
}

var microSkills = []MicroSkill{
	{"ms-w-grammar", "writing", "Грамматика Writing"}, {"ms-w-lexis", "writing", "Лексика Writing"}, {"ms-w-coherence", "writing", "Связность Writing"}, {"ms-w-task-response", "writing", "Выполнение задания Writing (общая рекомендация)"}, {"ms-s-response-development", "speaking", "Рекомендации Speaking (непроверенные)"},
	{"ms-l-numspelling", "listening", "Числа и написание"}, {"ms-l-distractor", "listening", "Дистракторы и самоисправления"}, {"ms-l-signpost", "listening", "Маркеры структуры лекции"}, {"ms-l-paraphrase", "listening", "Распознавание перефразирования"}, {"ms-l-attitude", "listening", "Позиция говорящего"},
	{"ms-r-tfng", "reading", "Доказательства True / False / Not Given"}, {"ms-r-ynng", "reading", "Позиция автора"}, {"ms-r-headings", "reading", "Главная мысль абзаца"}, {"ms-r-paraphrase", "reading", "Перефразирование и детали"}, {"ms-r-flowchart", "reading", "Процессы и причинные связи"},
	{"ms-w1-overview", "writing", "Обзор Task 1"}, {"ms-w1-datagroup", "writing", "Группировка данных Task 1"}, {"ms-w1-precision", "writing", "Числовые сравнения Task 1"}, {"ms-w2-position", "writing", "Позиция Task 2"}, {"ms-w2-argument", "writing", "Развитие аргумента Task 2"},
	{"ms-s-part2-turn", "speaking", "Развёрнутый ответ Part 2"}, {"ms-s-part3-abstract", "speaking", "Абстрактное рассуждение Part 3"}, {"ms-s-discourse", "speaking", "Связность и самокоррекция"}, {"ms-s-hypothetical", "speaking", "Гипотетические конструкции"},
}

func questionSkill(section, typ string) string {
	if section == "reading" {
		switch typ {
		case "true_false_not_given":
			return "ms-r-tfng"
		case "yes_no_not_given":
			return "ms-r-ynng"
		case "matching_headings":
			return "ms-r-headings"
		case "flow_chart_completion":
			return "ms-r-flowchart"
		default:
			return "ms-r-paraphrase"
		}
	}
	switch typ {
	case "multiple_choice":
		return "ms-l-distractor"
	case "form_completion", "table_completion", "short_answer":
		return "ms-l-numspelling"
	case "note_completion", "flow_chart_completion", "sentence_completion":
		return "ms-l-signpost"
	default:
		return "ms-l-paraphrase"
	}
}

// Drafts are embedded in the binary for deployable practice, never loaded from tmp
// at runtime. Their original metadata is retained in the source JSON for review.
func builtinDrills() []drillUnit {
	var bank struct {
		Units []struct {
			ID          string         `json:"id"`
			Section     string         `json:"skill"`
			SkillID     string         `json:"microSkillId"`
			Family      string         `json:"familyId"`
			Title       string         `json:"title"`
			Instruction string         `json:"instruction"`
			Minutes     int            `json:"durationMinutes"`
			Stimulus    map[string]any `json:"stimulus"`
			Questions   []struct {
				Prompt      string   `json:"prompt"`
				Options     []string `json:"options"`
				Answer      string   `json:"answer"`
				Explanation string   `json:"explanation"`
				Evidence    string   `json:"evidence"`
			} `json:"questions"`
		} `json:"units"`
	}
	if err := json.Unmarshal(draftBank, &bank); err != nil {
		panic(err)
	}
	out := []drillUnit{}
	for _, u := range bank.Units {
		// Render numeric tables as readable JSON, deliberately excluding unreviewed SVG.
		delete(u.Stimulus, "svgSpec")
		delete(u.Stimulus, "audioPlaceholder")
		stimulus := readableStimulus(u.Stimulus)
		for _, key := range []string{"passage", "transcript", "prompt", "draftParagraph", "taskCard", "examinerQuestion"} {
			if v, ok := u.Stimulus[key].(string); ok {
				stimulus = v
				break
			}
		}
		d := Drill{ID: u.ID, SkillID: u.SkillID, Section: u.Section, Title: u.Title, DurationMinutes: u.Minutes, FamilyID: ptr(u.Family), Version: "draft-1", Stimulus: stimulus, RightsProvenance: "Original IAC editorial draft, Antigravity 2026-10-24; no third-party exam text. Facts and pedagogy await editorial review.", EditorialStatus: "draft", Introduction: "Оригинальная черновая практика, а не валидированная оценка или прогноз балла IELTS."}
		switch u.Section {
		case "reading", "listening":
			d.Modality = "objective"
			if u.Section == "listening" {
				d.Modality = "script"
				d.Introduction += " Только чтение сценария: аудио недоступно, это не свидетельство аудирования."
			}
			d.Introduction += " " + u.Instruction
			for i, q := range u.Questions {
				v := d
				v.ID = fmt.Sprintf("%s-q%d", d.ID, i+1)
				v.Task = q.Prompt
				if len(q.Options) > 0 {
					v.Task += "\n" + strings.Join(q.Options, "\n")
				}
				key, _ := json.Marshal(map[string]any{"value": q.Answer})
				out = append(out, drillUnit{v, key, q.Explanation + "\nText evidence: " + q.Evidence})
			}
		case "writing":
			d.Modality = "typed"
			switch u.SkillID {
			case "ms-w1-overview":
				d.Task = "Write your own two-sentence overview of the supplied numeric table. Select the main trends and a meaningful contrast; distinguish overall trends from individual figures."
			case "ms-w1-precision":
				d.Task = "Write three accurate comparisons from the numeric table. Include units, periods and the distinction between percentage change and percentage points."
			case "ms-w2-position":
				d.Task = "Write your own 50–80 word introduction that answers how far you agree or disagree and states the position you will develop."
				if strings.Contains(strings.ToLower(stimulus), "discuss both views") {
					d.Task = "Write your own 50–80 word introduction that introduces both views and states your own opinion."
				}
			default:
				d.Task = "Write your own 80–120 word argument paragraph: claim, explanation of the mechanism, concrete example, and a limitation."
			}
			out = append(out, drillUnit{Drill: d, Feedback: "Правка сохранена как непроверенная практика. Сверьте ответ с заданием и исходными данными. Автоматического зачёта критерия или балла нет; нужна экспертная или независимая проверка."})
		case "speaking":
			d.Modality = "typed"
			d.Introduction += " Текстовая тренировка плана ответа; она не подтверждает произношение или устную речь."
			d.Task = "Write a short response plan and your own developed response to the prompt. Add a reason and example. This typed response is not a recording."
			out = append(out, drillUnit{Drill: d, Feedback: "Текстовый ответ сохранён, но не проверен. Прочитайте его вслух для тренировки; напечатанный текст не подтверждает беглость, темп или произношение."})
			audio := d
			audio.ID += "-audio"
			audio.Modality = "audio"
			audio.Task = "Practise the prompt aloud, then select your own submitted Speaking work with a saved recording below. The recording is a practice reference; its relevance to this prompt and pronunciation require independent review."
			audio.Introduction = "Оригинальный черновик Speaking. Связанная запись — образец для тренировки, а не независимый перенос."
			out = append(out, drillUnit{Drill: audio, Feedback: "Ссылка на попытку с аудио сохранена. Соответствие заданию, устная речь и критерии требуют независимой проверки; автоматического освоения нет."})
		}
	}
	// Additional original fictional cases provide genuinely distinct content families;
	// all start as drafts and require the same explicit editorial review gate.
	cases := []struct{ id, family, stimulus, task, key, feedback string }{
		{"iac-r-library", "iac-family-library", "A fictional university library opens at 08:00 on weekdays and 10:00 on Saturdays. It is closed on Sundays.", "The library opens at the same time on weekdays and Saturdays. Answer TRUE, FALSE or NOT GIVEN.", "FALSE", "Сравните два явно указанных времени: 08:00 и 10:00. Они различаются, поэтому утверждение о том же времени прямо опровергнуто. Ответ FALSE; это противоречие, а не отсутствие информации."},
		{"iac-r-transport", "iac-family-transport", "In a fictional campus travel survey, 40 students chose the bus and 25 chose cycling. The survey did not ask why students chose their transport.", "Cyclists preferred cycling because it was cheaper. Answer TRUE, FALSE or NOT GIVEN.", "NOT GIVEN", "Текст сообщает только количество ответов и прямо указывает, что о причинах не спрашивали. Стоимость не названа: нельзя ни подтвердить, ни опровергнуть причину. Ответ NOT GIVEN."},
		{"iac-r-volunteer", "iac-family-volunteer", "In a fictional volunteer programme, all 12 participants completed the orientation. Eight later attended a weekend workshop.", "Every participant completed orientation. Answer TRUE, FALSE or NOT GIVEN.", "TRUE", "Слова all 12 participants completed the orientation прямо подтверждают every participant completed orientation. Число участников другого workshop к утверждению не относится. Ответ TRUE."},
	}
	for _, c := range cases {
		key, _ := json.Marshal(map[string]any{"value": c.key})
		out = append(out, drillUnit{Drill: Drill{ID: c.id, SkillID: "ms-r-tfng", Section: "reading", Title: "Evidence practice: " + c.id, DurationMinutes: 8, Modality: "objective", FamilyID: ptr(c.family), Version: "original-1", Introduction: "Оригинальный вымышленный учебный пример. Редакторский черновик; не валидированный контроль IELTS.", Stimulus: c.stimulus, Task: c.task, RightsProvenance: "Original IAC fictional teaching case, 2026-10-24; no third-party exam content.", EditorialStatus: "draft"}, Key: key, Feedback: c.feedback})
	}
	// Broad tags come only from explicit errors/improvements. These separately
	// executable drafts give those cycles a next practice task without inventing a
	// Task 1 numerical diagnosis or automatically assessing the learner's revision.
	broad := []struct{ id, skill, section, title, stimulus, task string }{
		{"iac-w-grammar", "ms-w-grammar", "writing", "Writing: согласование и собственный пример", "A learner wrote: The number of students have increased, while many students prefers online resources.", "Rewrite both clauses with accurate subject–verb agreement. Then write two original sentences about a study habit, using one singular and one plural subject. Explain your choices."},
		{"iac-w-lexis", "ms-w-lexis", "writing", "Writing: точность словоупотребления", "A learner wrote: Public libraries make many good things for residents because they give a lot of stuff for study.", "Rewrite the sentence with precise, natural vocabulary. Add an original example showing which service helps which users. Prefer accuracy over rare words."},
		{"iac-w-coherence", "ms-w-coherence", "writing", "Writing: связь идеи и примера", "A learner wrote: Libraries provide internet access. The city has many parks. Students can use online journals there. Therefore libraries support study.", "Write a coherent 60–90 word paragraph about library access. Organize claim, explanation and relevant example; clarify pronoun references and remove or connect irrelevant information."},
		{"iac-w-task-response", "ms-w-task-response", "writing", "Writing: ответ на вопрос задания", "Some people argue that local governments should fund public libraries. Others believe this money should support other services. Discuss both views and give your opinion.", "Write an 80–120 word response plan and opening paragraph. Identify both views, state your own position, and explain one reason. This short exercise is not a complete essay."},
		{"iac-s-response-development", "ms-s-response-development", "speaking", "Speaking: рекомендации из разбора (текстовый план)", "What public place in your area is useful to students, and how could it be improved?", "Выберите одну рекомендацию из исходного разбора и запишите, как будете её отрабатывать. Если замечание касается произношения, нужна аудиозапись и независимая проверка: напечатанный текст её не заменяет."},
	}
	for _, b := range broad {
		out = append(out, drillUnit{Drill: Drill{ID: b.id, SkillID: b.skill, Section: b.section, Title: b.title, DurationMinutes: 10, Modality: "typed", FamilyID: ptr("family-" + b.id), Version: "original-1", Introduction: "Оригинальная короткая тренировка. Редакторский черновик; результат и правка остаются непроверенными.", Stimulus: b.stimulus, Task: b.task, RightsProvenance: "Original IAC teaching exercise, 2026-10-24; no third-party exam text.", EditorialStatus: "draft"}, Feedback: "Собственный ответ сохранён, но не проверен. Сверьте его с задачей и обсудите со специалистом; автоматического зачёта критерия, освоения или балла нет."})
	}
	return out
}

// Human-readable source data; no internal schema keys or unreviewed SVG markup
// enter the exercise text. Numeric tables retain every header, unit and cell.
func readableStimulus(stimulus map[string]any) string {
	if table, ok := stimulus["tableData"].(map[string]any); ok {
		lines := []string{}
		if title, ok := stimulus["title"].(string); ok {
			lines = append(lines, title, "")
		}
		rowText := func(raw any) string {
			values, _ := raw.([]any)
			cells := []string{}
			for _, v := range values {
				cells = append(cells, fmt.Sprint(v))
			}
			return strings.Join(cells, " | ")
		}
		lines = append(lines, rowText(table["headers"]))
		if rows, ok := table["rows"].([]any); ok {
			for _, r := range rows {
				lines = append(lines, rowText(r))
			}
		}
		return strings.Join(lines, "\n")
	}
	for _, key := range []string{"passage", "transcript", "prompt", "draftParagraph", "taskCard", "examinerQuestion"} {
		if s, ok := stimulus[key].(string); ok {
			return s
		}
	}
	lines := []string{}
	for _, key := range []string{"context", "title", "examinerQuestions", "questionsList", "taskCard"} {
		switch v := stimulus[key].(type) {
		case string:
			lines = append(lines, v)
		case []any:
			for _, q := range v {
				if s, ok := q.(string); ok {
					lines = append(lines, "• "+s)
				}
			}
		case map[string]any:
			if topic, ok := v["topic"].(string); ok {
				lines = append(lines, topic)
			}
			if bullets, ok := v["bulletPoints"].([]any); ok {
				for _, b := range bullets {
					lines = append(lines, "• "+fmt.Sprint(b))
				}
			}
		}
	}
	return strings.Join(lines, "\n")
}
