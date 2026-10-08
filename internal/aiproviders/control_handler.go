package aiproviders

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
)

func (h *Handler) controlFail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrMigrationRequired) {
		httpx.WriteError(w, r, 503, "AI_ROUTING_MIGRATION_REQUIRED", "Apply database migration 000032 for AI routing and statistics", nil)
		return
	}
	h.fail(w, r, err)
}
func (h *Handler) Reorder(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Items []OrderItem `json:"items"`
	}
	if err := httpx.DecodeJSON(w, r, 8<<10, &input); err != nil {
		h.fail(w, r, ErrValidation)
		return
	}
	actor, _ := auth.UserID(r.Context())
	if err := h.service.Reorder(r.Context(), input.Items, actor); err != nil {
		h.fail(w, r, err)
		return
	}
	h.List(w, r)
}
func (h *Handler) Routing(w http.ResponseWriter, r *http.Request) {
	p, migration, err := h.service.Routing(r.Context())
	if err != nil {
		h.controlFail(w, r, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"routing": p, "migrationRequired": migration})
}
func (h *Handler) SaveRouting(w http.ResponseWriter, r *http.Request) {
	var input Routing
	if err := httpx.DecodeJSON(w, r, 8<<10, &input); err != nil {
		h.fail(w, r, ErrValidation)
		return
	}
	if input.Mode == "hedged" && h.service.TelemetryRequired() && !httpx.ErrorReporterInstalled() {
		httpx.WriteError(w, r, 503, "AI_TELEMETRY_REQUIRED", "Configure GlitchTip before enabling parallel routing", nil)
		return
	}
	p, err := h.service.SaveRouting(r.Context(), input)
	if err != nil {
		h.controlFail(w, r, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"routing": p, "migrationRequired": false})
}
func (h *Handler) Stats(w http.ResponseWriter, r *http.Request) {
	days := 7
	if raw := r.URL.Query().Get("days"); raw != "" {
		var err error
		days, err = strconv.Atoi(raw)
		if err != nil {
			h.fail(w, r, ErrValidation)
			return
		}
	}
	p, err := h.service.Stats(r.Context(), days)
	if err != nil {
		h.controlFail(w, r, err)
		return
	}
	httpx.WriteJSON(w, 200, p)
}
