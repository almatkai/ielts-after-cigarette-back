package waitlist

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

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
	FirstName   string `json:"firstName,omitempty"`
	LastName    string `json:"lastName,omitempty"`
	Email       string `json:"email,omitempty"`
	Phone       string `json:"phone"`
	Source      string `json:"source,omitempty"`
	GoogleToken string `json:"googleToken,omitempty"`
}

type checkRequest struct {
	GoogleToken string `json:"googleToken,omitempty"`
	Phone       string `json:"phone,omitempty"`
}

func (h *Handler) Check(w http.ResponseWriter, r *http.Request) {
	var request checkRequest
	if err := httpx.DecodeJSON(w, r, h.maxBytes, &request); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON", nil)
		return
	}
	result, details, err := h.service.Check(r.Context(), CheckInput{
		GoogleToken: request.GoogleToken,
		Phone:       request.Phone,
	})
	if len(details) > 0 {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", details)
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "check waitlist",
			"request_id", httpx.RequestID(r.Context()),
			"error", err,
		)
		httpx.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "An internal error occurred", nil)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) AdminList(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	entries, err := h.service.ListForAdmin(r.Context(), token)
	if errors.Is(err, ErrInvalidAdminToken) {
		httpx.WriteError(w, r, http.StatusUnauthorized, "INVALID_TOKEN", "Google token is missing or invalid", nil)
		return
	}
	if errors.Is(err, ErrAdminForbidden) {
		httpx.WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "This Google account is not allowed to view the waitlist", nil)
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list waitlist entries",
			"request_id", httpx.RequestID(r.Context()),
			"error", err,
		)
		httpx.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "An internal error occurred", nil)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"entries": entries, "total": len(entries)})
}

func (h *Handler) Join(w http.ResponseWriter, r *http.Request) {
	var request joinRequest
	if err := httpx.DecodeJSON(w, r, h.maxBytes, &request); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON", nil)
		return
	}
	entry, details, err := h.service.Join(r.Context(), JoinInput{
		FirstName:   request.FirstName,
		LastName:    request.LastName,
		Email:       request.Email,
		Phone:       request.Phone,
		Source:      request.Source,
		GoogleToken: request.GoogleToken,
	})
	if len(details) > 0 {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", details)
		return
	}
	if errors.Is(err, ErrEntryExists) {
		httpx.WriteError(w, r, http.StatusConflict, "WAITLIST_ENTRY_EXISTS", "This Google account or phone number is already on the waitlist", nil)
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
