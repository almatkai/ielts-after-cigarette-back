package aiproviders

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository interface {
	List(context.Context) ([]Provider, error)
	Get(context.Context, uuid.UUID) (Provider, error)
	Save(context.Context, Provider, uuid.UUID, bool) (Provider, error)
	Delete(context.Context, uuid.UUID, int64) error
}
type PostgresRepository struct{ pool *pgxpool.Pool }

func NewRepository(pool *pgxpool.Pool) *PostgresRepository { return &PostgresRepository{pool} }

const columns = `id,name,endpoint,model,speaking_model,scopes,enabled,priority,timeout_seconds,key_ciphertext,revision,updated_at`

// Only a missing provider table is an expected deployment/readiness state.
// Permission, connection and SQL errors must not look like an empty bank.
func storageError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "42P01" {
		return ErrMigrationRequired
	}
	return err
}

func scan(row interface{ Scan(...any) error }) (Provider, error) {
	var p Provider
	err := row.Scan(&p.ID, &p.Name, &p.Endpoint, &p.Model, &p.SpeakingModel, &p.Scopes, &p.Enabled, &p.Priority, &p.TimeoutSeconds, &p.Ciphertext, &p.Revision, &p.UpdatedAt)
	p.HasKey = len(p.Ciphertext) > 0
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, storageError(err)
}
func (r *PostgresRepository) List(ctx context.Context) ([]Provider, error) {
	if r.pool == nil {
		return []Provider{}, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT `+columns+` FROM ai_providers ORDER BY priority,id`)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	items := []Provider{}
	for rows.Next() {
		p, err := scan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, storageError(rows.Err())
}
func (r *PostgresRepository) Get(ctx context.Context, id uuid.UUID) (Provider, error) {
	return scan(r.pool.QueryRow(ctx, `SELECT `+columns+` FROM ai_providers WHERE id=$1`, id))
}
func (r *PostgresRepository) Save(ctx context.Context, p Provider, actor uuid.UUID, create bool) (Provider, error) {
	if create {
		tx, err := r.pool.Begin(ctx)
		if err != nil {
			return Provider{}, err
		}
		defer tx.Rollback(ctx)
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('ai_provider_capacity',0))`); err != nil {
			return Provider{}, err
		}
		var count int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM ai_providers WHERE id<>$1`, EnvProviderID).Scan(&count); err != nil {
			return Provider{}, storageError(err)
		}
		if p.ID != EnvProviderID && count >= MaxChain {
			return Provider{}, ErrValidation
		}
		result, err := scan(tx.QueryRow(ctx, `INSERT INTO ai_providers (id,name,endpoint,model,speaking_model,scopes,enabled,priority,timeout_seconds,key_ciphertext,created_by)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING `+columns, p.ID, p.Name, p.Endpoint, p.Model, p.SpeakingModel, p.Scopes, p.Enabled, p.Priority, p.TimeoutSeconds, p.Ciphertext, actor))
		if err != nil {
			var postgresError *pgconn.PgError
			if p.ID == EnvProviderID && errors.As(err, &postgresError) && postgresError.Code == "23505" {
				return Provider{}, ErrConflict
			}
			return Provider{}, err
		}
		if err = tx.Commit(ctx); err != nil {
			return Provider{}, err
		}
		return result, nil
	}
	result, err := scan(r.pool.QueryRow(ctx, `UPDATE ai_providers SET name=$2,endpoint=$3,model=$4,speaking_model=$5,scopes=$6,enabled=$7,priority=$8,timeout_seconds=$9,key_ciphertext=$10,revision=revision+1,updated_at=CURRENT_TIMESTAMP WHERE id=$1 AND revision=$11 RETURNING `+columns, p.ID, p.Name, p.Endpoint, p.Model, p.SpeakingModel, p.Scopes, p.Enabled, p.Priority, p.TimeoutSeconds, p.Ciphertext, p.Revision))
	if errors.Is(err, ErrNotFound) {
		return Provider{}, ErrConflict
	}
	return result, err
}
func (r *PostgresRepository) Delete(ctx context.Context, id uuid.UUID, revision int64) error {
	result, err := r.pool.Exec(ctx, `DELETE FROM ai_providers WHERE id=$1 AND revision=$2`, id, revision)
	if err != nil {
		return storageError(err)
	}
	if result.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}
