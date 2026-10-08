package guest

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/fullmock"
	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const CookieName = "iac_guest_trial"
const trialTTL = 7 * 24 * time.Hour
const cookieTTL = 180 * 24 * time.Hour

type Config struct {
	Enabled         bool
	Secure          bool
	SameSite        http.SameSite
	Secret          string
	SiteKey         string
	TurnstileSecret string
	Hostnames       []string
	Origins         []string
	IPLimit         int64
	GlobalLimit     int64
	ExamTypes       []string
}

type MockService interface {
	StartGenerated(context.Context, uuid.UUID, bool) (fullmock.Session, bool, error)
	GetSession(context.Context, uuid.UUID, uuid.UUID) (fullmock.Session, error)
	RetakeGuest(context.Context, uuid.UUID, uuid.UUID, func(context.Context) error) (fullmock.Session, bool, error)
}
type AllowFunc func(context.Context, string, int64, time.Duration) (bool, error)

type Handler struct {
	cfg       Config
	repo      Repository
	mocks     MockService
	allow     AllowFunc
	ip        func(*http.Request) string
	logger    *slog.Logger
	client    *http.Client
	verifyURL string
}

func NewHandler(cfg Config, repo Repository, mocks MockService, allow AllowFunc, ip func(*http.Request) string, logger *slog.Logger) *Handler {
	return &Handler{cfg: cfg, repo: repo, mocks: mocks, allow: allow, ip: ip, logger: logger, client: &http.Client{Timeout: 10 * time.Second}, verifyURL: "https://challenges.cloudflare.com/turnstile/v0/siteverify"}
}

func (h *Handler) Config(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"enabled": h.cfg.Enabled, "siteKey": h.cfg.SiteKey, "examTypes": h.examTypes()})
}

func (h *Handler) examTypes() []string {
	if len(h.cfg.ExamTypes) == 0 {
		return []string{"academic"}
	}
	for _, examType := range h.cfg.ExamTypes {
		if examType == "academic" {
			return []string{"academic"}
		}
	}
	return []string{}
}

func (h *Handler) examTypeEnabled(value string) bool {
	for _, examType := range h.examTypes() {
		if value == examType {
			return true
		}
	}
	return false
}

func (h *Handler) hash(value string) []byte {
	mac := hmac.New(sha256.New, []byte(h.cfg.Secret))
	mac.Write([]byte(value))
	return mac.Sum(nil)
}
func (h *Handler) find(r *http.Request) (Trial, error) {
	cookie, err := r.Cookie(CookieName)
	if err != nil || len(cookie.Value) != 43 {
		return Trial{}, ErrNotFound
	}
	return h.repo.Find(r.Context(), h.hash(cookie.Value))
}
func (h *Handler) Session(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.cfg.Enabled {
		h.denied(w, r)
		return
	}
	item, err := h.find(r)
	if errors.Is(err, ErrNotFound) || err == nil && (item.ClaimedBy != nil || !item.ExpiresAt.After(time.Now())) {
		h.denied(w, r)
		return
	}
	if err != nil {
		httpx.InternalError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, item)
}

