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

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/cache"
	"github.com/almatkai/ielts-after-cigarette-back/internal/config"
	"github.com/almatkai/ielts-after-cigarette-back/internal/dashboard"
	"github.com/almatkai/ielts-after-cigarette-back/internal/health"
	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/almatkai/ielts-after-cigarette-back/internal/user"
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
	authHandler := auth.NewHandler(
		auth.NewService(authRepository, tokens),
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
		api.Route("/auth", func(public chi.Router) {
			public.With(rateLimit(rateLimiter, logger, cfg, "register")).Post("/register", authHandler.Register)
			public.With(rateLimit(rateLimiter, logger, cfg, "login")).Post("/login", authHandler.Login)
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
			key := fmt.Sprintf("rate-limit:auth:%s:%s", scope, remoteIP(r))
			allowed, err := limiter.Allow(r.Context(), key, cfg.AuthRateLimit, cfg.AuthRateWindow)
			if err != nil {
				logger.ErrorContext(r.Context(), "auth rate limiter unavailable",
					"request_id", httpx.RequestID(r.Context()),
					"error", err,
				)
				httpx.WriteError(w, r, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "Authentication service is temporarily unavailable", nil)
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

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
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
