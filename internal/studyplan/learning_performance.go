package studyplan

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const performanceRefLimit = 5

// Names describe formats, not inferred skills (short_answer is not spelling).
var performanceFormats = map[string]map[string]string{
	"reading": {
		"multiple_choice": "Выбор ответа", "true_false_not_given": "True / False / Not Given",
		"yes_no_not_given": "Yes / No / Not Given", "matching_information": "Соотнесение информации",
		"matching_headings": "Подбор заголовков", "matching_features": "Соотнесение признаков",
		"matching_sentence_endings": "Соотнесение окончаний предложений", "sentence_completion": "Заполнение предложений",
		"summary_completion": "Заполнение краткого изложения", "note_completion": "Заполнение заметок",
		"table_completion": "Заполнение таблицы", "flow_chart_completion": "Заполнение блок-схемы",
		"diagram_label_completion": "Подписи к диаграмме", "short_answer": "Краткий ответ",
	},
	"listening": {
		"multiple_choice": "Выбор ответа", "matching": "Соотнесение", "map_labelling": "Подписи к карте",
		"plan_labelling": "Подписи к плану", "diagram_labelling": "Подписи к диаграмме",
		"form_completion": "Заполнение формы", "note_completion": "Заполнение заметок",
		"table_completion": "Заполнение таблицы", "flow_chart_completion": "Заполнение блок-схемы",
		"sentence_completion": "Заполнение предложений", "short_answer": "Краткий ответ",
	},
}

type performanceAnswer struct {
	Section, QuestionType, AttemptID, MaterialID, Title, Stimulus string
	OccurredAt                                                    time.Time
	Correct                                                       bool
}

