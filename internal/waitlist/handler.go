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

func bearerToken(r *http.Request) string {
	return strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
}

// writeAdminError maps the admin authorization errors to HTTP responses and
// reports whether the request is already handled.
func (h *Handler) writeAdminError(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case errors.Is(err, ErrInvalidAdminToken):
		httpx.WriteError(w, r, http.StatusUnauthorized, "INVALID_TOKEN", "Google token is missing or invalid", nil)
	case errors.Is(err, ErrAdminForbidden):
		httpx.WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "This Google account is not a super admin", nil)
	case errors.Is(err, ErrAdminProtected):
		httpx.WriteError(w, r, http.StatusConflict, "ADMIN_PROTECTED", "The bootstrap super admin from the environment cannot be removed", nil)
	case errors.Is(err, ErrAdminEmailInvalid):
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", map[string]string{"email": "must be a valid email address"})
	case err != nil:
		h.logger.ErrorContext(r.Context(), "admin request",
			"request_id", httpx.RequestID(r.Context()),
			"error", err,
		)
		httpx.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "An internal error occurred", nil)
	default:
		return false
	}
	return true
}

type adminRequest struct {
	Email string `json:"email"`
}

func (h *Handler) AdminListAdmins(w http.ResponseWriter, r *http.Request) {
	admins, err := h.service.ListAdminsForAdmin(r.Context(), bearerToken(r))
	if h.writeAdminError(w, r, err) {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"admins": admins})
}

func (h *Handler) AdminAddAdmin(w http.ResponseWriter, r *http.Request) {
	var request adminRequest
	if err := httpx.DecodeJSON(w, r, h.maxBytes, &request); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON", nil)
		return
	}
	err := h.service.AddAdminForAdmin(r.Context(), bearerToken(r), request.Email)
	if h.writeAdminError(w, r, err) {
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"status": "ok"})
}

func (h *Handler) AdminRemoveAdmin(w http.ResponseWriter, r *http.Request) {
	err := h.service.RemoveAdminForAdmin(r.Context(), bearerToken(r), r.PathValue("email"))
	if h.writeAdminError(w, r, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
