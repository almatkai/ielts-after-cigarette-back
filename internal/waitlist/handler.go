package waitlist

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
)

type Handler struct {
	service  *Service
	logger   *slog.Logger
	maxBytes int64
}

func NewHandler(service *Service, logger *slog.Logger, maxBytes int64) *Handler {
	return &Handler{service: service, logger: logger, maxBytes: maxBytes}
}

type joinRequest struct {
	FirstName         string `json:"firstName,omitempty"`
	LastName          string `json:"lastName,omitempty"`
	Email             string `json:"email,omitempty"`
	Phone             string `json:"phone"`
	Source            string `json:"source,omitempty"`
	VerificationToken string `json:"verificationToken"`
	GoogleToken       string `json:"googleToken,omitempty"`
}

func (h *Handler) Join(w http.ResponseWriter, r *http.Request) {
	var request joinRequest
	if err := httpx.DecodeJSON(w, r, h.maxBytes, &request); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON", nil)
		return
	}
	entry, details, err := h.service.Join(r.Context(), JoinInput{
		FirstName:         request.FirstName,
		LastName:          request.LastName,
		Email:             request.Email,
		Phone:             request.Phone,
		Source:            request.Source,
		VerificationToken: request.VerificationToken,
		GoogleToken:       request.GoogleToken,
	})
	if len(details) > 0 {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", details)
		return
	}
	if errors.Is(err, ErrPhoneNotVerified) {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "PHONE_NOT_VERIFIED", "Phone verification is invalid or expired", nil)
		return
	}
	if errors.Is(err, ErrEntryExists) {
		httpx.WriteError(w, r, http.StatusConflict, "WAITLIST_ENTRY_EXISTS", "This phone number is already on the waitlist", nil)
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "join waitlist",
			"request_id", httpx.RequestID(r.Context()),
			"error", err,
		)
		httpx.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "An internal error occurred", nil)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, entry)
}
