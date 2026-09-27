package assistant

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
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

func (h *Handler) Chat(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	if err := httpx.DecodeJSON(w, r, h.maxRequestBody, &req); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Request body is invalid", nil)
		return
	}

	if len(req.Messages) == 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, "EMPTY_MESSAGES", "At least one message is required", nil)
		return
	}

	resp, err := h.service.Chat(r.Context(), req)
	if err != nil {
		if httpx.ClientGone(w, r, err) {
			return
		}
		if errors.Is(err, ErrAIUnavailable) {
			httpx.WriteError(w, r, http.StatusServiceUnavailable, "AI_UNAVAILABLE", "AI service is currently not available", nil)
			return
		}
		h.logger.ErrorContext(r.Context(), "assistant chat failed",
			"request_id", httpx.RequestID(r.Context()),
			"error", err,
		)
		httpx.WriteError(w, r, http.StatusBadGateway, "AI_FAILED", "AI assistant could not process the request", nil)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, resp)
}
