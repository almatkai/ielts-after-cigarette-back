package auth

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
)

type Handler struct {
	service  *Service
	logger   *slog.Logger
	maxBytes int64
	cookie   CookieConfig
}

type CookieConfig struct {
	Name     string
	Secure   bool
	SameSite http.SameSite
	MaxAge   time.Duration
}

func NewHandler(service *Service, logger *slog.Logger, maxBytes int64, cookie CookieConfig) *Handler {
	return &Handler{service: service, logger: logger, maxBytes: maxBytes, cookie: cookie}
}

type registerRequest struct {
	Name            string `json:"name"`
	Email           string `json:"email"`
	Password        string `json:"password"`
	ConfirmPassword string `json:"confirmPassword,omitempty"`
	AcceptedTerms   bool   `json:"acceptedTerms"`
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var request registerRequest
	if err := httpx.DecodeJSON(w, r, h.maxBytes, &request); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON", nil)
		return
	}
	result, details, err := h.service.Register(r.Context(), RegisterInput{
		Name:            request.Name,
		Email:           request.Email,
		Password:        request.Password,
		ConfirmPassword: request.ConfirmPassword,
		AcceptedTerms:   request.AcceptedTerms,
		UserAgent:       limited(r.UserAgent(), 512),
		IPAddress:       clientIP(r),
	})
	if len(details) > 0 {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", details)
		return
	}
	if errors.Is(err, ErrEmailExists) {
		httpx.WriteError(w, r, http.StatusConflict, "EMAIL_ALREADY_EXISTS", "An account with this email already exists", nil)
		return
	}
	if err != nil {
		h.internalError(w, r, "register user", err)
		return
	}
	h.setRefreshCookie(w, result.RefreshToken)
	httpx.WriteJSON(w, http.StatusCreated, result)
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Remember bool   `json:"remember,omitempty"`
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var request loginRequest
	if err := httpx.DecodeJSON(w, r, h.maxBytes, &request); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON", nil)
		return
	}
	result, details, err := h.service.Login(r.Context(), LoginInput{
		Email:     request.Email,
		Password:  request.Password,
		UserAgent: limited(r.UserAgent(), 512),
		IPAddress: clientIP(r),
	})
	if len(details) > 0 {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Request validation failed", details)
		return
	}
	if errors.Is(err, ErrInvalidCredentials) {
		httpx.WriteError(w, r, http.StatusUnauthorized, "INVALID_CREDENTIALS", "Email or password is incorrect", nil)
		return
	}
	if err != nil {
		h.internalError(w, r, "log in user", err)
		return
	}
	h.setRefreshCookie(w, result.RefreshToken)
	httpx.WriteJSON(w, http.StatusOK, result)
}

type refreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}

func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	refreshToken, err := h.refreshToken(w, r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON", nil)
		return
	}
	result, err := h.service.Refresh(r.Context(), refreshToken, SessionMetadata{
		UserAgent: limited(r.UserAgent(), 512),
		IPAddress: clientIP(r),
	})
	if errors.Is(err, ErrInvalidRefresh) || errors.Is(err, ErrRefreshReuse) {
		h.clearRefreshCookie(w)
		httpx.WriteError(w, r, http.StatusUnauthorized, "INVALID_REFRESH_TOKEN", "Refresh token is invalid or expired", nil)
		return
	}
	if err != nil {
		h.internalError(w, r, "refresh session", err)
		return
	}
	h.setRefreshCookie(w, result.RefreshToken)
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	refreshToken, err := h.refreshToken(w, r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON", nil)
		return
	}
	if err := h.service.Logout(r.Context(), refreshToken); err != nil && !errors.Is(err, ErrInvalidRefresh) {
		h.internalError(w, r, "log out session", err)
		return
	}
	h.clearRefreshCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	userID, ok := UserID(r.Context())
	if !ok {
		httpx.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required", nil)
		return
	}
	user, err := h.service.User(r.Context(), userID)
	if errors.Is(err, ErrUserNotFound) {
		httpx.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "User no longer exists", nil)
		return
	}
	if err != nil {
		h.internalError(w, r, "read current user", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, user)
}

func (h *Handler) internalError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	h.logger.ErrorContext(r.Context(), operation,
		"request_id", httpx.RequestID(r.Context()),
		"error", err,
	)
	httpx.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "An internal error occurred", nil)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return limited(host, 45)
	}
	return limited(strings.TrimSpace(r.RemoteAddr), 45)
}

func limited(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) > max {
		return value[:max]
	}
	return value
}

func (h *Handler) refreshToken(w http.ResponseWriter, r *http.Request) (string, error) {
	cookie, err := r.Cookie(h.cookie.Name)
	if err == nil && strings.TrimSpace(cookie.Value) != "" {
		return cookie.Value, nil
	}
	if r.ContentLength == 0 {
		return "", nil
	}
	var request refreshRequest
	if err := httpx.DecodeJSON(w, r, h.maxBytes, &request); err != nil {
		return "", err
	}
	return request.RefreshToken, nil
}

func (h *Handler) setRefreshCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     h.cookie.Name,
		Value:    token,
		Path:     "/api/v1/auth",
		MaxAge:   int(h.cookie.MaxAge.Seconds()),
		HttpOnly: true,
		Secure:   h.cookie.Secure,
		SameSite: h.cookie.SameSite,
	})
}

func (h *Handler) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     h.cookie.Name,
		Value:    "",
		Path:     "/api/v1/auth",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.cookie.Secure,
		SameSite: h.cookie.SameSite,
	})
}