func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.cfg.Enabled {
		httpx.WriteError(w, r, 403, "GUEST_DISABLED", "Гостевой тест пока недоступен", nil)
		return
	}
	if !h.validOrigin(r) {
		httpx.WriteError(w, r, 403, "CORS_ORIGIN_DENIED", "Origin is not allowed", nil)
		return
	}
	var input struct {
		ExamType      string     `json:"examType"`
		Token         string     `json:"turnstileToken"`
		AcceptedTerms bool       `json:"acceptedTerms"`
		RetakeSession *uuid.UUID `json:"retakeSessionId"`
	}
	if err := httpx.DecodeJSON(w, r, 4096, &input); err != nil {
		httpx.WriteError(w, r, 400, "INVALID_JSON", "Request body must be valid JSON", nil)
		return
	}
	if (input.ExamType != "academic" && input.ExamType != "general") || !input.AcceptedTerms {
		httpx.WriteError(w, r, 422, "VALIDATION_ERROR", "Выберите тип экзамена и примите условия использования", nil)
		return
	}
	if !h.examTypeEnabled(input.ExamType) {
		httpx.WriteError(w, r, 422, "GUEST_EXAM_TYPE_UNAVAILABLE", "Этот тип пробного экзамена пока недоступен", nil)
		return
	}
	item, err := h.find(r)
	if err == nil {
		if item.ClaimedBy != nil {
			httpx.WriteError(w, r, 403, "GUEST_CLAIMED", "Тест сохранён в аккаунте. Войдите, чтобы открыть результаты", nil)
			return
		}
		if !item.ExpiresAt.After(time.Now()) {
			httpx.WriteError(w, r, 403, "GUEST_EXPIRED", "Срок гостевого доступа истёк. Войдите для продолжения подготовки", nil)
			return
		}
		if input.RetakeSession != nil {
			h.retake(w, r, item, *input.RetakeSession)
		} else {
			h.startOrResume(w, r, item)
		}
		return
	}
	if !errors.Is(err, ErrNotFound) {
		httpx.InternalError(w, r, h.logger, err)
		return
	}
	if input.RetakeSession != nil {
		h.denied(w, r)
		return
	}
	// Throttle challenges too; rejected bot submissions must not flood Siteverify.
	if !h.limit(w, r, "challenge:"+base64.RawURLEncoding.EncodeToString(h.hash(h.ip(r))), 10, time.Minute) {
		return
	}
	if err = h.verify(r.Context(), input.Token); err != nil {
		if errors.Is(err, errChallenge) {
			httpx.WriteError(w, r, 403, "GUEST_CHALLENGE_FAILED", "Подтвердите, что вы человек, и попробуйте снова", nil)
		} else {
			httpx.WriteError(w, r, 503, "DEPENDENCY_UNAVAILABLE", "Проверка безопасности временно недоступна", nil)
		}
		return
	}
	// Positive, fail-closed quotas cap starts even when cookies/IPs are rotated.
	if !h.limit(w, r, "starts:ip:"+base64.RawURLEncoding.EncodeToString(h.hash(h.ip(r))), h.cfg.IPLimit, 24*time.Hour) || !h.limit(w, r, "starts:global", h.cfg.GlobalLimit, 24*time.Hour) {
		return
	}
	var secret [32]byte
	if _, err = rand.Read(secret[:]); err != nil {
		httpx.InternalError(w, r, h.logger, err)
		return
	}
	token := base64.RawURLEncoding.EncodeToString(secret[:])
	item, err = h.repo.Create(r.Context(), h.hash(token), input.ExamType, time.Now().Add(trialTTL))
	if err != nil {
		httpx.InternalError(w, r, h.logger, err)
		return
	}
	// Keep the consumed identity longer than review access, so expiration does
	// not silently grant a second mock to the same browser.
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: token, Path: "/api/v1", HttpOnly: true, Secure: h.cfg.Secure, SameSite: h.cfg.SameSite, MaxAge: int(cookieTTL.Seconds()), Expires: time.Now().Add(cookieTTL)})
	h.startOrResume(w, r, item)
}
func (h *Handler) startOrResume(w http.ResponseWriter, r *http.Request, item Trial) {
	var session fullmock.Session
	var err error
	ctx := auth.WithUser(r.Context(), item.UserID, "GUEST")
	if item.SessionID != nil {
		session, err = h.mocks.GetSession(ctx, item.UserID, *item.SessionID)
	} else {
		session, _, err = h.mocks.StartGenerated(ctx, item.UserID, false)
	}
	if err != nil {
		if errors.Is(err, fullmock.ErrBankIncomplete) {
			httpx.WriteError(w, r, 409, "FULL_MOCK_BANK_INCOMPLETE", "Для полного экзамена пока недостаточно опубликованных тестов", nil)
		} else {
			httpx.InternalError(w, r, h.logger, err)
		}
		return
	}
	httpx.WriteJSON(w, http.StatusOK, fullmock.SessionForViewer(ctx, session))
}

var errChallenge = errors.New("invalid turnstile challenge")

