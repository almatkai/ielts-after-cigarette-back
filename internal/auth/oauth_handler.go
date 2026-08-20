package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth/oauth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// OAuthHandler serves the redirect-based external OAuth flow
// (GET /auth/{provider}/start and /auth/{provider}/callback). It never writes
// JSON: every outcome is a 302 back to the frontend, either into the app with
// a fresh session cookie, to /app/login with an oauth_error code, or to
// /app/login with a pending registration token.
type OAuthHandler struct {
	service      *Service
	tokens       *TokenManager
	providers    map[string]oauth.Provider
	frontendBase string
	cookie       CookieConfig
	maxBytes     int64
	logger       *slog.Logger
}

func NewOAuthHandler(
	service *Service,
	tokens *TokenManager,
	providers map[string]oauth.Provider,
	frontendBase string,
	cookie CookieConfig,
	maxBytes int64,
	logger *slog.Logger,
) *OAuthHandler {
	return &OAuthHandler{
		service:      service,
		tokens:       tokens,
		providers:    providers,
		frontendBase: frontendBase,
		cookie:       cookie,
		maxBytes:     maxBytes,
		logger:       logger,
	}
}

// Start issues a signed state token and redirects the browser to the
// provider's authorize page.
func (h *OAuthHandler) Start(w http.ResponseWriter, r *http.Request) {
	provider, ok := h.provider(w, r)
	if !ok {
		return
	}
	state, err := h.tokens.NewOAuthStateToken(provider.Name(), r.URL.Query().Get("next"))
	if err != nil {
		h.logger.ErrorContext(r.Context(), "issue oauth state token",
			"request_id", httpx.RequestID(r.Context()),
			"error", err,
		)
		h.redirectLoginError(w, r, "server_error")
		return
	}
	http.Redirect(w, r, provider.StartURL(state), http.StatusFound)
}

// Callback validates the state, exchanges the code for the provider identity,
// and redirects per the sign-in outcome.
func (h *OAuthHandler) Callback(w http.ResponseWriter, r *http.Request) {
	provider, ok := h.provider(w, r)
	if !ok {
		return
	}
	if r.URL.Query().Get("error") != "" {
		h.redirectLoginError(w, r, "access_denied")
		return
	}
	state, err := h.tokens.ParseOAuthStateToken(r.URL.Query().Get("state"))
	if err != nil || state.Provider != provider.Name() {
		h.redirectLoginError(w, r, "invalid_state")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		h.redirectLoginError(w, r, "access_denied")
		return
	}

	identity, err := provider.Exchange(r.Context(), code)
	if errors.Is(err, oauth.ErrEmailRequired) {
		h.redirectLoginError(w, r, "email_required")
		return
	}
	if err != nil {
		h.logger.WarnContext(r.Context(), "oauth code exchange failed",
			"request_id", httpx.RequestID(r.Context()),
			"provider", provider.Name(),
			"error", err,
		)
		h.redirectLoginError(w, r, "exchange_failed")
		return
	}

	outcome, err := h.service.LoginWithOAuth(r.Context(), provider.Name(), identity, SessionMetadata{
		UserAgent: limited(r.UserAgent(), 512),
		IPAddress: clientIP(r),
	})
	if errors.Is(err, ErrOAuthEmailRequired) {
		h.redirectLoginError(w, r, "email_required")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "log in user with oauth",
			"request_id", httpx.RequestID(r.Context()),
			"provider", provider.Name(),
			"error", err,
		)
		h.redirectLoginError(w, r, "server_error")
		return
	}
	if outcome.PendingRegistration != nil {
		target := h.loginURL()
		query := target.Query()
		query.Set("registration", outcome.PendingRegistration.Token)
		target.RawQuery = query.Encode()
		http.Redirect(w, r, target.String(), http.StatusFound)
		return
	}
	h.setRefreshCookie(w, outcome.Session.RefreshToken)
	http.Redirect(w, r, h.frontendBase+state.Next, http.StatusFound)
}

func (h *OAuthHandler) provider(w http.ResponseWriter, r *http.Request) (oauth.Provider, bool) {
	provider, ok := h.providers[chi.URLParam(r, "provider")]
	if !ok {
		httpx.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "OAuth provider is not configured", nil)
		return nil, false
	}
	return provider, true
}

func (h *OAuthHandler) loginURL() *url.URL {
	target, err := url.Parse(h.frontendBase + "/app/login")
	if err != nil {
		// frontendBase comes from config and defaults to a valid URL; a broken
		// value would surface at startup in every redirect anyway.
		return &url.URL{Path: "/app/login"}
	}
	return target
}

func (h *OAuthHandler) redirectLoginError(w http.ResponseWriter, r *http.Request, code string) {
	target := h.loginURL()
	query := target.Query()
	query.Set("oauth_error", code)
	target.RawQuery = query.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func (h *OAuthHandler) setRefreshCookie(w http.ResponseWriter, token string) {
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