// This projection reads immutable submitted answers, including correct and graded
// blank answers. A NULL grade is not evidence. The lateral join proves that the
// question belongs to this attempt's version (including child Reading passages).
func loadTaskPerformance(ctx context.Context, tx pgx.Tx, user uuid.UUID) ([]TaskPerformance, error) {
	rows, err := tx.Query(ctx, `SELECT a.material_type,q.question_type,a.id,q.source_id,q.title,q.stimulus,a.submitted_at,aa.is_correct
 FROM attempts a
 JOIN attempt_answers aa ON aa.attempt_id=a.id AND aa.is_correct IS NOT NULL
 JOIN LATERAL (
   SELECT g.question_type,v.material_id AS source_id,v.title,v.body AS stimulus
   FROM reading_questions r JOIN reading_question_groups g ON g.id=r.group_id
   JOIN reading_material_versions v ON v.id=g.material_version_id
   JOIN reading_material_versions av ON av.id=a.material_version_id AND av.material_id=a.material_id
   WHERE a.material_type='reading' AND r.id=aa.question_id AND
     (v.id=av.id OR EXISTS(SELECT 1 FROM reading_test_passages tp WHERE tp.test_material_version_id=av.id AND tp.passage_material_version_id=v.id AND tp.passage_material_id=v.material_id))
   UNION ALL
   SELECT g.question_type,v.test_id,v.title,
     COALESCE((SELECT CASE WHEN count(*)=count(lp.audio_asset_id) THEN string_agg(lp.audio_asset_id::text,',' ORDER BY lp.position) END FROM listening_parts lp WHERE lp.test_version_id=v.id),'')
   FROM listening_questions l JOIN listening_question_groups g ON g.id=l.group_id
   JOIN listening_parts p ON p.id=g.part_id JOIN listening_test_versions v ON v.id=p.test_version_id
   WHERE a.material_type='listening' AND l.id=aa.question_id AND v.id=a.material_version_id AND v.test_id=a.material_id
 ) q ON true
 WHERE a.user_id=$1 AND a.status='SUBMITTED' AND a.submitted_at IS NOT NULL
 AND NOT EXISTS(SELECT 1 FROM full_mock_session_sections sec JOIN full_mock_sessions fm ON fm.id=sec.session_id WHERE sec.attempt_id=a.id AND fm.status<>'SUBMITTED')
 ORDER BY a.submitted_at,a.id,aa.question_id`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	answers := []performanceAnswer{}
	for rows.Next() {
		var a performanceAnswer
		if err := rows.Scan(&a.Section, &a.QuestionType, &a.AttemptID, &a.MaterialID, &a.Title, &a.Stimulus, &a.OccurredAt, &a.Correct); err != nil {
			return nil, err
		}
		answers = append(answers, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return taskPerformance(answers), nil
}

func taskPerformance(answers []performanceAnswer) []TaskPerformance {
	// Union material identity across versions AND equal known stimuli across IDs.
	// This is conservative repeat detection, not calibrated source independence.
	// Complete ordered audio asset sets identify duplicated Listening tests; a
	// missing asset does not establish equality with another material.
	parents := map[string]string{}
	var root func(string) string
	root = func(k string) string {
		p, ok := parents[k]
		if !ok {
			parents[k] = k
			return k
		}
		if p != k {
			parents[k] = root(p)
		}
		return parents[k]
	}
	stimuli := map[string]string{}
	for _, a := range answers {
		material := a.Section + ":" + a.MaterialID
		r := root(material)
		if stimulus := normalizedStimulus(a.Stimulus); stimulus != "" {
			key := fmt.Sprintf("%s:%x", a.Section, sha256.Sum256([]byte(stimulus)))
			if previous, ok := stimuli[key]; ok {
				parents[r] = root(previous)
			} else {
				stimuli[key] = material
			}
		}
	}
	byType := map[string]*TaskPerformance{}
	addType := func(section, typ string) *TaskPerformance {
		if strings.TrimSpace(typ) == "" {
			typ = "unknown"
		}
		key := section + ":" + typ
		if p := byType[key]; p != nil {
			return p
		}
		title := performanceFormats[section][typ]
		if title == "" {
			title = "Неизвестный тип задания"
			if typ != "unknown" {
				title += " (" + typ + ")"
			}
		}
		p := &TaskPerformance{Section: section, QuestionType: typ, Title: title, SourceRefs: []TaskPerformanceSource{}, Limitations: []string{
			"Только проверенные ответы сохранённых работ Reading/Listening. Непроверенные ответы и незавершённые Mock исключены; проверенный пустой ответ учитывается по сохранённому результату.",
			"Основной подсчёт относится к первой проверенной работе с каждым источником; повторные ответы, включая другие версии, показаны отдельно.",
			"Короткие упражнения, сохранённые правки и перенос навыка не входят в эти счётчики. Формат вопроса не устанавливает причину ошибки, освоение навыка или балл IELTS.",
			"Редакторская проверка и независимость источников здесь не установлены. Совпадения текста/полного набора аудио объединяются, но переработанные копии могут остаться нераспознанными.",
			"Показаны ссылки максимум на пять последних источников; общее число включает остальные.",
		}}
		if section == "reading" {
			p.Limitations = append(p.Limitations, "Один источник — логический passage. Passage внутри Reading TEST и отдельно, а также его версии, не считаются новыми текстами.")
		} else {
			p.Limitations = append(p.Limitations, "Один источник — материал Listening (тест целиком), а не каждый его part или вопрос. Версии одного материала считаются повторами.")
		}
		byType[key] = p
		return p
	}
	for section, formats := range performanceFormats {
		for typ := range formats {
			addType(section, typ)
		}
	}
	first := map[string]string{}
	sourceIDs := map[string]string{}
	refs := map[*TaskPerformance]map[string]*TaskPerformanceSource{}
	for _, a := range answers {
		if a.Section != "reading" && a.Section != "listening" {
			continue
		}
		p := addType(a.Section, a.QuestionType)
		source := root(a.Section + ":" + a.MaterialID)
		if _, ok := first[source]; !ok {
			first[source] = a.AttemptID
			// Public identity follows the first observed material, not the union
			// root selected while discovering later versions/copies.
			sourceIDs[source] = a.Section + ":" + a.MaterialID
		}
		if first[source] != a.AttemptID {
			p.PracticeAnswered++
			continue
		}
		if refs[p] == nil {
			refs[p] = map[string]*TaskPerformanceSource{}
		}
		ref := refs[p][source]
		if ref == nil {
			ref = &TaskPerformanceSource{SourceID: sourceIDs[source], AttemptID: a.AttemptID, Title: plainLearningText(a.Title), OccurredAt: a.OccurredAt.UTC().Format(time.RFC3339)}
			refs[p][source] = ref
		}
		p.Answered++
		ref.Answered++
		if a.Correct {
			p.Correct++
			ref.Correct++
		} else {
			p.Incorrect++
			ref.Incorrect++
		}
	}
	out := make([]TaskPerformance, 0, len(byType))
	for _, p := range byType {
		p.SourceCount = len(refs[p])
		for _, ref := range refs[p] {
			p.SourceRefs = append(p.SourceRefs, *ref)
		}
		sort.Slice(p.SourceRefs, func(i, j int) bool {
			a, b := p.SourceRefs[i], p.SourceRefs[j]
			if a.OccurredAt != b.OccurredAt {
				return a.OccurredAt > b.OccurredAt
			}
			return a.SourceID < b.SourceID
		})
		if len(p.SourceRefs) > performanceRefLimit {
			p.SourceRefs = p.SourceRefs[:performanceRefLimit]
		}
		if p.SourceCount == 0 {
			p.Limitations = append(p.Limitations, "Нет проверенных ответов в первой работе с источником по этому типу задания.")
		} else if p.SourceCount == 1 {
			p.Limitations = append(p.Limitations, "Наблюдение только на одном источнике; данных мало для обобщения.")
		}
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Section != out[j].Section {
			return out[i].Section < out[j].Section
		}
		return out[i].QuestionType < out[j].QuestionType
	})
	return out
}
