package phoneverification

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type Handler struct {
	service  *Service
	logger   *slog.Logger
	maxBytes int64
}

func NewHandler(service *Service, logger *slog.Logger, maxBytes int64) *Handler {
	return &Handler{service: service, logger: logger, maxBytes: maxBytes}
}

type sendRequest struct {
	Phone   string `json:"phone"`
	Purpose string `json:"purpose"`
}

func (h *Handler) Send(w http.ResponseWriter, r *http.Request) {
	var request sendRequest
	if err := httpx.DecodeJSON(w, r, h.maxBytes, &request); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON", nil)
		return
	}
	result, details, err := h.service.Send(r.Context(), SendInput{
		Phone: request.Phone, Purpose: request.Purpose,
	})
	if len(details) > 0 {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", details)
		return
	}
	if errors.Is(err, ErrResendTooSoon) {
		httpx.WriteError(w, r, http.StatusTooManyRequests, "VERIFICATION_RESEND_TOO_SOON", "Wait before requesting another code", nil)
		return
	}
	if errors.Is(err, ErrSenderNotConfigured) {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "WHATSAPP_NOT_CONFIGURED", "WhatsApp verification is not configured yet", nil)
		return
	}
	if errors.Is(err, ErrDeliveryFailed) {
		h.logger.ErrorContext(r.Context(), "send phone verification",
			"request_id", httpx.RequestID(r.Context()),
			"error", err,
		)
		httpx.WriteError(w, r, http.StatusBadGateway, "WHATSAPP_DELIVERY_FAILED", "WhatsApp verification could not be delivered", nil)
		return
	}
	if err != nil {
		h.internalError(w, r, "send phone verification", err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, result)
}

type confirmRequest struct {
	Phone   string `json:"phone"`
	Purpose string `json:"purpose"`
	Code    string `json:"code"`
}

func (h *Handler) Confirm(w http.ResponseWriter, r *http.Request) {
	verificationID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "verificationID")))
	if err != nil {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", map[string]string{
			"verificationId": "must be a valid UUID",
		})
		return
	}
	var request confirmRequest
	if err := httpx.DecodeJSON(w, r, h.maxBytes, &request); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON", nil)
		return
	}
	result, details, err := h.service.Confirm(r.Context(), ConfirmInput{
		VerificationID: verificationID,
		Phone:          request.Phone,
		Purpose:        request.Purpose,
		Code:           request.Code,
	})
	if len(details) > 0 {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", details)
		return
	}
	if errors.Is(err, ErrInvalidCode) {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "INVALID_VERIFICATION_CODE", "Verification code is invalid or expired", nil)
		return
	}
	if err != nil {
		h.internalError(w, r, "confirm phone verification", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) internalError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	h.logger.ErrorContext(r.Context(), operation,
		"request_id", httpx.RequestID(r.Context()),
		"error", err,
	)
	httpx.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "An internal error occurred", nil)
}
