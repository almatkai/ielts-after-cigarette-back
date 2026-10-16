package guest

import (
	"context"
	"errors"
	"net/http"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/fullmock"
	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Claim requires both a real account credential and possession of the guest
// cookie. IDs/ownership are never taken from a client-supplied body.
func (h *Handler) Claim(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	account, ok := auth.UserID(r.Context())
	if !ok || !auth.ValidRole(auth.Role(r.Context())) {
		h.denied(w, r)
		return
	}
	if !h.validOrigin(r) {
		httpx.WriteError(w, r, 403, "CORS_ORIGIN_DENIED", "Origin is not allowed", nil)
		return
	}
	if !h.cfg.Enabled {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"sessionId": nil})
		return
	}
	cookie, err := r.Cookie(CookieName)
	if err != nil || len(cookie.Value) != 43 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"sessionId": nil})
		return
	}
	id, err := h.repo.Claim(r.Context(), h.hash(cookie.Value), account)
	if errors.Is(err, ErrAlreadyClaimed) {
		httpx.WriteError(w, r, 409, "GUEST_ALREADY_CLAIMED", "Этот тест уже сохранён в другом аккаунте", nil)
		return
	}
	if err != nil {
		httpx.InternalError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"sessionId": id})
}

func (r *PostgresRepository) Claim(ctx context.Context, hash []byte, account uuid.UUID) (*uuid.UUID, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var actor uuid.UUID
	var claimedBy, claimedSession *uuid.UUID
	var valid bool
	err = tx.QueryRow(ctx, `SELECT user_id,claimed_by,claimed_session_id,expires_at>CURRENT_TIMESTAMP
 FROM guest_trials WHERE token_hash=$1 FOR UPDATE`, hash).Scan(&actor, &claimedBy, &claimedSession, &valid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if claimedBy != nil {
		if *claimedBy != account {
			return nil, ErrAlreadyClaimed
		}
		return claimedSession, nil
	}
	if !valid {
		return nil, nil
	}
	// Coordinate with guest mock generation, retaining its consumed tombstone.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "fullmock:"+actor.String()); err != nil {
		return nil, err
	}
	var id uuid.UUID
	var status string
	err = tx.QueryRow(ctx, `SELECT id,status FROM full_mock_sessions WHERE user_id=$1
 ORDER BY started_at DESC,id DESC LIMIT 1 FOR UPDATE`, actor).Scan(&id, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		// Sign-in during a running mock never steals an active account session.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if status != fullmock.SessionSubmitted {
		// A completed earlier try must not consume access to a running retake.
		return nil, nil
	}
	if _, err = tx.Exec(ctx, `UPDATE attempts SET user_id=$1 WHERE user_id=$2 AND id IN
 (SELECT sec.attempt_id FROM full_mock_session_sections sec JOIN full_mock_sessions s
 ON s.id=sec.session_id WHERE s.user_id=$2 AND s.status='SUBMITTED')`, account, actor); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE full_mock_sessions SET user_id=$1 WHERE user_id=$2 AND status='SUBMITTED'`, account, actor); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE guest_trials SET claimed_by=$1,claimed_session_id=$2 WHERE user_id=$3`, account, id, actor); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &id, nil
}
