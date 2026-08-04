package reading

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const materialSelect = `
	SELECT
		m.id, m.slug, m.exam_type, m.difficulty, m.status, m.revision,
		v.title, v.description, v.body, v.source_title, v.source_url,
		v.version_number, m.published_version_id,
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
	rows, err := r.pool.Query(ctx, materialSelect+` ORDER BY m.updated_at DESC, m.id`)
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
	return material, nil
}

func (r *PostgresRepository) Create(ctx context.Context, actorID uuid.UUID, input SaveInput) (Material, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Material{}, fmt.Errorf("begin reading material create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	materialID := uuid.New()
	versionID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO reading_materials (
			id, slug, exam_type, difficulty, status, revision, created_by, updated_by
		) VALUES ($1, $2, $3, $4, $5, 1, $6, $6)
	`, materialID, input.Slug, input.ExamType, input.Difficulty, StatusDraft, actorID)
	if err != nil {
		return Material{}, mapWriteError(err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO reading_material_versions (
			id, material_id, version_number, title, description, body,
			source_title, source_url, created_by
		) VALUES ($1, $2, 1, $3, $4, $5, $6, $7, $8)
	`, versionID, materialID, input.Title, input.Description, input.Body, input.SourceTitle, input.SourceURL, actorID)
	if err != nil {
		return Material{}, fmt.Errorf("insert reading material version: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE reading_materials SET current_version_id = $2 WHERE id = $1
	`, materialID, versionID); err != nil {
		return Material{}, fmt.Errorf("link current reading material version: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Material{}, fmt.Errorf("commit reading material create: %w", err)
	}
	return r.Get(ctx, materialID)
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
			source_title, source_url, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, versionID, id, nextVersion, input.Title, input.Description, input.Body, input.SourceTitle, input.SourceURL, actorID)
	if err != nil {
		return Material{}, fmt.Errorf("insert reading material version: %w", err)
	}
	_, err = tx.Exec(ctx, `
		UPDATE reading_materials
		SET slug = $2, exam_type = $3, difficulty = $4,
			current_version_id = $5, updated_by = $6, revision = revision + 1
		WHERE id = $1
	`, id, input.Slug, input.ExamType, input.Difficulty, versionID, actorID)
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
		&material.Revision,
		&material.Title,
		&material.Description,
		&material.Body,
		&material.SourceTitle,
		&material.SourceURL,
		&material.CurrentVersionNumber,
		&material.PublishedVersionID,
		&material.HasUnpublishedChanges,
		&material.PublishedAt,
		&material.CreatedAt,
		&material.UpdatedAt,
	)
	return material, err
}

func mapWriteError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "reading_materials_slug_key" {
		return ErrSlugExists
	}
	return err
}