func (h *Handler) verify(ctx context.Context, token string) error {
	if h.cfg.TurnstileSecret == "" {
		return nil
	} // Allowed only for explicitly enabled development.
	if token == "" || len(token) > 2048 {
		return errChallenge
	}
	values := url.Values{"secret": {h.cfg.TurnstileSecret}, "response": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.verifyURL, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("turnstile unavailable")
	}
	var result struct {
		Success  bool   `json:"success"`
		Hostname string `json:"hostname"`
		Action   string `json:"action"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&result); err != nil {
		return err
	}
	if !result.Success || result.Action != "guest_mock" {
		return errChallenge
	}
	for _, hostname := range h.cfg.Hostnames {
		if hostname == result.Hostname {
			return nil
		}
	}
	return errChallenge
}
func (h *Handler) validOrigin(r *http.Request) bool {
	for _, origin := range h.cfg.Origins {
		if r.Header.Get("Origin") == origin {
			return true
		}
	}
	return false
}
func (h *Handler) limit(w http.ResponseWriter, r *http.Request, key string, limit int64, ttl time.Duration) bool {
	if limit <= 0 {
		httpx.WriteError(w, r, 503, "DEPENDENCY_UNAVAILABLE", "Guest quota is not configured", nil)
		return false
	}
	allowed, err := h.allow(r.Context(), "guest:"+key, limit, ttl)
	if err != nil {
		httpx.WriteError(w, r, 503, "DEPENDENCY_UNAVAILABLE", "Guest quota is temporarily unavailable", nil)
		return false
	}
	if !allowed {
		httpx.WriteError(w, r, 429, "GUEST_LIMIT_EXCEEDED", "Лимит гостевого доступа достигнут. Войдите в аккаунт или попробуйте позже", nil)
		return false
	}
	return true
}
func (h *Handler) denied(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, 401, "GUEST_EXPIRED", "Гостевая сессия отсутствует или истекла", nil)
}

// Authenticate grants cookies only this narrow capability set. A guest cookie
// never becomes a Bearer credential for practice, profile or administration.
func (h *Handler) Authenticate(tokens *auth.TokenManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := auth.UserID(r.Context()); ok {
				next.ServeHTTP(w, r)
				return
			}
			if r.Header.Get("Authorization") == "" && h.cfg.Enabled && publicCatalogRoute(r) {
				w.Header().Set("Cache-Control", "public, max-age=30")
				next.ServeHTTP(w, r)
				return
			}
			if r.Header.Get("Authorization") != "" || !h.cfg.Enabled || !allowedRoute(r) {
				h.denied(w, r)
				return
			}
			item, err := h.find(r)
			if errors.Is(err, ErrNotFound) || err == nil && (item.ClaimedBy != nil || !item.ExpiresAt.After(time.Now())) {
				h.denied(w, r)
				return
			}
			if err != nil {
				httpx.InternalError(w, r, h.logger, err)
				return
			}
			if r.Method != http.MethodGet && !h.validOrigin(r) {
				httpx.WriteError(w, r, 403, "CORS_ORIGIN_DENIED", "Origin is not allowed", nil)
				return
			}
			if !h.limit(w, r, "requests:"+item.UserID.String(), 180, time.Minute) {
				return
			}
			if id := chi.URLParam(r, "mediaID"); id != "" {
				mediaID, err := uuid.Parse(id)
				if err != nil {
					h.denied(w, r)
					return
				}
				skill := "listening"
				if strings.Contains(r.URL.Path, "/writing/") {
					skill = "writing"
				}
				allowed, err := h.repo.OwnsMedia(r.Context(), item.UserID, skill, mediaID)
				if err != nil {
					httpx.InternalError(w, r, h.logger, err)
					return
				}
				if !allowed {
					httpx.WriteError(w, r, 404, "MEDIA_NOT_FOUND", "Media was not found", nil)
					return
				}
			}
			if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/recordings") {
				if !h.limit(w, r, "uploads:"+item.UserID.String()+":"+chi.URLParam(r, "attemptID"), 6, trialTTL) {
					return
				}
				r.Body = http.MaxBytesReader(w, r.Body, (12<<20)+(1<<20))
			}
			if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/submit") {
				if !h.limit(w, r, "submits:"+item.UserID.String()+":"+chi.URLParam(r, "attemptID"), 3, trialTTL) {
					return
				}
			}
			if strings.HasSuffix(r.URL.Path, "/answers") || strings.HasSuffix(r.URL.Path, "/submit") {
				r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
			}
			w.Header().Set("Cache-Control", "private, no-store")
			next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), item.UserID, "GUEST")))
		})
		return auth.AuthenticateOptional(tokens)(fallback)
	}
}
func allowedRoute(r *http.Request) bool {
	route := chi.RouteContext(r.Context()).RoutePattern()
	switch route {
	case "/api/v1/full-mock-sessions/{sessionID}", "/api/v1/full-mock-sessions/{sessionID}/sections/{sectionPosition}",
		"/api/v1/attempts/{attemptID}", "/api/v1/attempts/{attemptID}/status", "/api/v1/attempts/{attemptID}/material",
		"/api/v1/attempts/{attemptID}/recordings/{partID}", "/api/v1/listening/media/{mediaID}", "/api/v1/writing/media/{mediaID}":
		return r.Method == http.MethodGet
	case "/api/v1/full-mock-sessions/{sessionID}/advance", "/api/v1/full-mock-sessions/{sessionID}/finish",
		"/api/v1/full-mock-sessions/{sessionID}/pause",
		"/api/v1/attempts/{attemptID}/recordings", "/api/v1/attempts/{attemptID}/submit":
		return r.Method == http.MethodPost
	case "/api/v1/attempts/{attemptID}/answers":
		return r.Method == http.MethodPost || r.Method == http.MethodPut
	default:
		return false
	}
}

func publicCatalogRoute(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	switch chi.RouteContext(r.Context()).RoutePattern() {
	case "/api/v1/listening/tests", "/api/v1/reading/materials", "/api/v1/writing/materials", "/api/v1/speaking/materials":
		return true
	default:
		return false
	}
}
