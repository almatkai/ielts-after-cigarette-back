package speaking

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

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

const materialColumns = `m.id, m.slug, v.exam_type, v.difficulty, m.status, m.revision,
	v.title, v.description, v.parts, m.current_version_number, m.published_version_id,
	(m.published_version_id IS DISTINCT FROM v.id), m.published_at, m.created_at, m.updated_at`

func (r *PostgresRepository) List(ctx context.Context) ([]Material, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+materialColumns+`
		FROM speaking_materials m
		JOIN speaking_material_versions v ON v.material_id=m.id AND v.version_number=m.current_version_number
		ORDER BY m.updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list speaking materials: %w", err)
	}
	defer rows.Close()
	items := []Material{}
	for rows.Next() {
		material, err := scanMaterial(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, material)
	}
	return items, rows.Err()
}

func (r *PostgresRepository) Get(ctx context.Context, id uuid.UUID) (Material, error) {
	material, err := scanMaterial(r.pool.QueryRow(ctx, `SELECT `+materialColumns+`
		FROM speaking_materials m
		JOIN speaking_material_versions v ON v.material_id=m.id AND v.version_number=m.current_version_number
		WHERE m.id=$1`, id))
	return mapReadError(material, err, "get speaking material")
}

func (r *PostgresRepository) ListPublished(ctx context.Context) ([]MaterialSummary, error) {
	rows, err := r.pool.Query(ctx, `SELECT m.id, m.slug, v.exam_type, v.difficulty,
		v.title, v.description, m.published_at
		FROM speaking_materials m
		JOIN speaking_material_versions v ON v.id=m.published_version_id
		WHERE m.status='PUBLISHED'
		ORDER BY m.published_at DESC, m.updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list published speaking materials: %w", err)
	}
	defer rows.Close()
	items := []MaterialSummary{}
	for rows.Next() {
		var item MaterialSummary
		if err := rows.Scan(&item.ID, &item.Slug, &item.ExamType, &item.Difficulty,
			&item.Title, &item.Description, &item.PublishedAt); err != nil {
			return nil, fmt.Errorf("scan speaking material summary: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *PostgresRepository) GetPublished(ctx context.Context, id uuid.UUID) (Material, error) {
	material, err := scanMaterial(r.pool.QueryRow(ctx, `SELECT `+materialColumns+`
		FROM speaking_materials m
		JOIN speaking_material_versions v ON v.id=m.published_version_id
		WHERE m.id=$1 AND m.status='PUBLISHED'`, id))
	return mapReadError(material, err, "get published speaking material")
}

func (r *PostgresRepository) GetVersion(ctx context.Context, id, versionID uuid.UUID) (Material, error) {
	material, err := scanMaterial(r.pool.QueryRow(ctx, `SELECT `+materialColumns+`
		FROM speaking_materials m
		JOIN speaking_material_versions v ON v.id=$2 AND v.material_id=m.id
		WHERE m.id=$1`, id, versionID))
	return mapReadError(material, err, "get speaking material version")
}

func (r *PostgresRepository) PublishedVersionID(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	var versionID uuid.UUID
	err := r.pool.QueryRow(ctx, `SELECT published_version_id FROM speaking_materials
		WHERE id=$1 AND status='PUBLISHED' AND published_version_id IS NOT NULL`, id).Scan(&versionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("get published speaking version: %w", err)
	}
	return versionID, nil
}

func (r *PostgresRepository) Create(ctx context.Context, actorID uuid.UUID, input SaveInput) (Material, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Material{}, err
	}
	defer tx.Rollback(ctx)
	id, err := createMaterialInTx(ctx, tx, actorID, input)
	if err != nil {
		return Material{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Material{}, fmt.Errorf("commit speaking material: %w", err)
	}
	return r.Get(ctx, id)
}

func createMaterialInTx(ctx context.Context, tx pgx.Tx, actorID uuid.UUID, input SaveInput) (uuid.UUID, error) {
	materialID, versionID := uuid.New(), uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO speaking_materials
		(id, slug, status, revision, current_version_number)
		VALUES ($1,$2,'DRAFT',1,1)`, materialID, input.Slug); err != nil {
		return uuid.Nil, mapWriteError(err)
	}
	if err := insertVersion(ctx, tx, versionID, materialID, 1, actorID, input); err != nil {
		return uuid.Nil, err
	}
	return materialID, nil
}

func (r *PostgresRepository) Update(ctx context.Context, id, actorID uuid.UUID, input SaveInput) (Material, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Material{}, err
	}
	defer tx.Rollback(ctx)
	var currentVersion int
	var revision int64
	err = tx.QueryRow(ctx, `SELECT current_version_number, revision FROM speaking_materials WHERE id=$1 FOR UPDATE`, id).
		Scan(&currentVersion, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return Material{}, ErrNotFound
	}
	if err != nil {
		return Material{}, fmt.Errorf("lock speaking material: %w", err)
	}
	if revision != input.Revision {
		return Material{}, ErrRevisionConflict
	}
	newVersion := currentVersion + 1
	if _, err := tx.Exec(ctx, `UPDATE speaking_materials
		SET slug=$2, revision=revision+1, current_version_number=$3, updated_at=CURRENT_TIMESTAMP
		WHERE id=$1`, id, input.Slug, newVersion); err != nil {
		return Material{}, mapWriteError(err)
	}
	if err := insertVersion(ctx, tx, uuid.New(), id, newVersion, actorID, input); err != nil {
		return Material{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Material{}, fmt.Errorf("commit speaking material update: %w", err)
	}
	return r.Get(ctx, id)
}

func (r *PostgresRepository) Publish(ctx context.Context, id, _ uuid.UUID, expectedRevision int64) (Material, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Material{}, err
	}
	defer tx.Rollback(ctx)
	var revision int64
	var currentVersion int
	err = tx.QueryRow(ctx, `SELECT revision, current_version_number FROM speaking_materials WHERE id=$1 FOR UPDATE`, id).
		Scan(&revision, &currentVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Material{}, ErrNotFound
	}
	if err != nil {
		return Material{}, fmt.Errorf("lock speaking material for publish: %w", err)
	}
	if revision != expectedRevision {
		return Material{}, ErrRevisionConflict
	}
	versionID := uuid.Nil
	if err := tx.QueryRow(ctx, `SELECT id FROM speaking_material_versions WHERE material_id=$1 AND version_number=$2`, id, currentVersion).Scan(&versionID); err != nil {
		return Material{}, fmt.Errorf("get current speaking version: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE speaking_materials
		SET status='PUBLISHED', published_version_id=$2, published_at=CURRENT_TIMESTAMP,
			revision=revision+1, updated_at=CURRENT_TIMESTAMP
		WHERE id=$1`, id, versionID); err != nil {
		return Material{}, fmt.Errorf("publish speaking material: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Material{}, fmt.Errorf("commit speaking material publish: %w", err)
	}
	return r.Get(ctx, id)
}

func insertVersion(ctx context.Context, tx pgx.Tx, versionID, materialID uuid.UUID, number int, actorID uuid.UUID, input SaveInput) error {
	parts, err := json.Marshal(input.Parts)
	if err != nil {
		return fmt.Errorf("encode speaking parts: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO speaking_material_versions
		(id, material_id, version_number, exam_type, difficulty, title, description, parts, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9)`,
		versionID, materialID, number, input.ExamType, input.Difficulty, input.Title, input.Description, parts, actorID); err != nil {
		return fmt.Errorf("insert speaking material version: %w", err)
	}
	return nil
}

type scanner interface{ Scan(...any) error }

func scanMaterial(row scanner) (Material, error) {
	var material Material
	var parts []byte
	if err := row.Scan(
		&material.ID, &material.Slug, &material.ExamType, &material.Difficulty,
		&material.Status, &material.Revision, &material.Title, &material.Description,
		&parts, &material.CurrentVersionNumber, &material.PublishedVersionID,
		&material.HasUnpublishedChanges, &material.PublishedAt, &material.CreatedAt, &material.UpdatedAt,
	); err != nil {
		return Material{}, err
	}
	if err := json.Unmarshal(parts, &material.Parts); err != nil {
		return Material{}, fmt.Errorf("decode speaking material parts: %w", err)
	}
	if material.Parts == nil {
		material.Parts = []Part{}
	}
	return material, nil
}

func mapReadError(material Material, err error, operation string) (Material, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return Material{}, ErrNotFound
	}
	if err != nil {
		return Material{}, fmt.Errorf("%s: %w", operation, err)
	}
	return material, nil
}

func mapWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "speaking_materials_slug_key" {
		return ErrSlugExists
	}
	return err
}
