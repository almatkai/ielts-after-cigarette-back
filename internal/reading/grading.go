package reading

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/google/uuid"
)

// VersionRef identifies one pinned material version.
type VersionRef struct {
	MaterialID uuid.UUID
	VersionID  uuid.UUID
}

// maxPassageDepth bounds the passage expansion. reading_test_passages points
// from a test version at passage versions, so real data never nests deeper;
// the limit only guards against a corrupted link cycle.
const maxPassageDepth = 4

const gradingVersionSelect = `
	SELECT
		m.id, m.slug, m.exam_type, m.difficulty, m.status, m.material_kind, m.revision,
		v.title, v.description, v.body, v.duration_minutes, v.source_title, v.source_url,
		v.version_number, m.current_version_id, m.published_version_id,
		(m.published_version_id IS DISTINCT FROM m.current_version_id),
		m.published_at, m.created_at, m.updated_at,
		v.id
	FROM reading_materials m
	JOIN reading_material_versions v ON v.material_id = m.id
	WHERE v.id = ANY($1)
`

const gradingQuestionGroupSelect = `
	SELECT g.material_version_id, g.id, g.position, g.question_type, g.instructions,
		q.id, q.position, q.prompt, q.content, q.answer, q.explanation, q.points
	FROM reading_question_groups g
	LEFT JOIN reading_questions q ON q.group_id = g.id
	WHERE g.material_version_id = ANY($1)
	ORDER BY g.material_version_id, g.position, q.position
`

const gradingPassageSelect = `
	SELECT test_material_version_id, passage_material_id, passage_material_version_id
	FROM reading_test_passages
	WHERE test_material_version_id = ANY($1)
	ORDER BY test_material_version_id, position
`

// GradingStructures loads the full structure (question groups plus nested test
// passages) of many pinned versions in a bounded number of queries: three per
// nesting level instead of three per version. Grading a full mock and the
// mistakes page both need the structure of every attempt at once, and on a
// distant database the round trips, not the queries, dominate the latency.
func (r *PostgresRepository) GradingStructures(ctx context.Context, refs []VersionRef) (map[VersionRef]Material, error) {
	result := make(map[VersionRef]Material, len(refs))
	if len(refs) == 0 {
		return result, nil
	}
	versions, passages, err := r.loadGradingVersions(ctx, refs)
	if err != nil {
		return nil, err
	}
	for _, ref := range refs {
		material, ok := versions[ref.VersionID]
		if !ok || material.ID != ref.MaterialID {
			return nil, ErrNotFound
		}
		material.Passages = passageMaterials(versions, passages, ref.VersionID, 0)
		result[ref] = material
	}
	return result, nil
}

// buildQuestion decodes one row of a question-group query, where the question
// columns come from a LEFT JOIN and may be absent.
func buildQuestion(id uuid.UUID, position int, prompt string, content, answer []byte, explanation string, points int) (Question, error) {
	var decodedContent, decodedAnswer map[string]any
	if err := json.Unmarshal(content, &decodedContent); err != nil {
		return Question{}, fmt.Errorf("decode question content: %w", err)
	}
	if err := json.Unmarshal(answer, &decodedAnswer); err != nil {
		return Question{}, fmt.Errorf("decode question answer: %w", err)
	}
	return Question{
		ID:          id,
		Position:    position,
		Prompt:      prompt,
		Content:     decodedContent,
		Answer:      decodedAnswer,
		Explanation: explanation,
		Points:      points,
	}, nil
}

// loadGradingVersions fetches the requested versions level by level, together
// with the versions of the passages they reference. Every level costs three
// queries no matter how many versions it holds.
func (r *PostgresRepository) loadGradingVersions(
	ctx context.Context,
	refs []VersionRef,
) (map[uuid.UUID]Material, map[uuid.UUID][]VersionRef, error) {
	loaded := map[uuid.UUID]Material{}
	passages := map[uuid.UUID][]VersionRef{}
	pending := refs
	for depth := 0; len(pending) > 0 && depth < maxPassageDepth; depth++ {
		versionIDs := make([]uuid.UUID, 0, len(pending))
		queued := map[uuid.UUID]bool{}
		for _, ref := range pending {
			if _, ok := loaded[ref.VersionID]; ok || queued[ref.VersionID] {
				continue
			}
			queued[ref.VersionID] = true
			versionIDs = append(versionIDs, ref.VersionID)
		}
		if len(versionIDs) == 0 {
			break
		}
		materials, groups, links, err := r.loadLevel(ctx, versionIDs)
		if err != nil {
			return nil, nil, err
		}
		next := make([]VersionRef, 0, len(versionIDs))
		for _, versionID := range versionIDs {
			material, ok := materials[versionID]
			if !ok {
				continue
			}
			material.QuestionGroups = groups[versionID]
			if material.QuestionGroups == nil {
				material.QuestionGroups = []QuestionGroup{}
			}
			loaded[versionID] = material
			passages[versionID] = links[versionID]
			next = append(next, links[versionID]...)
		}
		pending = next
	}
	return loaded, passages, nil
}

