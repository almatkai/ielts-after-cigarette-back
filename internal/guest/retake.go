package guest

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/fullmock"
	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/google/uuid"
)

var errRetakeQuota = errors.New("retake quota response already written")

func (h *Handler) retake(w http.ResponseWriter, r *http.Request, item Trial, sourceID uuid.UUID) {
	if sourceID == uuid.Nil {
		httpx.WriteError(w, r, 422, "VALIDATION_ERROR", "Укажите тест для пересдачи", nil)
		return
	}
	if !h.limit(w, r, "requests:"+item.UserID.String(), 180, time.Minute) {
		return
	}
	ctx := auth.WithUser(r.Context(), item.UserID, "GUEST")
	session, _, err := h.mocks.RetakeGuest(ctx, item.UserID, sourceID, func(context.Context) error {
		// Retakes share the same daily IP/global budgets as initial starts. The
		// existing verified cookie is retained; neither identity nor TTL resets.
		if !h.limit(w, r, "starts:ip:"+base64.RawURLEncoding.EncodeToString(h.hash(h.ip(r))), h.cfg.IPLimit, 24*time.Hour) ||
			!h.limit(w, r, "starts:global", h.cfg.GlobalLimit, 24*time.Hour) {
			return errRetakeQuota
		}
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, errRetakeQuota):
			return
		case errors.Is(err, fullmock.ErrSessionNotFound):
			httpx.WriteError(w, r, 404, "FULL_MOCK_SESSION_NOT_FOUND", "Тест не найден", nil)
		case errors.Is(err, fullmock.ErrRetakeNotReady):
			httpx.WriteError(w, r, 409, "FULL_MOCK_RETAKE_NOT_READY", "Сначала завершите текущий тест", nil)
		case errors.Is(err, fullmock.ErrGuestUnavailable):
			h.denied(w, r)
		case errors.Is(err, fullmock.ErrBankIncomplete):
			httpx.WriteError(w, r, 409, "FULL_MOCK_BANK_INCOMPLETE", "Пробный тест временно недоступен", nil)
		default:
			httpx.InternalError(w, r, h.logger, err)
		}
		return
	}
	httpx.WriteJSON(w, http.StatusOK, fullmock.SessionForViewer(ctx, session))
}
