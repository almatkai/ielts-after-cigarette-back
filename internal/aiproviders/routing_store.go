package aiproviders

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Routing struct {
	Mode         string      `json:"mode"`
	MaxParallel  int         `json:"maxParallel"`
	HedgeDelayMS int         `json:"hedgeDelayMs"`
	ProviderIDs  []uuid.UUID `json:"providerIds"`
	Revision     int64       `json:"revision"`
}

func defaultRouting() Routing {
	return Routing{Mode: "sequential", MaxParallel: 1, HedgeDelayMS: 0, ProviderIDs: []uuid.UUID{}}
}

type CallMetric struct {
	ID              uuid.UUID `json:"id"`
	RunID           uuid.UUID `json:"runId"`
	ProviderID      uuid.UUID `json:"providerId"`
	ProviderName    string    `json:"providerName"`
	Model           string    `json:"model"`
	Purpose         string    `json:"purpose"`
	FromEnv         bool      `json:"fromEnv"`
	StartedAt       time.Time `json:"startedAt"`
	DurationMS      int64     `json:"durationMs"`
	FirstResponseMS *int64    `json:"firstResponseMs"`
	FirstTokenMS    *int64    `json:"firstTokenMs"`
	Outcome         string    `json:"outcome"`
	Won             bool      `json:"won"`
	Trigger         string    `json:"trigger"`
	Mode            string    `json:"mode"`
	Code            string    `json:"code"`
	HTTPStatus      int       `json:"httpStatus"`
	RequestID       string    `json:"requestId"`
}
type ModelStats struct {
	ProviderID      uuid.UUID `json:"providerId"`
	ProviderName    string    `json:"providerName"`
	Model           string    `json:"model"`
	Purpose         string    `json:"purpose"`
	Calls           int64     `json:"calls"`
	Successes       int64     `json:"successes"`
	Failures        int64     `json:"failures"`
	Timeouts        int64     `json:"timeouts"`
	Cancelled       int64     `json:"cancelled"`
	Wins            int64     `json:"wins"`
	P50MS           *float64  `json:"p50Ms"`
	P95MS           *float64  `json:"p95Ms"`
	FirstTokenMS    *float64  `json:"firstTokenMs"`
	FirstResponseMS *float64  `json:"firstResponseMs"`
}
type Stats struct {
	MigrationRequired bool         `json:"migrationRequired"`
	Models            []ModelStats `json:"models"`
	Recent            []CallMetric `json:"recent"`
}

type RoutingStore interface {
	Load(context.Context) (Routing, error)
	Save(context.Context, Routing) (Routing, error)
	Record(context.Context, CallMetric) error
	Stats(context.Context, int) (Stats, error)
}
type postgresRoutingStore struct {
	pool      *pgxpool.Pool
	lastPrune atomic.Int64
}