// loadLevel fetches the version rows, the question groups and the passage links
// of one nesting level. The three queries are independent and only the links
// are needed to descend, so they run together and a level costs one round trip
// instead of three.
func (r *PostgresRepository) loadLevel(
	ctx context.Context,
	versionIDs []uuid.UUID,
) (map[uuid.UUID]Material, map[uuid.UUID][]QuestionGroup, map[uuid.UUID][]VersionRef, error) {
	var (
		materials map[uuid.UUID]Material
		groups    map[uuid.UUID][]QuestionGroup
		links     map[uuid.UUID][]VersionRef
		errs      [3]error
	)
	var wait sync.WaitGroup
	wait.Add(3)
	go func() {
		defer wait.Done()
		materials, errs[0] = r.gradingVersionRows(ctx, versionIDs)
	}()
	go func() {
		defer wait.Done()
		groups, errs[1] = r.gradingQuestionGroups(ctx, versionIDs)
	}()
	go func() {
		defer wait.Done()
		links, errs[2] = r.gradingPassageLinks(ctx, versionIDs)
	}()
	wait.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, nil, nil, err
		}
	}
	return materials, groups, links, nil
}

func (r *PostgresRepository) gradingVersionRows(ctx context.Context, versionIDs []uuid.UUID) (map[uuid.UUID]Material, error) {
	rows, err := r.pool.Query(ctx, gradingVersionSelect, versionIDs)
	if err != nil {
		return nil, fmt.Errorf("list reading material versions: %w", err)
	}
	defer rows.Close()
	items := map[uuid.UUID]Material{}
	for rows.Next() {
		versionID, material, err := scanMaterialVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("scan reading material version: %w", err)
		}
		items[versionID] = material
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reading material versions: %w", err)
	}
	return items, nil
}

// gradingQuestionGroups loads the question groups (with their questions) of
// many versions in one round trip.
func (r *PostgresRepository) gradingQuestionGroups(
	ctx context.Context,
	versionIDs []uuid.UUID,
) (map[uuid.UUID][]QuestionGroup, error) {
	rows, err := r.pool.Query(ctx, gradingQuestionGroupSelect, versionIDs)
	if err != nil {
		return nil, fmt.Errorf("list reading question groups by version: %w", err)
	}
	defer rows.Close()
	type groupKey struct {
		versionID uuid.UUID
		groupID   uuid.UUID
	}
	groups := map[uuid.UUID][]QuestionGroup{}
	index := map[groupKey]int{}
	for rows.Next() {
		var versionID uuid.UUID
		var group QuestionGroup
		var position *int
		var questionID *uuid.UUID
		var prompt, explanation *string
		var content, answer []byte
		var points *int
		if err := rows.Scan(&versionID, &group.ID, &group.Position, &group.Type, &group.Instructions,
			&questionID, &position, &prompt, &content, &answer, &explanation, &points); err != nil {
			return nil, fmt.Errorf("scan reading question group: %w", err)
		}
		key := groupKey{versionID: versionID, groupID: group.ID}
		groupIndex, ok := index[key]
		if !ok {
			groupIndex = len(groups[versionID])
			index[key] = groupIndex
			group.Questions = []Question{}
			groups[versionID] = append(groups[versionID], group)
		}
		if questionID != nil {
			question, err := buildQuestion(*questionID, *position, *prompt, content, answer, *explanation, *points)
			if err != nil {
				return nil, err
			}
			groups[versionID][groupIndex].Questions = append(groups[versionID][groupIndex].Questions, question)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reading question groups: %w", err)
	}
	return groups, nil
}

func (r *PostgresRepository) gradingPassageLinks(
	ctx context.Context,
	testVersionIDs []uuid.UUID,
) (map[uuid.UUID][]VersionRef, error) {
	rows, err := r.pool.Query(ctx, gradingPassageSelect, testVersionIDs)
	if err != nil {
		return nil, fmt.Errorf("list reading test passages by version: %w", err)
	}
	defer rows.Close()
	links := map[uuid.UUID][]VersionRef{}
	for rows.Next() {
		var testVersionID uuid.UUID
		var ref VersionRef
		if err := rows.Scan(&testVersionID, &ref.MaterialID, &ref.VersionID); err != nil {
			return nil, fmt.Errorf("scan reading test passage: %w", err)
		}
		links[testVersionID] = append(links[testVersionID], ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reading test passages: %w", err)
	}
	return links, nil
}

func passageMaterials(
	versions map[uuid.UUID]Material,
	links map[uuid.UUID][]VersionRef,
	versionID uuid.UUID,
	depth int,
) []Material {
	refs := links[versionID]
	if len(refs) == 0 || depth >= maxPassageDepth {
		return nil
	}
	passages := make([]Material, 0, len(refs))
	for _, ref := range refs {
		passage, ok := versions[ref.VersionID]
		if !ok {
			continue
		}
		passage.Passages = passageMaterials(versions, links, ref.VersionID, depth+1)
		passages = append(passages, passage)
	}
	return passages
}
