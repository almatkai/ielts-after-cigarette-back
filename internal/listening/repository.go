package listening

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository interface {
	List(context.Context, bool) ([]Test, error)
	Get(context.Context, uuid.UUID, bool) (Test, error)
	Create(context.Context, uuid.UUID, SaveInput) (Test, error)
	Update(context.Context, uuid.UUID, uuid.UUID, SaveInput) (Test, error)
	Publish(context.Context, uuid.UUID, uuid.UUID, int64) (Test, error)
	CreateMedia(context.Context, uuid.UUID, Media) (Media, error)
	GetMedia(context.Context, uuid.UUID, bool) (Media, error)
}

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) List(ctx context.Context, published bool) ([]Test, error) {
	where := ""
	version := "t.current_version_id"
	if published {
		where = "WHERE t.status = 'PUBLISHED'"
		version = "t.published_version_id"
	}
	rows, err := r.pool.Query(ctx, `
		SELECT t.id, t.slug, t.exam_type, t.status, t.revision,
			v.title, v.description, v.duration_minutes, v.version_number,
			(t.published_version_id IS NULL OR t.current_version_id <> t.published_version_id), t.published_at, t.created_at, t.updated_at
		FROM listening_tests t JOIN listening_test_versions v ON v.id = `+version+`
		`+where+` ORDER BY t.updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list listening tests: %w", err)
	}
	defer rows.Close()
	items := []Test{}
	for rows.Next() {
		var item Test
		if err := rows.Scan(&item.ID, &item.Slug, &item.ExamType, &item.Status, &item.Revision,
			&item.Title, &item.Description, &item.DurationMinutes, &item.CurrentVersionNumber,
			&item.HasUnpublishedChanges, &item.PublishedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan listening test: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *PostgresRepository) Get(ctx context.Context, id uuid.UUID, published bool) (Test, error) {
	version := "t.current_version_id"
	statusFilter := ""
	if published {
		version = "t.published_version_id"
		statusFilter = " AND t.status = 'PUBLISHED'"
	}
	var item Test
	err := r.pool.QueryRow(ctx, `
		SELECT t.id, t.slug, t.exam_type, t.status, t.revision,
			v.title, v.description, v.duration_minutes, v.version_number,
			(t.published_version_id IS NULL OR t.current_version_id <> t.published_version_id), t.published_at, t.created_at, t.updated_at, v.id
		FROM listening_tests t JOIN listening_test_versions v ON v.id = `+version+`
		WHERE t.id = $1`+statusFilter, id).Scan(
		&item.ID, &item.Slug, &item.ExamType, &item.Status, &item.Revision,
		&item.Title, &item.Description, &item.DurationMinutes, &item.CurrentVersionNumber,
		&item.HasUnpublishedChanges, &item.PublishedAt, &item.CreatedAt, &item.UpdatedAt, &id,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Test{}, ErrNotFound
	}
	if err != nil {
		return Test{}, fmt.Errorf("get listening test: %w", err)
	}
	item.Parts, err = r.parts(ctx, id)
	return item, err
}

func (r *PostgresRepository) Create(ctx context.Context, actorID uuid.UUID, input SaveInput) (Test, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Test{}, err
	}
	defer tx.Rollback(ctx)
	testID, versionID := uuid.New(), uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO listening_tests
		(id, slug, exam_type, created_by, updated_by) VALUES ($1,$2,$3,$4,$4)`, testID, input.Slug, input.ExamType, actorID); err != nil {
		return Test{}, mapWriteError(err)
	}
	if err = insertVersion(ctx, tx, testID, versionID, 1, actorID, input); err != nil {
		return Test{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE listening_tests SET current_version_id=$2 WHERE id=$1`, testID, versionID); err != nil {
		return Test{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Test{}, err
	}
	return r.Get(ctx, testID, false)
}

func (r *PostgresRepository) Update(ctx context.Context, id, actorID uuid.UUID, input SaveInput) (Test, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Test{}, err
	}
	defer tx.Rollback(ctx)
	var revision int64
	var versionNumber int
	err = tx.QueryRow(ctx, `SELECT revision, v.version_number FROM listening_tests t
		JOIN listening_test_versions v ON v.id=t.current_version_id WHERE t.id=$1 FOR UPDATE`, id).Scan(&revision, &versionNumber)
	if errors.Is(err, pgx.ErrNoRows) {
		return Test{}, ErrNotFound
	}
	if err != nil {
		return Test{}, err
	}
	if revision != input.Revision {
		return Test{}, ErrRevisionConflict
	}
	versionID := uuid.New()
	if err = insertVersion(ctx, tx, id, versionID, versionNumber+1, actorID, input); err != nil {
		return Test{}, err
	}
	command, err := tx.Exec(ctx, `UPDATE listening_tests SET slug=$2, exam_type=$3,
		current_version_id=$4, revision=revision+1, updated_by=$5, updated_at=CURRENT_TIMESTAMP
		WHERE id=$1 AND revision=$6`, id, input.Slug, input.ExamType, versionID, actorID, input.Revision)
	if err != nil {
		return Test{}, mapWriteError(err)
	}
	if command.RowsAffected() != 1 {
		return Test{}, ErrRevisionConflict
	}
	if err = tx.Commit(ctx); err != nil {
		return Test{}, err
	}
	return r.Get(ctx, id, false)
}

func (r *PostgresRepository) Publish(ctx context.Context, id, actorID uuid.UUID, revision int64) (Test, error) {
	command, err := r.pool.Exec(ctx, `UPDATE listening_tests SET status='PUBLISHED',
		published_version_id=current_version_id, published_at=CURRENT_TIMESTAMP,
		revision=revision+1, updated_by=$3, updated_at=CURRENT_TIMESTAMP WHERE id=$1 AND revision=$2`, id, revision, actorID)
	if err != nil {
		return Test{}, err
	}
	if command.RowsAffected() != 1 {
		var exists bool
		if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM listening_tests WHERE id=$1)`, id).Scan(&exists); err != nil {
			return Test{}, err
		}
		if !exists {
			return Test{}, ErrNotFound
		}
		return Test{}, ErrRevisionConflict
	}
	return r.Get(ctx, id, false)
}