func (r *postgresRoutingStore) Load(ctx context.Context) (Routing, error) {
	p := defaultRouting()
	err := r.pool.QueryRow(ctx, `SELECT mode,max_parallel,hedge_delay_ms,provider_ids,revision FROM ai_provider_routing WHERE singleton`).Scan(&p.Mode, &p.MaxParallel, &p.HedgeDelayMS, &p.ProviderIDs, &p.Revision)
	return p, storageError(err)
}
func (r *postgresRoutingStore) Save(ctx context.Context, p Routing) (Routing, error) {
	var revision int64
	err := r.pool.QueryRow(ctx, `UPDATE ai_provider_routing SET mode=$1,max_parallel=$2,hedge_delay_ms=$3,provider_ids=$4,revision=revision+1,updated_at=now() WHERE singleton AND revision=$5 RETURNING revision`, p.Mode, p.MaxParallel, p.HedgeDelayMS, p.ProviderIDs, p.Revision).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrConflict
	}
	p.Revision = revision
	return p, storageError(err)
}
func (r *postgresRoutingStore) Record(ctx context.Context, p CallMetric) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO ai_provider_calls(id,run_id,provider_id,provider_name,model,purpose,from_env,started_at,duration_ms,first_response_ms,first_token_ms,outcome,won,trigger,mode,code,http_status,request_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`, p.ID, p.RunID, p.ProviderID, p.ProviderName, p.Model, p.Purpose, p.FromEnv, p.StartedAt, p.DurationMS, p.FirstResponseMS, p.FirstTokenMS, p.Outcome, p.Won, p.Trigger, p.Mode, p.Code, p.HTTPStatus, p.RequestID)
	if err == nil {
		now := time.Now().Unix()
		previous := r.lastPrune.Load()
		if now-previous >= 3600 && r.lastPrune.CompareAndSwap(previous, now) {
			// Operational metrics have a rolling 30-day retention, never student data.
			_, _ = r.pool.Exec(ctx, `DELETE FROM ai_provider_calls WHERE started_at < now() - interval '30 days'`)
		}
	}
	return storageError(err)
}
func (r *postgresRoutingStore) Stats(ctx context.Context, days int) (Stats, error) {
	result := Stats{Models: []ModelStats{}, Recent: []CallMetric{}}
	rows, err := r.pool.Query(ctx, `SELECT provider_id,(array_agg(provider_name ORDER BY started_at DESC))[1],model,purpose,count(*),count(*) FILTER(WHERE outcome='success'),count(*) FILTER(WHERE outcome IN ('failure','timeout')),count(*) FILTER(WHERE outcome='timeout'),count(*) FILTER(WHERE outcome='cancelled'),count(*) FILTER(WHERE won),percentile_cont(0.5) WITHIN GROUP(ORDER BY duration_ms) FILTER(WHERE outcome='success'),percentile_cont(0.95) WITHIN GROUP(ORDER BY duration_ms) FILTER(WHERE outcome='success'),percentile_cont(0.5) WITHIN GROUP(ORDER BY first_token_ms) FILTER(WHERE outcome='success'),percentile_cont(0.5) WITHIN GROUP(ORDER BY first_response_ms) FILTER(WHERE outcome='success') FROM ai_provider_calls WHERE started_at>=now()-($1::int * interval '1 day') GROUP BY provider_id,model,purpose ORDER BY purpose,model,provider_id LIMIT 200`, days)
	if err != nil {
		return result, storageError(err)
	}
	for rows.Next() {
		var p ModelStats
		if err := rows.Scan(&p.ProviderID, &p.ProviderName, &p.Model, &p.Purpose, &p.Calls, &p.Successes, &p.Failures, &p.Timeouts, &p.Cancelled, &p.Wins, &p.P50MS, &p.P95MS, &p.FirstTokenMS, &p.FirstResponseMS); err != nil {
			rows.Close()
			return result, storageError(err)
		}
		result.Models = append(result.Models, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, storageError(err)
	}
	rows, err = r.pool.Query(ctx, `SELECT id,run_id,provider_id,provider_name,model,purpose,from_env,started_at,duration_ms,first_response_ms,first_token_ms,outcome,won,trigger,mode,code,http_status,request_id FROM ai_provider_calls WHERE started_at>=now()-($1::int * interval '1 day') ORDER BY started_at DESC LIMIT 50`, days)
	if err != nil {
		return result, storageError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var p CallMetric
		if err := rows.Scan(&p.ID, &p.RunID, &p.ProviderID, &p.ProviderName, &p.Model, &p.Purpose, &p.FromEnv, &p.StartedAt, &p.DurationMS, &p.FirstResponseMS, &p.FirstTokenMS, &p.Outcome, &p.Won, &p.Trigger, &p.Mode, &p.Code, &p.HTTPStatus, &p.RequestID); err != nil {
			return result, storageError(err)
		}
		result.Recent = append(result.Recent, p)
	}
	return result, storageError(rows.Err())
}
