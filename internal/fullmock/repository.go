package fullmock

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const testColumns = `id, slug, status, revision, exam_type, title, description,
	duration_minutes, listening_material_id, reading_material_id, writing_material_id,
	speaking_material_id, published_at, created_at, updated_at`

type Repository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func (r *Repository) ListPublic(ctx context.Context) ([]Test, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+testColumns+` FROM full_mock_tests
		WHERE status=$1 ORDER BY published_at DESC, title`, StatusPublished)
	if err != nil {
		return nil, fmt.Errorf("list full mocks: %w", err)
	}
	defer rows.Close()
	items := []Test{}
	for rows.Next() {
		item, err := scanTest(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) List(ctx context.Context) ([]Test, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+testColumns+` FROM full_mock_tests ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list full mocks: %w", err)
	}
	defer rows.Close()
	items := []Test{}
	for rows.Next() {
		item, err := scanTest(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) Get(ctx context.Context, id uuid.UUID) (Test, error) {
	item, err := scanTest(r.pool.QueryRow(ctx, `SELECT `+testColumns+` FROM full_mock_tests WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Test{}, ErrNotFound
	}
	if err != nil {
		return Test{}, fmt.Errorf("get full mock: %w", err)
	}
	return item, nil
}

func (r *Repository) GetPublic(ctx context.Context, id uuid.UUID) (Test, error) {
	item, err := scanTest(r.pool.QueryRow(ctx, `SELECT `+testColumns+` FROM full_mock_tests WHERE id=$1 AND status=$2`, id, StatusPublished))
	if errors.Is(err, pgx.ErrNoRows) {
		return Test{}, ErrNotFound
	}
	if err != nil {
		return Test{}, fmt.Errorf("get full mock: %w", err)
	}
	return item, nil
}

func (r *Repository) Create(ctx context.Context, test Test) (Test, error) {
	item, err := scanTest(r.pool.QueryRow(ctx, `INSERT INTO full_mock_tests (
		id, slug, exam_type, title, description, duration_minutes, listening_material_id,
		reading_material_id, writing_material_id, speaking_material_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING `+testColumns,
		test.ID, test.Slug, test.ExamType, test.Title, test.Description, test.DurationMinutes,
		test.ListeningMaterialID, test.ReadingMaterialID, test.WritingMaterialID, test.SpeakingMaterialID))
	if err != nil {
		return Test{}, mapWriteError(err)
	}
	return item, nil
}

func (r *Repository) Update(ctx context.Context, id uuid.UUID, input SaveInput) (Test, error) {
	item, err := scanTest(r.pool.QueryRow(ctx, `UPDATE full_mock_tests SET slug=$2, exam_type=$3,
		title=$4, description=$5, duration_minutes=$6, listening_material_id=$7,
		reading_material_id=$8, writing_material_id=$9, speaking_material_id=$10,
		revision=revision+1, updated_at=CURRENT_TIMESTAMP WHERE id=$1 AND revision=$11
		RETURNING `+testColumns, id, input.Slug, input.ExamType, input.Title, input.Description,
		input.DurationMinutes, input.ListeningMaterialID, input.ReadingMaterialID,
		input.WritingMaterialID, input.SpeakingMaterialID, input.Revision))
	if errors.Is(err, pgx.ErrNoRows) {
		if _, getErr := r.Get(ctx, id); errors.Is(getErr, ErrNotFound) {
			return Test{}, ErrNotFound
		}
		return Test{}, ErrRevisionConflict
	}
	if err != nil {
		return Test{}, mapWriteError(err)
	}
	return item, nil
}

func (r *Repository) Publish(ctx context.Context, id uuid.UUID, revision int64) (Test, error) {
	item, err := scanTest(r.pool.QueryRow(ctx, `UPDATE full_mock_tests SET status=$2, published_at=CURRENT_TIMESTAMP,
		revision=revision+1, updated_at=CURRENT_TIMESTAMP WHERE id=$1 AND revision=$3
		RETURNING `+testColumns, id, StatusPublished, revision))
	if errors.Is(err, pgx.ErrNoRows) {
		if _, getErr := r.Get(ctx, id); errors.Is(getErr, ErrNotFound) {
			return Test{}, ErrNotFound
		}
		return Test{}, ErrRevisionConflict
	}
	if err != nil {
		return Test{}, fmt.Errorf("publish full mock: %w", err)
	}
	return item, nil
}

