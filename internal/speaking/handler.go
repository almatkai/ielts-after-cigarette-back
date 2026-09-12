package speaking

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type Handler struct {
	service *Service
	logger  *slog.Logger
	maxBody int64
}

func NewHandler(service *Service, logger *slog.Logger, maxBody int64) *Handler {
	return &Handler{service: service, logger: logger, maxBody: maxBody}
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.List(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := h.materialID(w, r)
	if !ok {
		return
	}
	material, err := h.service.Get(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, material)
}

func (h *Handler) ListPublic(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListPublic(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) GetPublic(w http.ResponseWriter, r *http.Request) {
	id, ok := h.materialID(w, r)
	if !ok {
		return
	}
	material, err := h.service.GetPublic(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, material)
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var input SaveInput
	if !h.decode(w, r, &input) {
		return
	}
	actor, _ := auth.UserID(r.Context())
	material, details, err := h.service.Create(r.Context(), actor, input)
	h.writeSave(w, r, http.StatusCreated, material, details, err)
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := h.materialID(w, r)
	if !ok {
		return
	}
	var input SaveInput
	if !h.decode(w, r, &input) {
		return
	}
	actor, _ := auth.UserID(r.Context())
	material, details, err := h.service.Update(r.Context(), id, actor, input)
	h.writeSave(w, r, http.StatusOK, material, details, err)
}

func (h *Handler) Publish(w http.ResponseWriter, r *http.Request) {
	id, ok := h.materialID(w, r)
	if !ok {
		return
	}
	var input PublishInput
	if !h.decode(w, r, &input) {
		return
	}
	actor, _ := auth.UserID(r.Context())
	material, details, err := h.service.Publish(r.Context(), id, actor, input.Revision)
	h.writeSave(w, r, http.StatusOK, material, details, err)
}

func (h *Handler) decode(w http.ResponseWriter, r *http.Request, target any) bool {
	if err := httpx.DecodeJSON(w, r, h.maxBody, target); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON", nil)
		return false
	}
	return true
}

func (h *Handler) materialID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "materialID"))
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ID", "Material ID must be UUID", nil)
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) writeSave(w http.ResponseWriter, r *http.Request, status int, material Material, details map[string]string, err error) {
	if len(details) > 0 {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", details)
		return
	}
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, status, material)
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, "SPEAKING_MATERIAL_NOT_FOUND", "Speaking material was not found", nil)
	case errors.Is(err, ErrSlugExists):
		httpx.WriteError(w, r, http.StatusConflict, "SPEAKING_SLUG_EXISTS", "Speaking material slug already exists", nil)
	case errors.Is(err, ErrRevisionConflict):
		httpx.WriteError(w, r, http.StatusConflict, "REVISION_CONFLICT", "Speaking material was changed by another editor", nil)
	default:
		h.logger.ErrorContext(r.Context(), "speaking request failed", "request_id", httpx.RequestID(r.Context()), "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Request failed", nil)
	}
}
