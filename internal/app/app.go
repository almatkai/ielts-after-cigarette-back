package app

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	adminapi "github.com/almatkai/ielts-after-cigarette-back/internal/admin"
	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/cache"
	"github.com/almatkai/ielts-after-cigarette-back/internal/config"
	"github.com/almatkai/ielts-after-cigarette-back/internal/dashboard"
	"github.com/almatkai/ielts-after-cigarette-back/internal/health"
	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/almatkai/ielts-after-cigarette-back/internal/phoneverification"
	"github.com/almatkai/ielts-after-cigarette-back/internal/reading"
	"github.com/almatkai/ielts-after-cigarette-back/internal/user"
	"github.com/almatkai/ielts-after-cigarette-back/internal/waitlist"
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type limiter interface {
	Allow(context.Context, string, int64, time.Duration) (bool, error)
}

func New(
	cfg config.Config,
	pool *pgxpool.Pool,
	redisClient *redis.Client,
	logger *slog.Logger,
) http.Handler {
	tokens := auth.NewTokenManager(
		cfg.JWTSecret,
		cfg.JWTIssuer,
		cfg.JWTAudience,
		cfg.AccessTokenTTL,
		cfg.RefreshTokenTTL,
	)

	authRepository := auth.NewPostgresRepository(pool)
	waitlistRepository := waitlist.NewPostgresRepository(pool)
	authService := auth.NewService(authRepository, tokens).WithGoogleLogin(
		waitlist.NewGoogleTokenVerifier(cfg.GoogleClientID),
		newSuperAdminChecker(cfg.SuperAdminEmails, waitlistRepository),
	)
	authHandler := auth.NewHandler(
		authService,
		logger,
		cfg.MaxRequestBody,
		auth.CookieConfig{
			Name:     cfg.RefreshCookieName,
			Secure:   cfg.RefreshCookieSecure,
			SameSite: cookieSameSite(cfg.RefreshCookieSameSite),
			MaxAge:   cfg.RefreshTokenTTL,
		},
	)

	userRepository := user.NewPostgresRepository(pool)
	userHandler := user.NewHandler(user.NewService(userRepository), logger, cfg.MaxRequestBody)

	dashboardRepository := dashboard.NewPostgresRepository(pool)
	dashboardHandler := dashboard.NewHandler(dashboard.NewService(dashboardRepository), logger)
	adminHandler := adminapi.NewHandler()
	readingRepository := reading.NewPostgresRepository(pool)
	readingHandler := reading.NewHandler(reading.NewService(readingRepository), logger, cfg.MaxRequestBody)

	phoneRepository := phoneverification.NewPostgresRepository(pool)
	infobipAPIKey := cfg.InfobipAPIKey
	if !cfg.InfobipEnabled {
		infobipAPIKey = ""
	}
	phoneSender := phoneverification.NewInfobipSender(
		cfg.InfobipBaseURL,
		infobipAPIKey,
		cfg.InfobipWhatsAppSender,
		cfg.InfobipWhatsAppTemplate,
		cfg.InfobipWhatsAppLanguage,
		&http.Client{Timeout: cfg.InfobipTimeout},
	)
	phoneHandler := phoneverification.NewHandler(
		phoneverification.NewService(
			phoneRepository,
			phoneSender,
			cfg.PhoneVerificationSecret,
			cfg.PhoneCodeTTL,
			cfg.PhoneTokenTTL,
			cfg.PhoneResendInterval,
			int(cfg.PhoneMaxAttempts),
		),
		logger,
		cfg.MaxRequestBody,
	)
	waitlistHandler := waitlist.NewHandler(waitlist.NewService(waitlistRepository, waitlist.NewGoogleTokenVerifier(cfg.GoogleClientID), cfg.SuperAdminEmails), logger, cfg.MaxRequestBody)

	healthHandler := health.NewHandler(
		pool.Ping,
		func(ctx context.Context) error { return cache.Ping(ctx, redisClient) },
	)
	rateLimiter := cache.NewRateLimiter(redisClient)

	router := chi.NewRouter()
	router.Use(httpx.RequestIDMiddleware)
	router.Use(httpx.Recover(logger))
	router.Use(httpx.AccessLog(logger))
	router.Use(httpx.CORS(cfg.CORSAllowedOrigins))
	router.Use(chimiddleware.Timeout(cfg.RequestTimeout))

	router.Get("/health/live", healthHandler.Live)
	router.Get("/health/ready", healthHandler.Ready)

	router.Route("/api/v1", func(api chi.Router) {
		api.With(rateLimit(rateLimiter, logger, cfg, "phone-send")).Post("/phone-verifications", phoneHandler.Send)
		api.With(rateLimit(rateLimiter, logger, cfg, "phone-confirm")).Post("/phone-verifications/{verificationID}/confirm", phoneHandler.Confirm)
		api.With(rateLimit(rateLimiter, logger, cfg, "waitlist")).Post("/waitlist", waitlistHandler.Join)
		api.With(rateLimit(rateLimiter, logger, cfg, "waitlist")).Post("/waitlist/check", waitlistHandler.Check)

		api.Route("/auth", func(public chi.Router) {
			public.With(rateLimit(rateLimiter, logger, cfg, "register")).Post("/register", authHandler.Register)
			public.With(rateLimit(rateLimiter, logger, cfg, "login")).Post("/login", authHandler.Login)
			public.With(rateLimit(rateLimiter, logger, cfg, "login")).Post("/google", authHandler.GoogleLogin)
			public.With(rateLimit(rateLimiter, logger, cfg, "register")).Post("/google/complete", authHandler.CompleteGoogleRegistration)
			public.With(rateLimit(rateLimiter, logger, cfg, "refresh")).Post("/refresh", authHandler.Refresh)
			public.Post("/logout", authHandler.Logout)
		})

		api.Group(func(protected chi.Router) {
			protected.Use(auth.Authenticate(tokens))
			protected.Get("/users/me", authHandler.Me)
			protected.Get("/profile", userHandler.Get)
			protected.Patch("/profile", userHandler.UpdateProfile)
			protected.Put("/profile/goal", userHandler.UpdateGoal)
			protected.Get("/dashboard", dashboardHandler.Get)
			protected.Route("/admin", func(adminRouter chi.Router) {
				adminRouter.Use(auth.RequireAnyRole(auth.RoleEditor, auth.RoleAdmin))
				adminRouter.Get("/access", adminHandler.Access)
				adminRouter.Get("/reading/materials", readingHandler.List)
				adminRouter.Post("/reading/materials", readingHandler.Create)
				adminRouter.Get("/reading/materials/{materialID}", readingHandler.Get)
				adminRouter.Put("/reading/materials/{materialID}", readingHandler.Update)
				adminRouter.With(auth.RequireAnyRole(auth.RoleAdmin)).Post("/reading/materials/{materialID}/publish", readingHandler.Publish)
				adminRouter.With(auth.RequireAnyRole(auth.RoleAdmin)).Get("/waitlist", waitlistHandler.AdminList)
				adminRouter.With(auth.RequireAnyRole(auth.RoleAdmin)).Get("/super-admins", waitlistHandler.AdminListAdmins)
				adminRouter.With(auth.RequireAnyRole(auth.RoleAdmin)).Post("/super-admins", waitlistHandler.AdminAddAdmin)
				adminRouter.With(auth.RequireAnyRole(auth.RoleAdmin)).Delete("/super-admins/{email}", waitlistHandler.AdminRemoveAdmin)
			})
		})
	})

	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "Resource was not found", nil)
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "HTTP method is not allowed", nil)
	})
	return router
}

