package reading

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const materialSelect = `
	SELECT
		m.id, m.slug, m.exam_type, m.difficulty, m.status, m.material_kind, m.revision,
		v.title, v.description, v.body, v.duration_minutes, v.source_title, v.source_url,
		v.version_number, m.current_version_id, m.published_version_id,
		(m.published_version_id IS DISTINCT FROM m.current_version_id),
		m.published_at, m.created_at, m.updated_at
	FROM reading_materials m
	JOIN reading_material_versions v ON v.id = m.current_version_id
`

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) List(ctx context.Context) ([]Material, error) {
	rows, err := r.pool.Query(ctx, materialSelect+`
		WHERE NOT EXISTS (
			SELECT 1 FROM reading_test_passages tp WHERE tp.passage_material_id = m.id
		)
		ORDER BY m.updated_at DESC, m.id`)
	if err != nil {
		return nil, fmt.Errorf("list reading materials: %w", err)
	}
	defer rows.Close()

	materials := []Material{}
	for rows.Next() {
		material, err := scanMaterial(rows)
		if err != nil {
			return nil, fmt.Errorf("scan reading material: %w", err)
		}
		materials = append(materials, material)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reading materials: %w", err)
	}
	return materials, nil
}

func (r *PostgresRepository) Get(ctx context.Context, id uuid.UUID) (Material, error) {
	material, err := scanMaterial(r.pool.QueryRow(ctx, materialSelect+` WHERE m.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Material{}, ErrNotFound
	}
	if err != nil {
		return Material{}, fmt.Errorf("get reading material: %w", err)
	}
	material.QuestionGroups, err = r.questionGroups(ctx, material.ID, material.CurrentVersionNumber)
	if err != nil {
		return Material{}, err
	}
	material.Passages, err = r.testPassages(ctx, material.CurrentVersionID)
	return material, err
}

// ListPublished returns published materials without the passage body and
// questions, joined to the published version for the title.
func (r *PostgresRepository) ListPublished(ctx context.Context) ([]MaterialSummary, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT m.id, m.slug, m.exam_type, m.difficulty, m.material_kind,
			v.title, v.description, m.published_at, v.duration_minutes
		FROM reading_materials m
		JOIN reading_material_versions v ON v.id = m.published_version_id
		WHERE m.status = 'PUBLISHED'
		  AND NOT EXISTS (
			SELECT 1 FROM reading_test_passages tp WHERE tp.passage_material_id = m.id
		  )
		ORDER BY m.published_at DESC, m.id`)
	if err != nil {
		return nil, fmt.Errorf("list published reading materials: %w", err)
	}
	defer rows.Close()
	items := []MaterialSummary{}
	for rows.Next() {
		var item MaterialSummary
		if err := rows.Scan(&item.ID, &item.Slug, &item.ExamType, &item.Difficulty, &item.Kind,
			&item.Title, &item.Description, &item.PublishedAt, &item.DurationMinutes); err != nil {
			return nil, fmt.Errorf("scan published reading material: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate published reading materials: %w", err)
	}
	return items, nil
}

// GetPublished returns a published material with the structure of its
// published version.
func (r *PostgresRepository) GetPublished(ctx context.Context, id uuid.UUID) (Material, error) {
	query := strings.Replace(materialSelect,
		"v.id = m.current_version_id", "v.id = m.published_version_id", 1) +
		` WHERE m.id = $1 AND m.status = 'PUBLISHED'`
	material, err := scanMaterial(r.pool.QueryRow(ctx, query, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Material{}, ErrNotFound
	}
	if err != nil {
		return Material{}, fmt.Errorf("get published reading material: %w", err)
	}
	material.QuestionGroups, err = r.questionGroups(ctx, material.ID, material.CurrentVersionNumber)
	if err != nil {
		return Material{}, err
	}
	material.Passages, err = r.testPassages(ctx, *material.PublishedVersionID)
	return material, err
}

// GetVersion returns the material with the structure of a specific version,
// regardless of its status. Used by attempts grading, which is pinned to the
// version the attempt was started on.
func (r *PostgresRepository) GetVersion(ctx context.Context, id, versionID uuid.UUID) (Material, error) {
	query := strings.Replace(materialSelect,
		"v.id = m.current_version_id", "v.id = $2 AND v.material_id = m.id", 1) +
		` WHERE m.id = $1`
	material, err := scanMaterial(r.pool.QueryRow(ctx, query, id, versionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Material{}, ErrNotFound
	}
	if err != nil {
		return Material{}, fmt.Errorf("get reading material version: %w", err)
	}
	// CurrentVersionNumber here holds the number of the joined version, so
	// questionGroups loads exactly that version's structure.
	material.QuestionGroups, err = r.questionGroups(ctx, material.ID, material.CurrentVersionNumber)
	if err != nil {
		return Material{}, err
	}
	material.Passages, err = r.testPassages(ctx, versionID)
	return material, err
}

// PublishedVersionID returns the published version of a PUBLISHED material.
func (r *PostgresRepository) PublishedVersionID(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	var versionID *uuid.UUID
	err := r.pool.QueryRow(ctx, `SELECT published_version_id FROM reading_materials
		WHERE id=$1 AND status='PUBLISHED'`, id).Scan(&versionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("get published reading version: %w", err)
	}
	if versionID == nil {
		return uuid.Nil, ErrNotFound
	}
	return *versionID, nil
}

func (r *PostgresRepository) Create(ctx context.Context, actorID uuid.UUID, input SaveInput) (Material, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Material{}, fmt.Errorf("begin reading material create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	materialID, err := createMaterialInTx(ctx, tx, actorID, input)
	if err != nil {
		return Material{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Material{}, fmt.Errorf("commit reading material create: %w", err)
	}
	return r.Get(ctx, materialID)
}

func (r *PostgresRepository) CreateMany(ctx context.Context, actorID uuid.UUID, inputs []SaveInput) ([]Material, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin reading material bulk create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	ids := make([]uuid.UUID, 0, len(inputs))
	versionIDs := make([]uuid.UUID, 0, len(inputs))
	for _, input := range inputs {
		id, err := createMaterialInTx(ctx, tx, actorID, input)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
		var versionID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT current_version_id FROM reading_materials WHERE id=$1`, id).Scan(&versionID); err != nil {
			return nil, fmt.Errorf("read created reading version: %w", err)
		}
		versionIDs = append(versionIDs, versionID)
	}
	if len(inputs) > 1 && inputs[0].Kind == KindTest {
		for index := 1; index < len(inputs); index++ {
			if _, err := tx.Exec(ctx, `
				INSERT INTO reading_test_passages (
					test_material_version_id, position, passage_material_id, passage_material_version_id
				) VALUES ($1, $2, $3, $4)
			`, versionIDs[0], index, ids[index], versionIDs[index]); err != nil {
				return nil, fmt.Errorf("link reading test passage: %w", err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit reading material bulk create: %w", err)
	}
	items := make([]Material, 0, len(ids))
	for _, id := range ids {
		material, err := r.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		items = append(items, material)
	}
	return items, nil
}

func createMaterialInTx(ctx context.Context, tx pgx.Tx, actorID uuid.UUID, input SaveInput) (uuid.UUID, error) {
	materialID := uuid.New()
	versionID := uuid.New()
	_, err := tx.Exec(ctx, `
		INSERT INTO reading_materials (
			id, slug, exam_type, difficulty, status, revision, created_by, updated_by
		) VALUES ($1, $2, $3, $4, $5, 1, $6, $6)
	`, materialID, input.Slug, input.ExamType, input.Difficulty, StatusDraft, actorID)
	if err != nil {
		return uuid.Nil, mapWriteError(err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO reading_material_versions (
			id, material_id, version_number, title, description, body,
			duration_minutes, source_title, source_url, created_by
		) VALUES ($1, $2, 1, $3, $4, $5, $6, $7, $8, $9)
	`, versionID, materialID, input.Title, input.Description, input.Body, input.DurationMinutes, input.SourceTitle, input.SourceURL, actorID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("insert reading material version: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE reading_materials SET current_version_id = $2, material_kind = $3 WHERE id = $1
	`, materialID, versionID, input.Kind); err != nil {
		return uuid.Nil, fmt.Errorf("link current reading material version: %w", err)
	}
	if err := insertQuestionGroups(ctx, tx, versionID, input.QuestionGroups, actorID); err != nil {
		return uuid.Nil, err
	}
	return materialID, nil
}

func (r *PostgresRepository) Update(ctx context.Context, id, actorID uuid.UUID, input SaveInput) (Material, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Material{}, fmt.Errorf("begin reading material update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var revision int64
	var nextVersion int
	err = tx.QueryRow(ctx, `
		SELECT revision,
			(SELECT COALESCE(MAX(version_number), 0) + 1 FROM reading_material_versions WHERE material_id = $1)
		FROM reading_materials
		WHERE id = $1
		FOR UPDATE
	`, id).Scan(&revision, &nextVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Material{}, ErrNotFound
	}
	if err != nil {
		return Material{}, fmt.Errorf("lock reading material: %w", err)
	}
	if revision != input.Revision {
		return Material{}, ErrRevisionConflict
	}

	versionID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO reading_material_versions (
			id, material_id, version_number, title, description, body,
			duration_minutes, source_title, source_url, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, versionID, id, nextVersion, input.Title, input.Description, input.Body, input.DurationMinutes, input.SourceTitle, input.SourceURL, actorID)
	if err != nil {
		return Material{}, fmt.Errorf("insert reading material version: %w", err)
	}
	if err := insertQuestionGroups(ctx, tx, versionID, input.QuestionGroups, actorID); err != nil {
		return Material{}, err
	}
	if input.Kind == KindTest {
		if _, err := tx.Exec(ctx, `
			INSERT INTO reading_test_passages (
				test_material_version_id, position, passage_material_id, passage_material_version_id
			)
			SELECT $1, position, passage_material_id, passage_material_version_id
			FROM reading_test_passages
			WHERE test_material_version_id = (
				SELECT current_version_id FROM reading_materials WHERE id = $2
			)
		`, versionID, id); err != nil {
			return Material{}, fmt.Errorf("copy reading test passages: %w", err)
		}
	}
	_, err = tx.Exec(ctx, `
		UPDATE reading_materials
		SET slug = $2, exam_type = $3, difficulty = $4, material_kind = $5,
			current_version_id = $6, updated_by = $7, revision = revision + 1
		WHERE id = $1
	`, id, input.Slug, input.ExamType, input.Difficulty, input.Kind, versionID, actorID)
	if err != nil {
		return Material{}, mapWriteError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Material{}, fmt.Errorf("commit reading material update: %w", err)
	}
	return r.Get(ctx, id)
}

func (r *PostgresRepository) Publish(ctx context.Context, id, actorID uuid.UUID, expectedRevision int64) (Material, error) {
	command, err := r.pool.Exec(ctx, `
		UPDATE reading_materials
		SET published_version_id = current_version_id,
			status = $4, published_by = $2, published_at = $3,
			updated_by = $2, revision = revision + 1
		WHERE id = $1 AND revision = $5
	`, id, actorID, time.Now().UTC(), StatusPublished, expectedRevision)
	if err != nil {
		return Material{}, fmt.Errorf("publish reading material: %w", err)
	}
	if command.RowsAffected() == 0 {
		var exists bool
		if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM reading_materials WHERE id = $1)`, id).Scan(&exists); err != nil {
			return Material{}, fmt.Errorf("check reading material after publish conflict: %w", err)
		}
		if !exists {
			return Material{}, ErrNotFound
		}
		return Material{}, ErrRevisionConflict
	}
	return r.Get(ctx, id)
}

func (r *PostgresRepository) Archive(ctx context.Context, id, actorID uuid.UUID, expectedRevision int64) (Material, error) {
	command, err := r.pool.Exec(ctx, `
		UPDATE reading_materials
		SET status = $3, updated_by = $2, revision = revision + 1
		WHERE id = $1 AND revision = $4
	`, id, actorID, StatusArchived, expectedRevision)
	if err != nil {
		return Material{}, fmt.Errorf("archive reading material: %w", err)
	}
	if command.RowsAffected() == 0 {
		if _, err := r.Get(ctx, id); errors.Is(err, ErrNotFound) {
			return Material{}, ErrNotFound
		}
		return Material{}, ErrRevisionConflict
	}
	return r.Get(ctx, id)
}

type rowScanner interface {
	Scan(...any) error
}

func scanMaterial(row rowScanner) (Material, error) {
	var material Material
	err := row.Scan(
		&material.ID,
		&material.Slug,
		&material.ExamType,
		&material.Difficulty,
		&material.Status,
		&material.Kind,
		&material.Revision,
		&material.Title,
		&material.Description,
		&material.Body,
		&material.DurationMinutes,
		&material.SourceTitle,
		&material.SourceURL,
		&material.CurrentVersionNumber,
		&material.CurrentVersionID,
		&material.PublishedVersionID,
		&material.HasUnpublishedChanges,
		&material.PublishedAt,
		&material.CreatedAt,
		&material.UpdatedAt,
	)
	return material, err
}

func insertQuestionGroups(ctx context.Context, tx pgx.Tx, versionID uuid.UUID, groups []QuestionGroup, actorID uuid.UUID) error {
	for groupIndex, group := range groups {
		groupID := uuid.New()
		position := group.Position
		if position < 1 {
			position = groupIndex + 1
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO reading_question_groups (id, material_version_id, position, question_type, instructions, created_by)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, groupID, versionID, position, group.Type, group.Instructions, actorID); err != nil {
			return fmt.Errorf("insert reading question group: %w", err)
		}
		for questionIndex, question := range group.Questions {
			content, err := json.Marshal(question.Content)
			if err != nil {
				return fmt.Errorf("marshal question content: %w", err)
			}
			answer, err := json.Marshal(question.Answer)
			if err != nil {
				return fmt.Errorf("marshal question answer: %w", err)
			}
			qPosition := question.Position
			if qPosition < 1 {
				qPosition = questionIndex + 1
			}
			points := question.Points
			if points < 1 {
				points = 1
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO reading_questions (id, group_id, position, prompt, content, answer, explanation, points, created_by)
				VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb, $7, $8, $9)
			`, uuid.New(), groupID, qPosition, question.Prompt, content, answer, question.Explanation, points, actorID); err != nil {
				return fmt.Errorf("insert reading question: %w", err)
			}
		}
	}
	return nil
}

func (r *PostgresRepository) questionGroups(ctx context.Context, materialID uuid.UUID, versionNumber int) ([]QuestionGroup, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT g.id, g.position, g.question_type, g.instructions,
			q.id, q.position, q.prompt, q.content, q.answer, q.explanation, q.points
		FROM reading_question_groups g
		JOIN reading_material_versions v ON v.id = g.material_version_id
		LEFT JOIN reading_questions q ON q.group_id = g.id
		WHERE v.material_id = $1 AND v.version_number = $2
		ORDER BY g.position, q.position
	`, materialID, versionNumber)
	if err != nil {
		return nil, fmt.Errorf("list reading question groups: %w", err)
	}
	defer rows.Close()
	groups := []QuestionGroup{}
	byID := map[uuid.UUID]int{}
	for rows.Next() {
		var group QuestionGroup
		var position *int
		var qID *uuid.UUID
		var prompt, explanation *string
		var content, answer []byte
		var points *int
		if err := rows.Scan(&group.ID, &group.Position, &group.Type, &group.Instructions, &qID, &position, &prompt, &content, &answer, &explanation, &points); err != nil {
			return nil, fmt.Errorf("scan reading question group: %w", err)
		}
		if index, ok := byID[group.ID]; ok {
			group = groups[index]
		} else {
			byID[group.ID] = len(groups)
			groups = append(groups, group)
			groups[len(groups)-1].Questions = []Question{}
		}
		if qID != nil {
			var qContent, qAnswer map[string]any
			if err := json.Unmarshal(content, &qContent); err != nil {
				return nil, fmt.Errorf("decode question content: %w", err)
			}
			if err := json.Unmarshal(answer, &qAnswer); err != nil {
				return nil, fmt.Errorf("decode question answer: %w", err)
			}
			groups[byID[group.ID]].Questions = append(groups[byID[group.ID]].Questions, Question{ID: *qID, Position: *position, Prompt: *prompt, Content: qContent, Answer: qAnswer, Explanation: *explanation, Points: *points})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reading question groups: %w", err)
	}
	return groups, nil
}

func (r *PostgresRepository) testPassages(ctx context.Context, testVersionID uuid.UUID) ([]Material, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT passage_material_id, passage_material_version_id
		FROM reading_test_passages
		WHERE test_material_version_id = $1
		ORDER BY position
	`, testVersionID)
	if err != nil {
		return nil, fmt.Errorf("list reading test passages: %w", err)
	}
	type passageRef struct{ materialID, versionID uuid.UUID }
	refs := []passageRef{}
	for rows.Next() {
		var ref passageRef
		if err := rows.Scan(&ref.materialID, &ref.versionID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan reading test passage: %w", err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate reading test passages: %w", err)
	}
	rows.Close()
	passages := make([]Material, 0, len(refs))
	for _, ref := range refs {
		passage, err := r.GetVersion(ctx, ref.materialID, ref.versionID)
		if err != nil {
			return nil, err
		}
		passages = append(passages, passage)
	}
	return passages, nil
}

func mapWriteError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "reading_materials_slug_key" {
		return ErrSlugExists
	}
	return err
}
