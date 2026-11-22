package billing

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const promocodeColumns = `p.id, p.code, p.discount_type, p.discount_value, p.plan_code, p.min_price_kzt, p.allow_on_sale,
 p.max_uses, p.used_count, p.max_uses_per_user, p.starts_at, p.expires_at, p.is_active, p.partner_user_id,
 COALESCE(NULLIF(TRIM(u.first_name || ' ' || COALESCE(u.last_name, '')), ''), u.email),
 p.created_by, p.note, p.created_at, p.updated_at`

const promocodeFrom = ` FROM promocodes p LEFT JOIN users u ON u.id = p.partner_user_id `

func scanPromocode(row scanner) (p Promocode, err error) {
	err = row.Scan(
		&p.ID, &p.Code, &p.DiscountType, &p.DiscountValue, &p.PlanCode, &p.MinPriceKZT, &p.AllowOnSale,
		&p.MaxUses, &p.UsedCount, &p.MaxUsesPerUser, &p.StartsAt, &p.ExpiresAt, &p.IsActive, &p.PartnerUserID,
		&p.PartnerName, &p.CreatedBy, &p.Note, &p.CreatedAt, &p.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrPromocodeNotFound
	}
	return p, err
}

func (r *PostgresRepository) PromocodeByCode(ctx context.Context, code string) (Promocode, error) {
	clean := strings.ToLower(strings.TrimSpace(code))
	if clean == "" {
		return Promocode{}, ErrPromocodeNotFound
	}
	return scanPromocode(r.pool.QueryRow(ctx, `SELECT `+promocodeColumns+promocodeFrom+`WHERE lower(btrim(p.code)) = $1`, clean))
}

func (r *PostgresRepository) PromocodeByID(ctx context.Context, id uuid.UUID) (Promocode, error) {
	return scanPromocode(r.pool.QueryRow(ctx, `SELECT `+promocodeColumns+promocodeFrom+`WHERE p.id = $1`, id))
}

