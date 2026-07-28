package health

import (
	"context"
	"net/http"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
)

type Check func(context.Context) error

type Handler struct {
	postgres Check
	redis    Check
	timeout  time.Duration
}

func NewHandler(postgres, redis Check) *Handler {
	return &Handler{postgres: postgres, redis: redis, timeout: 2 * time.Second}
}

func (h *Handler) Live(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()

	details := map[string]string{"postgres": "ok", "redis": "ok"}
	if err := h.postgres(ctx); err != nil {
		details["postgres"] = "unavailable"
	}
	if err := h.redis(ctx); err != nil {
		details["redis"] = "unavailable"
	}
	if details["postgres"] != "ok" || details["redis"] != "ok" {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status":       "not_ready",
			"dependencies": details,
		})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"status":       "ok",
		"dependencies": details,
	})
}
