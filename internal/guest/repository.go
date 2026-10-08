package guest

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("guest session not found")
var ErrAlreadyClaimed = errors.New("guest trial belongs to another account")

type Trial struct {
	UserID    uuid.UUID  `json:"id"`
	ExpiresAt time.Time  `json:"expiresAt"`
	SessionID *uuid.UUID `json:"sessionId"`
	ClaimedBy *uuid.UUID `json:"-"`
}

type Repository interface {
	Find(context.Context, []byte) (Trial, error)
	Create(context.Context, []byte, string, time.Time) (Trial, error)
	OwnsMedia(context.Context, uuid.UUID, string, uuid.UUID) (bool, error)
	Claim(context.Context, []byte, uuid.UUID) (*uuid.UUID, error)
}

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) Find(ctx context.Context, hash []byte) (Trial, error) {
	var item Trial
	err := r.pool.QueryRow(ctx, `SELECT g.user_id,g.expires_at,
 COALESCE(g.claimed_session_id,(SELECT id FROM full_mock_sessions WHERE user_id=g.user_id ORDER BY started_at,id LIMIT 1)),g.claimed_by
 FROM guest_trials g JOIN users u ON u.id=g.user_id AND u.role='GUEST' WHERE g.token_hash=$1`, hash).
		Scan(&item.UserID, &item.ExpiresAt, &item.SessionID, &item.ClaimedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return Trial{}, ErrNotFound
	}
	return item, err
}

func (r *PostgresRepository) Create(ctx context.Context, hash []byte, examType string, expires time.Time) (Trial, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Trial{}, err
	}
	defer tx.Rollback(ctx)
	id := uuid.New()
	// Reserved .invalid address and an unusable password; normal auth excludes GUEST.
	if _, err = tx.Exec(ctx, `INSERT INTO users (id,email,password_hash,role,status,terms_accepted_at,source)
 VALUES ($1,$2,'!','GUEST','GUEST',CURRENT_TIMESTAMP,'guest-trial')`, id, id.String()+"@guest.invalid"); err != nil {
		return Trial{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_profiles (user_id,display_name,exam_type) VALUES ($1,'Гость',$2)`, id, examType); err != nil {
		return Trial{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO guest_trials (user_id,token_hash,expires_at) VALUES ($1,$2,$3)`, id, hash, expires); err != nil {
		return Trial{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Trial{}, err
	}
	return Trial{UserID: id, ExpiresAt: expires}, nil
}

func (r *PostgresRepository) OwnsMedia(ctx context.Context, actor uuid.UUID, skill string, id uuid.UUID) (bool, error) {
	var allowed bool
	var query string
	switch skill {
	case "listening":
		query = `SELECT EXISTS (SELECT 1 FROM attempts a JOIN full_mock_session_sections sec ON sec.attempt_id=a.id
 JOIN listening_parts p ON p.test_version_id=a.material_version_id
 LEFT JOIN listening_question_groups g ON g.part_id=p.id
 WHERE a.user_id=$1 AND a.material_type='listening' AND (p.audio_asset_id=$2 OR g.image_asset_id=$2))`
	case "writing":
		query = `SELECT EXISTS (SELECT 1 FROM attempts a JOIN full_mock_session_sections sec ON sec.attempt_id=a.id
 JOIN writing_material_versions v ON v.id=a.material_version_id, jsonb_array_elements(v.tasks) task
 WHERE a.user_id=$1 AND a.material_type='writing' AND task->>'visualAssetId'=$2::text)`
	default:
		return false, nil
	}
	err := r.pool.QueryRow(ctx, query, actor, id).Scan(&allowed)
	return allowed, err
}
