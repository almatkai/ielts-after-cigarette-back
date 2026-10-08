package auth

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
)

// Google posts credentials here after a full-page sign-in. Do not depend on
// window.opener: iOS in-app browsers can replace the original page with a popup.
func (h *Handler) googleRedirect(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	r.Body = http.MaxBytesReader(w, r.Body, h.maxBytes)
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/app/login?google=error", http.StatusSeeOther)
		return
	}
	csrf, err := r.Cookie("g_csrf_token")
	postedCSRF := r.PostForm.Get("g_csrf_token")
	if err != nil || csrf.Value == "" || postedCSRF == "" || subtle.ConstantTimeCompare([]byte(csrf.Value), []byte(postedCSRF)) != 1 {
		http.Redirect(w, r, "/app/login?google=error", http.StatusSeeOther)
		return
	}
	outcome, err := h.service.GoogleLogin(r.Context(), GoogleLoginInput{
		GoogleToken: r.PostForm.Get("credential"),
		UserAgent:   limited(r.UserAgent(), 512),
		IPAddress:   clientIP(r),
	})
	if err != nil {
		if !errors.Is(err, ErrInvalidGoogleToken) && !errors.Is(err, ErrAccountNotFound) {
			h.logger.ErrorContext(r.Context(), "google redirect login failed", "error", err)
		}
		h.setGoogleRegistrationCookie(w, "")
		http.Redirect(w, r, "/app/login?google=error", http.StatusSeeOther)
		return
	}
	if outcome.PendingRegistration != nil {
		h.clearRefreshCookie(w)
		h.setGoogleRegistrationCookie(w, outcome.PendingRegistration.Token)
		http.Redirect(w, r, "/app/login?google=registration", http.StatusSeeOther)
		return
	}
	h.setGoogleRegistrationCookie(w, "")
	h.setRefreshCookie(w, outcome.Session.RefreshToken)
	http.Redirect(w, r, "/app/login?google=success", http.StatusSeeOther)
}

// PendingGoogleRegistration restores the signed profile after a full-page return.
// The credential and registration token never travel in a URL or browser history.
func (h *Handler) PendingGoogleRegistration(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	cookie, err := r.Cookie(h.cookie.Name + "_google_registration")
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		httpx.WriteError(w, r, http.StatusUnauthorized, "GOOGLE_TOKEN_INVALID", "Registration token is missing or expired", nil)
		return
	}
	claims, err := h.service.tokens.ParseGoogleRegistrationToken(cookie.Value)
	if err != nil {
		h.setGoogleRegistrationCookie(w, "")
		httpx.WriteError(w, r, http.StatusUnauthorized, "GOOGLE_TOKEN_INVALID", "Registration token is invalid or expired", nil)
		return
	}
	profile := GoogleProfile{Email: claims.Email, Name: googleDisplayName(claims.Name, claims.Email)}
	// Preserve a waitlist lead's phone prefill, just as the popup flow does.
	lead, err := h.service.repository.FindUserByGoogleSub(r.Context(), claims.Subject)
	if errors.Is(err, ErrUserNotFound) {
		lead, err = h.service.repository.FindUserByEmail(r.Context(), claims.Email)
	}
	if err != nil && !errors.Is(err, ErrUserNotFound) {
		h.internalError(w, r, "restore google registration", err)
		return
	}
	if err == nil && LeadStatus(lead.Status) {
		profile.Phone = lead.Phone
	}
	httpx.WriteJSON(w, http.StatusOK, pendingRegistrationResponse{
		RegistrationRequired: true,
		RegistrationToken:    cookie.Value,
		Profile:              profile,
	})
}

func (h *Handler) setGoogleRegistrationCookie(w http.ResponseWriter, token string) {
	maxAge := int(googleRegistrationTTL.Seconds())
	if token == "" {
		maxAge = -1
	}
	http.SetCookie(w, &http.Cookie{
		Name:     h.cookie.Name + "_google_registration",
		Value:    token,
		Path:     "/api/v1/auth",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   h.cookie.Secure,
		SameSite: http.SameSiteLaxMode,
	})
}
