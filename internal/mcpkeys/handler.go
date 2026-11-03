package mcpkeys

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
	return &Handler{
		service:        service,
		logger:         logger,
		maxRequestBody: maxRequestBody,
	}
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	keys, err := h.service.ListKeys(r.Context())
	if err != nil {
		httpx.InternalError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ListKeysResponse{Keys: keys})
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	actorID, ok := auth.UserID(r.Context())
	if !ok {
		httpx.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "A valid Bearer token is required", nil)
		return
	}

	var input CreateKeyInput
	if err := httpx.DecodeJSON(w, r, h.maxRequestBody, &input); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be a valid JSON object", nil)
		return
	}

	result, err := h.service.GenerateKey(r.Context(), input.Name, actorID)
	if err != nil {
		httpx.InternalError(w, r, h.logger, err)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, result)
}

func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) {
	keyIDStr := chi.URLParam(r, "keyID")
	keyID, err := uuid.Parse(keyIDStr)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_UUID", "keyID must be a valid UUID", nil)
		return
	}

	if err := h.service.RevokeKey(r.Context(), keyID); err != nil {
		if errors.Is(err, ErrKeyNotFound) {
			httpx.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "MCP key not found or already revoked", nil)
			return
		}
		httpx.InternalError(w, r, h.logger, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