func (r *PostgresRepository) ListPromocodes(ctx context.Context, partnerUserID *uuid.UUID, activeOnly *bool) ([]Promocode, error) {
	query := `SELECT ` + promocodeColumns + promocodeFrom + `
		WHERE ($1::uuid IS NULL OR p.partner_user_id = $1)
		  AND ($2::boolean IS NULL OR p.is_active = $2)
		ORDER BY p.created_at DESC`
	rows, err := r.pool.Query(ctx, query, partnerUserID, activeOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []Promocode
	for rows.Next() {
		p, err := scanPromocode(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

func (r *PostgresRepository) CreatePromocode(ctx context.Context, input CreatePromocodeInput, actor uuid.UUID) (Promocode, error) {
	code := strings.ToUpper(strings.TrimSpace(input.Code))
	if code == "" || len(code) > 32 {
		return Promocode{}, ErrValidation
	}

	dtype := strings.ToUpper(strings.TrimSpace(input.DiscountType))
	if dtype != "PERCENT" && dtype != "FIXED" && dtype != "FREE" {
		return Promocode{}, ErrValidation
	}

	dval := input.DiscountValue
	var intVal int
	if dtype == "PERCENT" {
		if dval > 0 && dval < 1 {
			dval = dval * 100
		}
		intVal = int(math.Round(dval))
		if intVal < 1 || intVal > 100 {
			return Promocode{}, ErrValidation
		}
	} else if dtype == "FIXED" {
		intVal = int(math.Round(dval))
		if intVal <= 0 {
			return Promocode{}, ErrValidation
		}
	} else if dtype == "FREE" {
		intVal = 0
	}

	if input.MaxUses != nil && *input.MaxUses <= 0 {
		return Promocode{}, ErrValidation
	}
	maxPerUser := 1
	if input.MaxUsesPerUser != nil && *input.MaxUsesPerUser > 0 {
		maxPerUser = *input.MaxUsesPerUser
	}
	if input.MinPriceKZT != nil && *input.MinPriceKZT < 0 {
		return Promocode{}, ErrValidation
	}

	startsAt := time.Now().UTC()
	if input.StartsAt != nil {
		startsAt = input.StartsAt.UTC()
	}
	var expiresAt *time.Time
	if input.ExpiresAt != nil {
		t := input.ExpiresAt.UTC()
		if !t.After(startsAt) {
			return Promocode{}, ErrValidation
		}
		expiresAt = &t
	}

	allowOnSale := true
	if input.AllowOnSale != nil {
		allowOnSale = *input.AllowOnSale
	}

	id := uuid.New()
	query := `INSERT INTO promocodes (
		id, code, discount_type, discount_value, plan_code, min_price_kzt, allow_on_sale,
		max_uses, used_count, max_uses_per_user, starts_at, expires_at, is_active,
		partner_user_id, created_by, note
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7,
		$8, 0, $9, $10, $11, true,
		$12, $13, $14
	)`

	_, err := r.pool.Exec(ctx, query,
		id, code, dtype, intVal, input.PlanCode, input.MinPriceKZT, allowOnSale,
		input.MaxUses, maxPerUser, startsAt, expiresAt,
		input.PartnerUserID, actor, strings.TrimSpace(input.Note),
	)
	if err != nil {
		return Promocode{}, err
	}

	return r.PromocodeByID(ctx, id)
}

func (r *PostgresRepository) UpdatePromocodeStatus(ctx context.Context, id uuid.UUID, isActive bool) (Promocode, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE promocodes SET is_active = $2, updated_at = clock_timestamp() WHERE id = $1`, id, isActive)
	if err != nil {
		return Promocode{}, err
	}
	if tag.RowsAffected() == 0 {
		return Promocode{}, ErrPromocodeNotFound
	}
	return r.PromocodeByID(ctx, id)
}

func (r *PostgresRepository) DeletePromocode(ctx context.Context, id uuid.UUID) error {
	var usedCount int
	err := r.pool.QueryRow(ctx, `SELECT used_count FROM promocodes WHERE id = $1`, id).Scan(&usedCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPromocodeNotFound
	}
	if err != nil {
		return err
	}
	if usedCount > 0 {
		// If already used, soft-deactivate instead of breaking historical FKs
		_, err = r.pool.Exec(ctx, `UPDATE promocodes SET is_active = false, updated_at = clock_timestamp() WHERE id = $1`, id)
		return err
	}

	_, err = r.pool.Exec(ctx, `DELETE FROM promocodes WHERE id = $1`, id)
	return err
}

func (r *PostgresRepository) UserPromocodeRedemptionsCount(ctx context.Context, promoID, userID uuid.UUID) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM promocode_redemptions WHERE promocode_id = $1 AND user_id = $2`, promoID, userID).Scan(&count)
	return count, err
}

func (r *PostgresRepository) PromocodeRedemptions(ctx context.Context, promoID uuid.UUID) ([]PromocodeRedemption, error) {
	query := `SELECT r.id, r.promocode_id, r.user_id, u.email,
		COALESCE(NULLIF(TRIM(u.first_name || ' ' || COALESCE(u.last_name, '')), ''), u.email),
		r.order_id, r.discount_amount, r.final_price, r.created_at
		FROM promocode_redemptions r
		JOIN users u ON u.id = r.user_id
		WHERE r.promocode_id = $1
		ORDER BY r.created_at DESC`

	rows, err := r.pool.Query(ctx, query, promoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []PromocodeRedemption
	for rows.Next() {
		var red PromocodeRedemption
		err := rows.Scan(
			&red.ID, &red.PromocodeID, &red.UserID, &red.UserEmail,
			&red.UserName, &red.OrderID, &red.DiscountAmount, &red.FinalPrice, &red.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		result = append(result, red)
	}
	return result, rows.Err()
}
