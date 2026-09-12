package fullmock

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
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

func (h *Handler) ListPublic(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListPublic(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) GetPublic(w http.ResponseWriter, r *http.Request) {
	id, ok := h.id(w, r, "mockID")
	if !ok {
		return
	}
	item, err := h.service.GetPublic(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, item)
}

func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	id, ok := h.id(w, r, "mockID")
	if !ok {
		return
	}
	userID, _ := auth.UserID(r.Context())
	session, created, err := h.service.Start(r.Context(), userID, id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpx.WriteJSON(w, status, session)
}

func (h *Handler) GetSession(w http.ResponseWriter, r *http.Request) {
	id, ok := h.id(w, r, "sessionID")
	if !ok {
		return
	}
	userID, _ := auth.UserID(r.Context())
	session, err := h.service.GetSession(r.Context(), userID, id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, session)
}

func (h *Handler) Advance(w http.ResponseWriter, r *http.Request) {
	id, ok := h.id(w, r, "sessionID")
	if !ok {
		return
	}
	userID, _ := auth.UserID(r.Context())
	session, err := h.service.Advance(r.Context(), userID, id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, session)
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
	id, ok := h.id(w, r, "mockID")
	if !ok {
		return
	}
	item, err := h.service.Get(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, item)
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var input SaveInput
	if !h.decode(w, r, &input) {
		return
	}
	item, details, err := h.service.Create(r.Context(), input)
	h.writeSave(w, r, http.StatusCreated, item, details, err)
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := h.id(w, r, "mockID")
	if !ok {
		return
	}
	var input SaveInput
	if !h.decode(w, r, &input) {
		return
	}
	item, details, err := h.service.Update(r.Context(), id, input)
	h.writeSave(w, r, http.StatusOK, item, details, err)
}

func (h *Handler) Publish(w http.ResponseWriter, r *http.Request) {
	id, ok := h.id(w, r, "mockID")
	if !ok {
		return
	}
	var input PublishInput
	if !h.decode(w, r, &input) {
		return
	}
	item, details, err := h.service.Publish(r.Context(), id, input.Revision)
	h.writeSave(w, r, http.StatusOK, item, details, err)
}

func (h *Handler) decode(w http.ResponseWriter, r *http.Request, target any) bool {
	if err := httpx.DecodeJSON(w, r, h.maxBody, target); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON", nil)
		return false
	}
	return true
}

func (h *Handler) id(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ID", "ID must be UUID", nil)
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) writeSave(w http.ResponseWriter, r *http.Request, status int, item Test, details map[string]string, err error) {
	if len(details) > 0 {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", details)
		return
	}
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, status, item)
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrSessionNotFound), errors.Is(err, attempts.ErrMaterialNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, "FULL_MOCK_NOT_FOUND", "Full mock resource was not found", nil)
	case errors.Is(err, ErrSectionIncomplete):
		httpx.WriteError(w, r, http.StatusConflict, "FULL_MOCK_SECTION_INCOMPLETE", "Submit the current section before continuing", nil)
	case errors.Is(err, ErrSessionCompleted):
		httpx.WriteError(w, r, http.StatusConflict, "FULL_MOCK_COMPLETED", "Full mock session was already completed", nil)
	case errors.Is(err, ErrRevisionConflict):
		httpx.WriteError(w, r, http.StatusConflict, "REVISION_CONFLICT", "Full mock was changed by another editor", nil)
	case errors.Is(err, ErrSlugExists):
		httpx.WriteError(w, r, http.StatusConflict, "FULL_MOCK_SLUG_EXISTS", "Full mock slug already exists", nil)
	default:
		h.logger.ErrorContext(r.Context(), "full mock request failed", "request_id", httpx.RequestID(r.Context()), "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Request failed", nil)
	}
}
