package ailimits

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository interface {
	Get(ctx context.Context) (Limits, bool, error)
	Upsert(ctx context.Context, limits Limits) (Limits, error)
}

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) Get(ctx context.Context) (Limits, bool, error) {
	if r == nil || r.pool == nil {
		return Limits{}, false, nil
	}
	var (
		assistant      int64
		guestAssistant int64
		writing        int64
		speaking       int64
		updatedAt      time.Time
	)
	err := r.pool.QueryRow(ctx, `
		SELECT assistant_limit, guest_assistant_limit, writing_limit, speaking_limit, updated_at
		FROM system_daily_limits
		WHERE id = 1
	`).Scan(&assistant, &guestAssistant, &writing, &speaking, &updatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Limits{}, false, nil
		}
		// If table doesn't exist yet, treat as not found rather than error
		return Limits{}, false, nil
	}
	return Limits{
		AssistantLimit:      assistant,
		GuestAssistantLimit: guestAssistant,
		WritingLimit:        writing,
		SpeakingLimit:       speaking,
		UpdatedAt:           updatedAt,
	}, true, nil
}

func (r *PostgresRepository) Upsert(ctx context.Context, limits Limits) (Limits, error) {
	if r == nil || r.pool == nil {
		limits.UpdatedAt = time.Now().UTC()
		return limits, nil
	}
	var updatedAt time.Time
	err := r.pool.QueryRow(ctx, `
		INSERT INTO system_daily_limits (id, assistant_limit, guest_assistant_limit, writing_limit, speaking_limit, updated_at)
		VALUES (1, $1, $2, $3, $4, CURRENT_TIMESTAMP)
		ON CONFLICT (id) DO UPDATE SET
			assistant_limit = EXCLUDED.assistant_limit,
			guest_assistant_limit = EXCLUDED.guest_assistant_limit,
			writing_limit = EXCLUDED.writing_limit,
			speaking_limit = EXCLUDED.speaking_limit,
			updated_at = CURRENT_TIMESTAMP
		RETURNING updated_at
	`, limits.AssistantLimit, limits.GuestAssistantLimit, limits.WritingLimit, limits.SpeakingLimit).Scan(&updatedAt)
	if err != nil {
		return Limits{}, err
	}
	limits.UpdatedAt = updatedAt
	return limits, nil
}
