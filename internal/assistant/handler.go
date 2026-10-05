package assistant

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/aiproviders"
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

func (h *Handler) Stream(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	if err := httpx.DecodeJSON(w, r, h.maxRequestBody, &req); err != nil || !validRequest(req) {
		httpx.WriteError(w, r, 400, "INVALID_REQUEST", "Invalid chat request", nil)
		return
	}
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Now().Add(aiproviders.ChainTimeout + 15*time.Second))
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	emit := func(event StreamEvent) error {
		data, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := w.Write(append(append([]byte("data: "), data...), []byte("\n\n")...)); err != nil {
			return err
		}
		return controller.Flush()
	}
	response, err := h.service.Stream(r.Context(), req, emit)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		// Per-provider failures (including recovered ones) were already reported
		// by the router. Never expose upstream errors or partial mixed answers.
		_ = emit(StreamEvent{Type: "reset"})
		_ = emit(StreamEvent{Type: "unavailable", Text: "Сейчас не удаётся получить ответ. Попробуй ещё раз чуть позже."})
		return
	}
	_ = emit(StreamEvent{Type: "done", Response: &response})
}

func (h *Handler) Chat(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	if err := httpx.DecodeJSON(w, r, h.maxRequestBody, &req); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Request body is invalid", nil)
		return
	}

	if !validRequest(req) {
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