func insertVersion(ctx context.Context, tx pgx.Tx, testID, versionID uuid.UUID, number int, actorID uuid.UUID, input SaveInput) error {
	if _, err := tx.Exec(ctx, `INSERT INTO listening_test_versions
		(id,test_id,version_number,title,description,duration_minutes,created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, versionID, testID, number, input.Title, input.Description, input.DurationMinutes, actorID); err != nil {
		return fmt.Errorf("insert listening version: %w", err)
	}
	for partIndex, part := range input.Parts {
		partID := uuid.New()
		if _, err := tx.Exec(ctx, `INSERT INTO listening_parts
			(id,test_version_id,position,title,audio_asset_id) VALUES ($1,$2,$3,$4,$5)`,
			partID, versionID, partIndex+1, part.Title, part.AudioAssetID); err != nil {
			return fmt.Errorf("insert listening part: %w", err)
		}
		for groupIndex, group := range part.Groups {
			groupID := uuid.New()
			config, _ := json.Marshal(group.Config)
			if _, err := tx.Exec(ctx, `INSERT INTO listening_question_groups
				(id,part_id,position,question_type,instructions,context,config,image_asset_id)
				VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8)`, groupID, partID, groupIndex+1,
				group.Type, group.Instructions, group.Context, config, group.ImageAssetID); err != nil {
				return fmt.Errorf("insert listening group: %w", err)
			}
			for questionIndex, question := range group.Questions {
				content, _ := json.Marshal(question.Content)
				answer, _ := json.Marshal(question.Answer)
				if _, err := tx.Exec(ctx, `INSERT INTO listening_questions
					(id,group_id,position,number,prompt,content,answer,explanation,points)
					VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7::jsonb,$8,$9)`, uuid.New(), groupID,
					questionIndex+1, question.Number, question.Prompt, content, answer, question.Explanation, question.Points); err != nil {
					return fmt.Errorf("insert listening question: %w", err)
				}
			}
		}
	}
	return nil
}

func (r *PostgresRepository) parts(ctx context.Context, versionID uuid.UUID) ([]Part, error) {
	rows, err := r.pool.Query(ctx, `SELECT p.id,p.position,p.title,p.audio_asset_id,
		g.id,g.position,g.question_type,g.instructions,g.context,g.config,g.image_asset_id,
		q.id,q.position,q.number,q.prompt,q.content,q.answer,q.explanation,q.points
		FROM listening_parts p
		LEFT JOIN listening_question_groups g ON g.part_id=p.id
		LEFT JOIN listening_questions q ON q.group_id=g.id
		WHERE p.test_version_id=$1 ORDER BY p.position,g.position,q.position`, versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	parts := []Part{}
	partIndex, groupIndex := map[uuid.UUID]int{}, map[uuid.UUID][2]int{}
	for rows.Next() {
		var part Part
		var groupID, questionID *uuid.UUID
		var groupPosition, questionPosition, questionNumber, points *int
		var groupType, instructions, contextText, prompt, explanation *string
		var config, content, answer []byte
		var imageID *uuid.UUID
		if err := rows.Scan(&part.ID, &part.Position, &part.Title, &part.AudioAssetID,
			&groupID, &groupPosition, &groupType, &instructions, &contextText, &config, &imageID,
			&questionID, &questionPosition, &questionNumber, &prompt, &content, &answer, &explanation, &points); err != nil {
			return nil, err
		}
		pi, ok := partIndex[part.ID]
		if !ok {
			pi = len(parts)
			partIndex[part.ID] = pi
			part.Groups = []QuestionGroup{}
			parts = append(parts, part)
		}
		if groupID == nil {
			continue
		}
		key, ok := groupIndex[*groupID]
		if !ok {
			var cfg map[string]any
			_ = json.Unmarshal(config, &cfg)
			group := QuestionGroup{ID: *groupID, Position: *groupPosition, Type: *groupType, Instructions: *instructions, Context: *contextText, Config: cfg, ImageAssetID: imageID, Questions: []Question{}}
			parts[pi].Groups = append(parts[pi].Groups, group)
			key = [2]int{pi, len(parts[pi].Groups) - 1}
			groupIndex[*groupID] = key
		}
		if questionID != nil {
			var c, a map[string]any
			_ = json.Unmarshal(content, &c)
			_ = json.Unmarshal(answer, &a)
			parts[key[0]].Groups[key[1]].Questions = append(parts[key[0]].Groups[key[1]].Questions, Question{ID: *questionID, Position: *questionPosition, Number: *questionNumber, Prompt: *prompt, Content: c, Answer: a, Explanation: *explanation, Points: *points})
		}
	}
	return parts, rows.Err()
}

func (r *PostgresRepository) CreateMedia(ctx context.Context, actorID uuid.UUID, media Media) (Media, error) {
	media.ID = uuid.New()
	err := r.pool.QueryRow(ctx, `INSERT INTO listening_media
		(id,kind,original_name,mime_type,storage_key,byte_size,created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING created_at`, media.ID, media.Kind,
		media.OriginalName, media.MimeType, media.StorageKey, media.ByteSize, actorID).Scan(&media.CreatedAt)
	return media, err
}

func (r *PostgresRepository) GetMedia(ctx context.Context, id uuid.UUID, publishedOnly bool) (Media, error) {
	var media Media
	err := r.pool.QueryRow(ctx, `SELECT m.id,m.kind,m.original_name,m.mime_type,m.storage_key,m.byte_size,m.created_at
		FROM listening_media m WHERE m.id=$1 AND (NOT $2 OR EXISTS (
			SELECT 1 FROM listening_tests t
			JOIN listening_parts p ON p.test_version_id=t.published_version_id
			LEFT JOIN listening_question_groups g ON g.part_id=p.id
			WHERE t.status='PUBLISHED' AND (p.audio_asset_id=m.id OR g.image_asset_id=m.id)
		))`, id, publishedOnly).Scan(&media.ID, &media.Kind, &media.OriginalName, &media.MimeType, &media.StorageKey, &media.ByteSize, &media.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Media{}, ErrMediaNotFound
	}
	return media, err
}

func mapWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "listening_tests_slug_key" {
		return ErrSlugExists
	}
	return err
}
