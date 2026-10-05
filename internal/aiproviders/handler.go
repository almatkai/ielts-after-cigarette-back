package aiproviders

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type Handler struct {
	service *Service
	logger  *slog.Logger
}

func NewHandler(service *Service, logger *slog.Logger) *Handler { return &Handler{service, logger} }
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.List(r.Context())
	migrationRequired := errors.Is(err, ErrMigrationRequired)
	if err != nil && !migrationRequired {
		h.fail(w, r, err)
		return
	}
	if items == nil {
		items = []Provider{}
	}
	httpx.WriteJSON(w, 200, map[string]any{"items": items, "migrationRequired": migrationRequired, "encryptionConfigured": h.service.EncryptionConfigured(), "envFallbackConfigured": h.service.EnvConfigured(), "errorReportingConfigured": httpx.ErrorReporterInstalled(), "errorReportingRequired": h.service.TelemetryRequired(), "maxProviders": MaxChain})
}
func (h *Handler) Save(w http.ResponseWriter, r *http.Request) {
	var input Input
	if err := httpx.DecodeJSON(w, r, 16<<10, &input); err != nil {
		h.fail(w, r, ErrValidation)
		return
	}
	if input.Enabled && h.service.TelemetryRequired() && !httpx.ErrorReporterInstalled() {
		httpx.WriteError(w, r, 503, "AI_TELEMETRY_REQUIRED", "Configure GlitchTip before enabling AI providers", nil)
		return
	}
	id := uuid.Nil
	if r.Method == http.MethodPut {
		var err error
		id, err = uuid.Parse(chi.URLParam(r, "providerID"))
		if err != nil {
			h.fail(w, r, ErrValidation)
			return
		}
	}
	actor, _ := auth.UserID(r.Context())
	item, err := h.service.Save(r.Context(), id, actor, input)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	status := 200
	if r.Method == http.MethodPost {
		status = 201
	}
	httpx.WriteJSON(w, status, item)
}
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "providerID"))
	if err != nil {
		h.fail(w, r, ErrValidation)
		return
	}
	revision, err := strconv.ParseInt(r.URL.Query().Get("revision"), 10, 64)
	if err != nil {
		h.fail(w, r, ErrValidation)
		return
	}
	if err := h.service.Delete(r.Context(), id, revision); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func (h *Handler) Test(w http.ResponseWriter, r *http.Request) {
	if h.service.TelemetryRequired() && !httpx.ErrorReporterInstalled() {
		httpx.WriteError(w, r, 503, "AI_TELEMETRY_REQUIRED", "Configure GlitchTip before testing AI providers", nil)
		return
	}
	id := uuid.Nil
	var input *Input
	if raw := chi.URLParam(r, "providerID"); raw != "" {
		var err error
		id, err = uuid.Parse(raw)
		if err != nil {
			h.fail(w, r, ErrValidation)
			return
		}
	}
	if id == uuid.Nil || r.ContentLength != 0 {
		input = &Input{}
		if err := httpx.DecodeJSON(w, r, 16<<10, input); err != nil {
			h.fail(w, r, ErrValidation)
			return
		}
	}
	result, err := h.service.Test(r.Context(), id, input)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, 200, result)
}
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrMigrationRequired):
		httpx.WriteError(w, r, 503, "AI_PROVIDER_MIGRATION_REQUIRED", "Apply database migrations through 000031 before managing AI providers", nil)
	case errors.Is(err, ErrValidation):
		httpx.WriteError(w, r, 422, "VALIDATION_ERROR", "Check name, HTTPS completion URL, model, scopes, timeout and API key", nil)
	case errors.Is(err, ErrEncryption):
		httpx.WriteError(w, r, 503, "AI_ENCRYPTION_UNAVAILABLE", "AI provider key encryption is not configured or the stored key cannot be decrypted", nil)
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, r, 404, "AI_PROVIDER_NOT_FOUND", "Provider not found", nil)
	case errors.Is(err, ErrConflict):
		httpx.WriteError(w, r, 409, "REVISION_CONFLICT", "Provider changed; refresh and retry", nil)
	default:
		httpx.InternalError(w, r, h.logger, errors.New("AI provider configuration storage failed"))
	}
}
