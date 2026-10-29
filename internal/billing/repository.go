package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}
type Repository interface {
	Plans(context.Context) ([]Plan, error)
	Plan(context.Context, string) (Plan, error)
	Settings(context.Context) (Settings, error)
	Snapshot(context.Context, uuid.UUID) (Access, error)
	CreateOrder(context.Context, Order) (Order, error)
	SetProviderOrder(context.Context, uuid.UUID, string) error
	Order(context.Context, uuid.UUID) (Order, error)
	Orders(context.Context, *uuid.UUID, string, int, int) ([]Order, error)
	Entitlements(context.Context, uuid.UUID) ([]Entitlement, error)
	Grant(context.Context, uuid.UUID, uuid.UUID, GrantInput) (Entitlement, error)
	Notify(context.Context, string, Notification) (Order, bool, error)
	Revoke(context.Context, uuid.UUID, string) error
	Meetings(context.Context, uuid.UUID, int) error
	UpdateSettings(context.Context, Settings) (Settings, error)
	UpdatePlan(context.Context, string, PlanInput) (Plan, error)
}
type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}
func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

const planColumns = `code,title,list_price_kzt,sale_price_kzt,COALESCE(sale_price_kzt,list_price_kzt),access_days,meetings_count,meetings_kind,active`

type scanner interface{ Scan(...any) error }