func (r *Repository) FindActiveSession(ctx context.Context, userID, testID uuid.UUID) (Session, error) {
	return r.getSession(ctx, `SELECT id, mock_test_id, user_id, status, current_section, started_at, submitted_at
		FROM full_mock_sessions WHERE user_id=$1 AND mock_test_id=$2 AND status=$3`, userID, testID, SessionInProgress)
}

func (r *Repository) GetSession(ctx context.Context, id uuid.UUID) (Session, error) {
	return r.getSession(ctx, `SELECT id, mock_test_id, user_id, status, current_section, started_at, submitted_at
		FROM full_mock_sessions WHERE id=$1`, id)
}

func (r *Repository) getSession(ctx context.Context, query string, args ...any) (Session, error) {
	var session Session
	err := r.pool.QueryRow(ctx, query, args...).Scan(&session.ID, &session.MockTestID, &session.UserID, &session.Status,
		&session.CurrentSection, &session.StartedAt, &session.SubmittedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrSessionNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("get full mock session: %w", err)
	}
	return session, nil
}

func (r *Repository) CreateSession(ctx context.Context, session Session, sections []SessionSection) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO full_mock_sessions (id, mock_test_id, user_id) VALUES ($1,$2,$3)`,
		session.ID, session.MockTestID, session.UserID)
	if err != nil {
		return mapWriteError(err)
	}
	for _, section := range sections {
		_, err = tx.Exec(ctx, `INSERT INTO full_mock_session_sections (session_id, position, skill, attempt_id)
			VALUES ($1,$2,$3,$4)`, session.ID, section.Position, section.Skill, section.Attempt.ID)
		if err != nil {
			return fmt.Errorf("add full mock section: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func (r *Repository) ListSessionSections(ctx context.Context, sessionID uuid.UUID) ([]SessionSection, error) {
	rows, err := r.pool.Query(ctx, `SELECT s.position, s.skill, a.id, a.user_id, a.material_type, a.material_id,
		a.material_version_id, a.status, a.score, a.max_score, a.band::double precision, a.started_at, a.submitted_at
		FROM full_mock_session_sections s JOIN attempts a ON a.id=s.attempt_id
		WHERE s.session_id=$1 ORDER BY s.position`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list full mock sections: %w", err)
	}
	defer rows.Close()
	sections := []SessionSection{}
	for rows.Next() {
		section := SessionSection{}
		if err := rows.Scan(&section.Position, &section.Skill, &section.Attempt.ID, &section.Attempt.UserID,
			&section.Attempt.MaterialType, &section.Attempt.MaterialID, &section.Attempt.MaterialVersionID,
			&section.Attempt.Status, &section.Attempt.Score, &section.Attempt.MaxScore, &section.Attempt.Band,
			&section.Attempt.StartedAt, &section.Attempt.SubmittedAt); err != nil {
			return nil, err
		}
		sections = append(sections, section)
	}
	return sections, rows.Err()
}

func (r *Repository) Advance(ctx context.Context, id uuid.UUID, nextSection int, complete bool) error {
	if complete {
		result, err := r.pool.Exec(ctx, `UPDATE full_mock_sessions SET current_section=5, status=$2,
			submitted_at=CURRENT_TIMESTAMP WHERE id=$1 AND status=$3`, id, SessionSubmitted, SessionInProgress)
		if err != nil {
			return fmt.Errorf("complete full mock: %w", err)
		}
		if result.RowsAffected() == 0 {
			return ErrSessionCompleted
		}
		return nil
	}
	result, err := r.pool.Exec(ctx, `UPDATE full_mock_sessions SET current_section=$2 WHERE id=$1 AND status=$3`, id, nextSection, SessionInProgress)
	if err != nil {
		return fmt.Errorf("advance full mock: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrSessionCompleted
	}
	return nil
}

type rowScanner interface{ Scan(...any) error }

func scanTest(row rowScanner) (Test, error) {
	var item Test
	err := row.Scan(&item.ID, &item.Slug, &item.Status, &item.Revision, &item.ExamType, &item.Title,
		&item.Description, &item.DurationMinutes, &item.ListeningMaterialID, &item.ReadingMaterialID,
		&item.WritingMaterialID, &item.SpeakingMaterialID, &item.PublishedAt, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func mapWriteError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "full_mock_tests_slug_key") {
		return ErrSlugExists
	}
	return fmt.Errorf("save full mock: %w", err)
}
