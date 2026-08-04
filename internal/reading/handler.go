package reading

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
	service        *Service
	logger         *slog.Logger
	maxRequestBody int64
}

func NewHandler(service *Service, logger *slog.Logger, maxRequestBody int64) *Handler {
	return &Handler{service: service, logger: logger, maxRequestBody: maxRequestBody}
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	materials, err := h.service.List(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": materials})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	materialID, ok := h.materialID(w, r)
	if !ok {
		return
	}
	material, err := h.service.Get(r.Context(), materialID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, material)
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var input SaveInput
	if err := httpx.DecodeJSON(w, r, h.maxRequestBody, &input); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be a valid JSON object", nil)
		return
	}
	actorID, _ := auth.UserID(r.Context())
	material, details, err := h.service.Create(r.Context(), actorID, input)
	if len(details) > 0 {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", details)
		return
	}
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, material)
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	materialID, ok := h.materialID(w, r)
	if !ok {
		return
	}
	var input SaveInput
	if err := httpx.DecodeJSON(w, r, h.maxRequestBody, &input); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be a valid JSON object", nil)
		return
	}
	actorID, _ := auth.UserID(r.Context())
	material, details, err := h.service.Update(r.Context(), materialID, actorID, input)
	if len(details) > 0 {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", details)
		return
	}
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, material)
}

func (h *Handler) Publish(w http.ResponseWriter, r *http.Request) {
	materialID, ok := h.materialID(w, r)
	if !ok {
		return
	}
	var input PublishInput
	if err := httpx.DecodeJSON(w, r, h.maxRequestBody, &input); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be a valid JSON object", nil)
		return
	}
	actorID, _ := auth.UserID(r.Context())
	material, details, err := h.service.Publish(r.Context(), materialID, actorID, input.Revision)
	if len(details) > 0 {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", details)
		return
	}
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, material)
}

func (h *Handler) materialID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "materialID"))
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ID", "Material ID must be a UUID", nil)
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, "READING_MATERIAL_NOT_FOUND", "Reading material was not found", nil)
	case errors.Is(err, ErrSlugExists):
		httpx.WriteError(w, r, http.StatusConflict, "READING_SLUG_EXISTS", "A reading material with this slug already exists", nil)
	case errors.Is(err, ErrRevisionConflict):
		httpx.WriteError(w, r, http.StatusConflict, "REVISION_CONFLICT", "The material was changed by another editor; reload it before saving", nil)
	default:
		h.logger.ErrorContext(r.Context(), "reading material request failed", "request_id", httpx.RequestID(r.Context()), "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "The server could not process the request", nil)
	}
}
