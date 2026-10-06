package ailimits

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
)

type Handler struct {
	service *Service
	logger  *slog.Logger
}

func NewHandler(service *Service, logger *slog.Logger) *Handler {
	return &Handler{
		service: service,
		logger:  logger,
	}
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	limits, err := h.service.Get(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "get daily limits", "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to retrieve daily limits", nil)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, limits)
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	var input UpdateLimitsInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Invalid request payload", nil)
		return
	}

	limits, err := h.service.Update(r.Context(), input)
	if err != nil {
		if errors.Is(err, ErrInvalidLimits) {
			httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error(), nil)
			return
		}
		h.logger.ErrorContext(r.Context(), "update daily limits", "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update daily limits", nil)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, limits)
}