func rateLimit(
	limiter limiter,
	logger *slog.Logger,
	cfg config.Config,
	scope string,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := fmt.Sprintf("rate-limit:public:%s:%s", scope, remoteIP(r))
			allowed, err := limiter.Allow(r.Context(), key, cfg.AuthRateLimit, cfg.AuthRateWindow)
			if err != nil {
				logger.ErrorContext(r.Context(), "public rate limiter unavailable",
					"request_id", httpx.RequestID(r.Context()),
					"error", err,
				)
				httpx.WriteError(w, r, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "Service is temporarily unavailable", nil)
				return
			}
			if !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(int(cfg.AuthRateWindow.Seconds())))
				httpx.WriteError(w, r, http.StatusTooManyRequests, "RATE_LIMITED", "Too many authentication attempts", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// remoteIP returns the client IP for rate limiting. The API is served behind
// nginx, which sets X-Forwarded-For, so the direct peer is the proxy's docker
// address. Trust the header only when the peer is private or loopback —
// otherwise direct callers could spoof it to dodge the rate limiter.
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	if isTrustedProxy(host) {
		if forwarded := firstForwardedFor(r.Header.Get("X-Forwarded-For")); forwarded != "" {
			return forwarded
		}
	}
	return host
}

func firstForwardedFor(header string) string {
	first, _, _ := strings.Cut(header, ",")
	return strings.TrimSpace(first)
}

func isTrustedProxy(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsPrivate() || ip.IsLoopback()
}

// superAdminChecker answers whether an email may sign in as a platform admin:
// bootstrap accounts come from SUPER_ADMIN_EMAILS, runtime accounts from the
// super_admins table managed through the waitlist admin API.
type superAdminChecker struct {
	env  map[string]struct{}
	repo *waitlist.PostgresRepository
}

func newSuperAdminChecker(envEmails []string, repo *waitlist.PostgresRepository) *superAdminChecker {
	env := make(map[string]struct{}, len(envEmails))
	for _, email := range envEmails {
		if email = strings.ToLower(strings.TrimSpace(email)); email != "" {
			env[email] = struct{}{}
		}
	}
	return &superAdminChecker{env: env, repo: repo}
}

func (c *superAdminChecker) IsSuperAdmin(ctx context.Context, email string) (bool, error) {
	if _, ok := c.env[email]; ok {
		return true, nil
	}
	return c.repo.IsAdmin(ctx, email)
}

func cookieSameSite(value string) http.SameSite {
	switch value {
	case "strict":
		return http.SameSiteStrictMode
	case "none":
		return http.SameSiteNoneMode
	default:
		return http.SameSiteLaxMode
	}
}