func scanPlan(row scanner) (p Plan, err error) {
	err = row.Scan(&p.Code, &p.Title, &p.ListPrice, &p.SalePrice, &p.Price, &p.AccessDays, &p.MeetingsCount, &p.MeetingsKind, &p.Active)
	return p, notFound(err)
}
func (r *PostgresRepository) Plans(ctx context.Context) ([]Plan, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+planColumns+` FROM billing_plans WHERE active ORDER BY sort_order,code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Plan{}
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}
func (r *PostgresRepository) Plan(ctx context.Context, code string) (Plan, error) {
	return scanPlan(r.pool.QueryRow(ctx, `SELECT `+planColumns+` FROM billing_plans WHERE code=$1`, code))
}
func settings(ctx context.Context, q queryer) (s Settings, err error) {
	err = q.QueryRow(ctx, `SELECT free_listening,free_reading,free_writing,free_speaking,free_assistant_daily_limit,updated_at FROM billing_settings WHERE id=1`).Scan(&s.FreeListening, &s.FreeReading, &s.FreeWriting, &s.FreeSpeaking, &s.FreeAssistantDailyLimit, &s.UpdatedAt)
	return
}
func (r *PostgresRepository) Settings(ctx context.Context) (Settings, error) {
	return settings(ctx, r.pool)
}
func lockUser(ctx context.Context, q queryer, user uuid.UUID) error {
	var id uuid.UUID
	return notFound(q.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, user).Scan(&id))
}
func snapshot(ctx context.Context, q queryer, user uuid.UUID) (Access, error) {
	var a Access
	var role, status string
	if err := q.QueryRow(ctx, `SELECT role,status FROM users WHERE id=$1`, user).Scan(&role, &status); err != nil {
		return a, notFound(err)
	}
	a.Guest = role == "GUEST" || status == "GUEST"
	a.Staff = role == "ADMIN" || role == "EDITOR"
	s, err := settings(ctx, q)
	if err != nil {
		return a, err
	}
	a.Assistant = AssistantQuota{Limit: s.FreeAssistantDailyLimit, Remaining: s.FreeAssistantDailyLimit}
	a.Free = map[string]Quota{}
	for skill, limit := range s.Limits() {
		a.Free[skill] = Quota{Limit: limit, Remaining: limit}
	}
	rows, err := q.Query(ctx, `SELECT skill,COUNT(*) FROM free_usage WHERE user_id=$1 GROUP BY skill`, user)
	if err != nil {
		return a, err
	}
	for rows.Next() {
		var skill string
		var used int
		if err := rows.Scan(&skill, &used); err != nil {
			rows.Close()
			return a, err
		}
		quota := a.Free[skill]
		quota.Used = used
		quota.Remaining = max(0, quota.Limit-used)
		a.Free[skill] = quota
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return a, err
	}
	var p PlanView
	var ends time.Time
	err = q.QueryRow(ctx, `SELECT p.code,p.title,e.ends_at,e.meetings_total,e.meetings_used
 FROM user_entitlements e JOIN billing_plans p ON p.code=e.plan_code
 WHERE e.user_id=$1 AND e.revoked_at IS NULL AND e.starts_at<=clock_timestamp() AND e.ends_at>clock_timestamp()
 ORDER BY e.starts_at,e.id LIMIT 1`, user).Scan(&p.Code, &p.Title, &ends, &a.MeetingsTotal, &a.MeetingsUsed)
	if err == nil {
		a.Active = true
		a.Plan = &p
		a.EndsAt = &ends
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return a, err
	}
	err = q.QueryRow(ctx, `SELECT MAX(ends_at) FROM user_entitlements WHERE user_id=$1 AND revoked_at IS NULL AND ends_at>clock_timestamp()`, user).Scan(&a.QueuedUntil)
	return a, err
}
func (r *PostgresRepository) Snapshot(ctx context.Context, user uuid.UUID) (Access, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Access{}, err
	}
	defer tx.Rollback(ctx)
	a, err := snapshot(ctx, tx, user)
	if err != nil {
		return a, err
	}
	return a, tx.Commit(ctx)
}

const orderColumns = `o.id,o.user_id,u.email,o.status,o.plan_code,o.amount_kzt,o.list_price_kzt,o.created_at,o.paid_at,o.currency,o.provider,o.provider_order_id,
 COALESCE((o.payload->>'accessDays')::integer,p.access_days),COALESCE((o.payload->>'meetingsCount')::integer,p.meetings_count)`
const orderFrom = ` FROM billing_orders o JOIN users u ON u.id=o.user_id JOIN billing_plans p ON p.code=o.plan_code `

func scanOrder(row scanner) (o Order, err error) {
	err = row.Scan(&o.ID, &o.UserID, &o.Email, &o.Status, &o.PlanCode, &o.Amount, &o.ListPrice, &o.CreatedAt, &o.PaidAt, &o.Currency, &o.Provider, &o.ProviderOrderID, &o.AccessDays, &o.MeetingsCount)
	return o, notFound(err)
}
func (r *PostgresRepository) CreateOrder(ctx context.Context, o Order) (Order, error) {
	payload, err := json.Marshal(map[string]any{"accessDays": o.AccessDays, "meetingsCount": o.MeetingsCount})
	if err != nil {
		return o, err
	}
	err = r.pool.QueryRow(ctx, `INSERT INTO billing_orders(id,user_id,plan_code,list_price_kzt,amount_kzt,provider,payload) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING created_at`, o.ID, o.UserID, o.PlanCode, o.ListPrice, o.Amount, o.Provider, payload).Scan(&o.CreatedAt)
	o.Status = "PENDING"
	o.Currency = "KZT"
	return o, err
}
func (r *PostgresRepository) SetProviderOrder(ctx context.Context, id uuid.UUID, providerID string) error {
	if providerID == "" {
		return ErrInvalidCallback
	}
	tag, err := r.pool.Exec(ctx, `UPDATE billing_orders SET provider_order_id=$2 WHERE id=$1 AND (provider_order_id IS NULL OR provider_order_id=$2)`, id, providerID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrInvalidCallback
	}
	return err
}
func (r *PostgresRepository) Order(ctx context.Context, id uuid.UUID) (Order, error) {
	return scanOrder(r.pool.QueryRow(ctx, `SELECT `+orderColumns+orderFrom+`WHERE o.id=$1`, id))
}
func (r *PostgresRepository) Orders(ctx context.Context, user *uuid.UUID, status string, limit, offset int) ([]Order, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+orderColumns+orderFrom+`WHERE ($1::uuid IS NULL OR o.user_id=$1) AND ($2='' OR o.status=$2) ORDER BY o.created_at DESC,o.id DESC LIMIT NULLIF($3,0) OFFSET $4`, user, status, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Order{}
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, o)
	}
	return result, rows.Err()
}

const entitlementColumns = `id,user_id,plan_code,order_id,source,starts_at,ends_at,revoked_at,meetings_total,meetings_used,granted_by,note,created_at`

func scanEntitlement(row scanner) (e Entitlement, err error) {
	err = row.Scan(&e.ID, &e.UserID, &e.PlanCode, &e.OrderID, &e.Source, &e.StartsAt, &e.EndsAt, &e.RevokedAt, &e.MeetingsTotal, &e.MeetingsUsed, &e.GrantedBy, &e.Note, &e.CreatedAt)
	return e, notFound(err)
}
func (r *PostgresRepository) Entitlements(ctx context.Context, user uuid.UUID) ([]Entitlement, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+entitlementColumns+` FROM user_entitlements WHERE user_id=$1 ORDER BY starts_at,id`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Entitlement{}
	for rows.Next() {
		e, err := scanEntitlement(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

// Caller owns the user's row lock. All grants and quota consumption use it.
func insertEntitlement(ctx context.Context, tx pgx.Tx, e Entitlement, days int) (Entitlement, error) {
	var now time.Time
	var last *time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp(),MAX(ends_at) FROM user_entitlements WHERE user_id=$1 AND revoked_at IS NULL`, e.UserID).Scan(&now, &last); err != nil {
		return e, err
	}
	e.StartsAt, e.EndsAt = entitlementWindow(now, last, days)
	return scanEntitlement(tx.QueryRow(ctx, `INSERT INTO user_entitlements(id,user_id,plan_code,order_id,source,starts_at,ends_at,meetings_total,granted_by,note)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING `+entitlementColumns, e.ID, e.UserID, e.PlanCode, e.OrderID, e.Source, e.StartsAt, e.EndsAt, e.MeetingsTotal, e.GrantedBy, e.Note))
}
func (r *PostgresRepository) Grant(ctx context.Context, user, actor uuid.UUID, input GrantInput) (Entitlement, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Entitlement{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockUser(ctx, tx, user); err != nil {
		return Entitlement{}, err
	}
	p, err := scanPlan(tx.QueryRow(ctx, `SELECT `+planColumns+` FROM billing_plans WHERE code=$1`, input.PlanCode))
	if err != nil {
		return Entitlement{}, err
	}
	days := p.AccessDays
	if input.Days != nil {
		days = *input.Days
	}
	e, err := insertEntitlement(ctx, tx, Entitlement{ID: uuid.New(), UserID: user, PlanCode: p.Code, Source: input.Source, MeetingsTotal: p.MeetingsCount, GrantedBy: &actor, Note: input.Note}, days)
	if err != nil {
		return e, err
	}
	return e, tx.Commit(ctx)
}
func (r *PostgresRepository) Notify(ctx context.Context, provider string, n Notification) (Order, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Order{}, false, err
	}
	defer tx.Rollback(ctx)
	o, err := scanOrder(tx.QueryRow(ctx, `SELECT `+orderColumns+orderFrom+`WHERE o.id=$1 FOR UPDATE OF o`, n.OrderID))
	if err != nil {
		return o, false, err
	}
	if o.Provider != provider || n.Currency != o.Currency || n.Amount != o.Amount || n.ProviderOrderID == "" || (o.ProviderOrderID != nil && *o.ProviderOrderID != n.ProviderOrderID) {
		return o, false, ErrInvalidCallback
	}
	if err := lockUser(ctx, tx, o.UserID); err != nil {
		return o, false, err
	}
	if n.Status == o.Status {
		return o, false, tx.Commit(ctx)
	}
	valid := o.Status == "PENDING" && (n.Status == "PAID" || n.Status == "FAILED" || n.Status == "CANCELLED") || o.Status == "PAID" && n.Status == "REFUNDED"
	if !valid {
		return o, false, ErrInvalidCallback
	}
	payload, err := json.Marshal(n.Payload)
	if err != nil {
		return o, false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE billing_orders SET status=$2,provider_order_id=$3,paid_at=CASE WHEN $2='PAID' THEN clock_timestamp() ELSE paid_at END,payload=jsonb_set(payload,'{notification}',$4::jsonb) WHERE id=$1`, o.ID, n.Status, n.ProviderOrderID, payload); err != nil {
		return o, false, err
	}
	paid := n.Status == "PAID"
	if paid {
		if _, err = insertEntitlement(ctx, tx, Entitlement{ID: uuid.New(), UserID: o.UserID, PlanCode: o.PlanCode, OrderID: &o.ID, Source: "PAYMENT", MeetingsTotal: o.MeetingsCount}, o.AccessDays); err != nil {
			return o, false, err
		}
	}
	if n.Status == "REFUNDED" {
		if _, err = tx.Exec(ctx, `UPDATE user_entitlements SET revoked_at=clock_timestamp() WHERE order_id=$1 AND revoked_at IS NULL`, o.ID); err != nil {
			return o, false, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return o, false, err
	}
	o.Status = n.Status
	return o, paid, nil
}
func (r *PostgresRepository) Revoke(ctx context.Context, id uuid.UUID, note string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var user uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT user_id FROM user_entitlements WHERE id=$1`, id).Scan(&user); err != nil {
		return notFound(err)
	}
	if err = lockUser(ctx, tx, user); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE user_entitlements SET revoked_at=COALESCE(revoked_at,clock_timestamp()),note=CASE WHEN $2='' THEN note ELSE concat_ws(E'\n',NULLIF(note,''),$2) END WHERE id=$1`, id, note)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *PostgresRepository) Meetings(ctx context.Context, id uuid.UUID, used int) error {
	tag, err := r.pool.Exec(ctx, `UPDATE user_entitlements SET meetings_used=$2 WHERE id=$1 AND $2>=0 AND $2<=meetings_total`, id, used)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err = r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_entitlements WHERE id=$1)`, id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		return ErrValidation
	}
	return nil
}
func (r *PostgresRepository) UpdateSettings(ctx context.Context, s Settings) (Settings, error) {
	err := r.pool.QueryRow(ctx, `UPDATE billing_settings SET free_listening=$1,free_reading=$2,free_writing=$3,free_speaking=$4,free_assistant_daily_limit=$5,updated_at=clock_timestamp() WHERE id=1 RETURNING updated_at`, s.FreeListening, s.FreeReading, s.FreeWriting, s.FreeSpeaking, s.FreeAssistantDailyLimit).Scan(&s.UpdatedAt)
	return s, err
}
func (r *PostgresRepository) UpdatePlan(ctx context.Context, code string, input PlanInput) (Plan, error) {
	p, err := scanPlan(r.pool.QueryRow(ctx, `UPDATE billing_plans SET list_price_kzt=$2,sale_price_kzt=$3,active=$4,updated_at=clock_timestamp() WHERE code=$1 RETURNING `+planColumns, code, *input.ListPrice, input.SalePrice, *input.Active))
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23514" {
		return p, fmt.Errorf("%w: prices", ErrValidation)
	}
	return p, err
}
