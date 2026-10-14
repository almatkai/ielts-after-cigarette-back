package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	adminapi "github.com/almatkai/ielts-after-cigarette-back/internal/admin"
	"github.com/almatkai/ielts-after-cigarette-back/internal/ailimits"
	"github.com/almatkai/ielts-after-cigarette-back/internal/aiproviders"
	"github.com/almatkai/ielts-after-cigarette-back/internal/analytics"
	"github.com/almatkai/ielts-after-cigarette-back/internal/assistant"
	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/blog"
	"github.com/almatkai/ielts-after-cigarette-back/internal/cache"
	"github.com/almatkai/ielts-after-cigarette-back/internal/config"
	"github.com/almatkai/ielts-after-cigarette-back/internal/dashboard"
	"github.com/almatkai/ielts-after-cigarette-back/internal/fullmock"
	"github.com/almatkai/ielts-after-cigarette-back/internal/guest"
	"github.com/almatkai/ielts-after-cigarette-back/internal/health"
	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/almatkai/ielts-after-cigarette-back/internal/jobs"
	"github.com/almatkai/ielts-after-cigarette-back/internal/listening"
	"github.com/almatkai/ielts-after-cigarette-back/internal/objectstorage"
	"github.com/almatkai/ielts-after-cigarette-back/internal/observability"
	"github.com/almatkai/ielts-after-cigarette-back/internal/phoneverification"
	"github.com/almatkai/ielts-after-cigarette-back/internal/reading"
	"github.com/almatkai/ielts-after-cigarette-back/internal/speaking"
	"github.com/almatkai/ielts-after-cigarette-back/internal/user"
	"github.com/almatkai/ielts-after-cigarette-back/internal/waitlist"
	"github.com/almatkai/ielts-after-cigarette-back/internal/writing"
	"github.com/almatkai/ielts-after-cigarette-back/internal/writingpipeline"
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
	sharedObjectStores ...objectstorage.Store,
) http.Handler {
	var sharedObjectStore objectstorage.Store
	if len(sharedObjectStores) > 0 {
		sharedObjectStore = sharedObjectStores[0]
	}
	return NewWithOptions(cfg, pool, redisClient, logger, Options{ObjectStore: sharedObjectStore})
}

type Options struct {
	ObjectStore   objectstorage.Store
	GradingCache  *cache.JSON
	Metrics       *observability.Metrics
	WorkerContext context.Context
}

func NewWithOptions(cfg config.Config, pool *pgxpool.Pool, redisClient *redis.Client, logger *slog.Logger, options Options) http.Handler {
	sharedObjectStore := options.ObjectStore
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
	blogRepository := blog.NewPostgresRepository(pool)
	blogObjectStore := sharedObjectStore
	if blogObjectStore == nil {
		blogObjectStore = objectstorage.NewFileStore(cfg.BlogMediaDir)
	}
	speakingObjectStore := sharedObjectStore
	if speakingObjectStore == nil {
		speakingObjectStore = objectstorage.NewFileStore(cfg.SpeakingMediaDir)
	}
	usersHandler := adminapi.NewUsersHandler(pool, map[string]objectstorage.Store{"blog": blogObjectStore, "speaking": speakingObjectStore}, redisClient, logger, cfg.SuperAdminEmails)
	if pool != nil {
		tokens.WithAccountState(usersHandler.AccountState)
		cleanupContext := options.WorkerContext
		if cleanupContext == nil {
			cleanupContext = context.Background()
		}
		usersHandler.StartCleanup(cleanupContext)
	}
	blogService := blog.NewService(blogRepository, blogObjectStore)
	blogHandler := blog.NewHandler(blogService, logger, cfg.MaxRequestBody, cfg.MaxMediaUploadBytes)
	readingRepository := reading.NewPostgresRepository(pool)
	readingService := reading.NewService(readingRepository)
	readingHandler := reading.NewHandler(readingService, logger, cfg.MaxRequestBody)
	writingRepository := writing.NewPostgresRepository(pool)
	writingObjectStore := sharedObjectStore
	if writingObjectStore == nil {
		writingObjectStore = objectstorage.NewFileStore(cfg.WritingMediaDir)
	}
	writingService := writing.NewService(writingRepository, writingObjectStore)
	writingHandler := writing.NewHandler(writingService, logger, cfg.MaxRequestBody, cfg.MaxMediaUploadBytes)
	speakingRepository := speaking.NewPostgresRepository(pool)
	speakingService := speaking.NewService(speakingRepository)
	speakingHandler := speaking.NewHandler(speakingService, logger, cfg.MaxRequestBody)
	listeningRepository := listening.NewPostgresRepository(pool)
	listeningService := listening.NewService(listeningRepository, cfg.ListeningMediaDir)
	if sharedObjectStore != nil {
		listeningService = listening.NewServiceWithStorage(listeningRepository, sharedObjectStore)
	}
	sttService := listening.NewSTTService(
		cfg.STTAPIURL,
		cfg.STTAPIKey,
		cfg.AIChatCompletionsURL,
		cfg.AIAPIKey,
		cfg.AIModel,
	)
	listeningService.WithSTTService(sttService)
	listeningHandler := listening.NewHandler(listeningService, logger, cfg.MaxRequestBody, cfg.MaxMediaUploadBytes)
	attemptsRepository := attempts.NewPostgresRepository(pool)
	speakingMediaLimit := cfg.MaxMediaUploadBytes
	if speakingMediaLimit < 1 || speakingMediaLimit > 12<<20 {
		speakingMediaLimit = 12 << 20
	}
	aiProviders := aiproviders.NewConfigured(pool, cfg, logger)
	aiProvidersHandler := aiproviders.NewHandler(aiProviders, logger)
	aiEvaluator := attempts.NewChatCompletionsEvaluator(cfg.AIChatCompletionsURL, cfg.AIAPIKey, cfg.AIModel, &http.Client{Timeout: cfg.AITimeout}).
		WithSpeakingModel(cfg.AISpeakingModel).
		WithSpeakingAudio(cfg.AISpeakingAudioEnabled && !cfg.SpeechEnabled).WithProviders(aiProviders)
	dailyLimiter := cache.NewDailyLimiter(redisClient)
	aiLimitsRepo := ailimits.NewPostgresRepository(pool)
	aiLimitsService := ailimits.NewService(aiLimitsRepo, dailyLimiter, redisClient, ailimits.Limits{
		AssistantLimit:      cfg.DailyLimitAssistant,
		GuestAssistantLimit: cfg.DailyLimitGuestAssistant,
		WritingLimit:        cfg.DailyLimitWriting,
		SpeakingLimit:       cfg.DailyLimitSpeaking,
	})
	aiLimitsHandler := ailimits.NewHandler(aiLimitsService, logger)

	attemptsService := attempts.NewService(attemptsRepository, map[string]attempts.MaterialProvider{
		attempts.MaterialListening: attempts.NewListeningProvider(listeningService),
		attempts.MaterialReading:   attempts.NewReadingProvider(readingService),
		attempts.MaterialWriting:   attempts.NewWritingProvider(writingService),
		attempts.MaterialSpeaking:  attempts.NewSpeakingProvider(speakingService),
	}, aiEvaluator).WithGradingCache(options.GradingCache).WithDailyLimiter(aiLimitsService)
	if sharedObjectStore != nil {
		attemptsService.WithSpeakingObjectStore(sharedObjectStore, speakingMediaLimit, aiEvaluator)
	} else {
		attemptsService.WithSpeakingRecordingStore(cfg.SpeakingMediaDir, speakingMediaLimit, aiEvaluator)
	}
	if sttService != nil {
		attemptsService.WithSpeakingTranscriber(&speakingSTTAdapter{stt: sttService})
	}
	if cfg.SpeechEnabled {
		attemptsService.WithSpeakingPipeline(jobs.NewSpeakingQueue(redisClient))
	}
	if redisClient != nil {
		writingQueue := jobs.NewWritingQueue(redisClient)
		attemptsService.WithWritingPipeline(writingQueue)

		if !cfg.WritingWorkerExternal {
			writingWorker := writingpipeline.NewWorker(
				attemptsRepository,
				attempts.NewWritingProvider(writingService),
				aiEvaluator,
				writingQueue,
				logger,
			)
			workerCtx := options.WorkerContext
			if workerCtx == nil {
				workerCtx = context.Background()
			}
			go func() {
				if err := writingWorker.Run(workerCtx); err != nil && !errors.Is(err, context.Canceled) {
					logger.Error("in-process writing worker stopped", "error", err)
				}
			}()
		}
	}
	attemptsHandler := attempts.NewHandler(attemptsService, logger, cfg.MaxRequestBody).WithSpeakingMedia(speakingMediaLimit)
	fullMockRepository := fullmock.NewPostgresRepository(pool)
	if cfg.GuestTrialEnabled {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := fullMockRepository.PrepareGuestMock(ctx); err != nil {
			logger.Error("fixed guest mock unavailable", "error", err)
		}
		cancel()
	}
	fullMockService := fullmock.NewService(fullMockRepository, attemptsService)
	attemptsService.SetExamGuard(fullMockService)
	fullMockHandler := fullmock.NewHandler(fullMockService, logger, cfg.MaxRequestBody)

	assistantService := assistant.NewService(cfg.AIChatCompletionsURL, cfg.AIAPIKey, cfg.AIModel, &http.Client{Timeout: cfg.AITimeout}).WithProviders(aiProviders)
	assistantHandler := assistant.NewHandler(assistantService, logger, cfg.MaxRequestBody)

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

	analyticsTracker := analytics.NewTracker(redisClient, pool, logger)
	analyticsHandler := analytics.NewHandler(analytics.NewRepository(pool), analyticsTracker, logger, cfg.MaxRequestBody)

	healthChecks := []health.Check{}
	if sharedObjectStore != nil {
		healthChecks = append(healthChecks, sharedObjectStore.Check)
	}
	healthHandler := health.NewHandler(pool.Ping, func(ctx context.Context) error { return cache.Ping(ctx, redisClient) }, healthChecks...)
	rateLimiter := cache.NewRateLimiter(redisClient)
	guestHandler := guest.NewHandler(guest.Config{
		Enabled: cfg.GuestTrialEnabled, Secure: cfg.RefreshCookieSecure, SameSite: cookieSameSite(cfg.RefreshCookieSameSite),
		Secret: cfg.JWTSecret, SiteKey: cfg.TurnstileSiteKey, TurnstileSecret: cfg.TurnstileSecretKey,
		Hostnames: cfg.TurnstileHostnames, Origins: cfg.CORSAllowedOrigins,
		IPLimit: cfg.GuestTrialIPLimit, GlobalLimit: cfg.GuestTrialGlobalLimit,
		ExamTypes: cfg.GuestTrialExamTypes,
	}, guest.NewPostgresRepository(pool), fullMockService, rateLimiter.Allow, remoteIP, logger)

	router := chi.NewRouter()
	router.Use(httpx.RequestIDMiddleware)
	if options.Metrics != nil {
		router.Use(options.Metrics.Middleware)
	}
	router.Use(httpx.Recover(logger))
	router.Use(httpx.AccessLog(logger))
	router.Use(httpx.CORS(cfg.CORSAllowedOrigins, httpx.FormPostOrigin{
		Path: "/api/v1/auth/google", Origin: "https://accounts.google.com",
	}))
	router.Use(timeoutByRequest(cfg.RequestTimeout, cfg.MediaUploadTimeout, max(cfg.AITimeout+15*time.Second, aiproviders.ChainTimeout+15*time.Second)))
	// History endpoints answer with tens of kilobytes to megabytes of JSON
	// (a mistakes page carries the review of every attempt), so compress the
	// text responses. Audio and other binary media keep their own types and are
	// passed through untouched.
	router.Use(chimiddleware.Compress(5, "application/json", "text/plain"))

	router.Get("/health/live", healthHandler.Live)
	router.Get("/health/ready", healthHandler.Ready)

	router.Route("/api/v1", func(api chi.Router) {
		api.With(rateLimit(rateLimiter, logger, cfg, "phone-send")).Post("/phone-verifications", phoneHandler.Send)
		api.With(rateLimit(rateLimiter, logger, cfg, "phone-confirm")).Post("/phone-verifications/{verificationID}/confirm", phoneHandler.Confirm)
		api.With(rateLimit(rateLimiter, logger, cfg, "waitlist")).Post("/waitlist", waitlistHandler.Join)
		api.With(rateLimit(rateLimiter, logger, cfg, "waitlist")).Post("/waitlist/check", waitlistHandler.Check)
		api.With(auth.Authenticate(tokens), rateLimit(rateLimiter, logger, cfg, "assistant"), dailyAssistantLimit(aiLimitsService, logger)).Post("/assistant/chat", assistantHandler.Chat)
		api.With(auth.Authenticate(tokens), rateLimit(rateLimiter, logger, cfg, "assistant"), dailyAssistantLimit(aiLimitsService, logger)).Post("/assistant/chat/stream", assistantHandler.Stream)
		api.Get("/guest/config", guestHandler.Config)
		api.Get("/guest/session", guestHandler.Session)
		api.Post("/guest/start", guestHandler.Start)
		api.With(auth.Authenticate(tokens), rateLimit(rateLimiter, logger, cfg, "auth")).Post("/guest/claim", guestHandler.Claim)

		api.With(auth.AuthenticateOptional(tokens), analyticsRateLimit(rateLimiter)).Post("/analytics/ping", analyticsHandler.Ping)

		// Public blog
		api.With(auth.AuthenticateOptional(tokens)).Get("/blog/posts", blogHandler.ListPublished)
		api.With(auth.AuthenticateOptional(tokens)).Get("/blog/posts/{slug}", blogHandler.GetPublic)
		api.With(auth.AuthenticateOptional(tokens)).Get("/blog/media/{mediaID}", blogHandler.Media)

		api.Route("/auth", func(public chi.Router) {
			// No password registration or login routes: Google is the sole public entry point.
			public.With(rateLimit(rateLimiter, logger, cfg, "login")).Post("/google", authHandler.GoogleLogin)
			public.With(rateLimit(rateLimiter, logger, cfg, "login")).Get("/google/pending", authHandler.PendingGoogleRegistration)
			public.With(rateLimit(rateLimiter, logger, cfg, "register")).Post("/google/complete", authHandler.CompleteGoogleRegistration)
			public.With(rateLimit(rateLimiter, logger, cfg, "refresh")).Post("/refresh", authHandler.Refresh)
			public.Post("/logout", authHandler.Logout)
		})

		api.Group(func(protected chi.Router) {
			protected.Use(guestHandler.Authenticate(tokens))
			protected.Use(analyticsTracker.Middleware)
			protected.Get("/full-mocks/overview", fullMockHandler.Overview)
			protected.Post("/full-mocks/start", fullMockHandler.StartGenerated)
			protected.Get("/full-mock-sessions/{sessionID}", fullMockHandler.GetSession)
			protected.Get("/full-mock-sessions/{sessionID}/sections/{sectionPosition}", fullMockHandler.Section)
			protected.Post("/full-mock-sessions/{sessionID}/advance", fullMockHandler.Advance)
			protected.Post("/full-mock-sessions/{sessionID}/finish", fullMockHandler.Finish)
			protected.Post("/full-mock-sessions/{sessionID}/pause", fullMockHandler.Pause)
			protected.Get("/listening/tests", listeningHandler.ListPublic)
			protected.Get("/listening/tests/{testID}", listeningHandler.GetPublic)
			protected.Get("/listening/media/{mediaID}", listeningHandler.Media)
			protected.Post("/listening/tests/{testID}/attempts", attemptsHandler.Start)
			protected.Get("/reading/materials", readingHandler.ListPublic)
			protected.Get("/reading/materials/{materialID}", readingHandler.GetPublic)
			protected.Post("/reading/materials/{materialID}/attempts", attemptsHandler.StartReading)
			protected.Get("/writing/materials", writingHandler.ListPublic)
			protected.Get("/writing/materials/{materialID}", writingHandler.GetPublic)
			protected.Get("/writing/media/{mediaID}", writingHandler.Media)
			protected.Post("/writing/materials/{materialID}/attempts", attemptsHandler.StartWriting)
			protected.Get("/speaking/materials", speakingHandler.ListPublic)
			protected.Get("/speaking/materials/{materialID}", speakingHandler.GetPublic)
			protected.Post("/speaking/materials/{materialID}/attempts", attemptsHandler.StartSpeaking)
			protected.Put("/attempts/{attemptID}/answers", attemptsHandler.SaveAnswers)
			protected.Post("/attempts/{attemptID}/answers", attemptsHandler.SaveAnswers)
			protected.Post("/attempts/{attemptID}/recordings", attemptsHandler.UploadSpeakingRecording)
			protected.Get("/attempts/{attemptID}/recordings/{partID}", attemptsHandler.SpeakingRecording)
			protected.Post("/attempts/{attemptID}/submit", attemptsHandler.Submit)
			protected.Get("/attempts/mistakes", attemptsHandler.Mistakes)
			protected.Get("/attempts/mistakes/attempts", attemptsHandler.MistakeAttempts)
			protected.Get("/attempts/{attemptID}/mistakes", attemptsHandler.MistakeDetail)
			protected.Get("/attempts", attemptsHandler.List)
			protected.Get("/attempts/{attemptID}", attemptsHandler.Get)
			protected.Get("/attempts/{attemptID}/status", attemptsHandler.Status)
			protected.Get("/attempts/{attemptID}/material", attemptsHandler.Material)
			protected.Get("/users/me", authHandler.Me)
			protected.Get("/profile", userHandler.Get)
			protected.Patch("/profile", userHandler.UpdateProfile)
			protected.Put("/profile/goal", userHandler.UpdateGoal)
			protected.Get("/dashboard", dashboardHandler.Get)

			// Writer applications (any authenticated user)
			protected.Post("/blog/media", blogHandler.UploadMedia)
			protected.Post("/writers/applications", blogHandler.Apply)
			protected.Get("/writers/applications/mine", blogHandler.MyApplication)
			protected.Get("/writers/applications/{applicationID}/certificate", blogHandler.Certificate)

			// Writer workspace
			protected.Route("/writer", func(writerRouter chi.Router) {
				writerRouter.Use(auth.RequireAnyRole(auth.RoleWriter, auth.RoleAdmin))
				writerRouter.Get("/posts", blogHandler.ListMine)
				writerRouter.Post("/posts", blogHandler.Create)
				writerRouter.Put("/posts/{postID}", blogHandler.Update)
				writerRouter.Post("/posts/{postID}/publish", blogHandler.Publish)
				writerRouter.Post("/posts/{postID}/archive", blogHandler.Archive)
			})

			protected.Route("/admin", func(adminRouter chi.Router) {
				adminRouter.Use(auth.RequireAdminWorkspace)
				adminRouter.Get("/access", adminHandler.Access)
				adminRouter.Group(func(users chi.Router) {
					users.Use(auth.RequireAnyRole(auth.RoleAdmin))
					users.Use(func(next http.Handler) http.Handler {
						return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							w.Header().Set("Cache-Control", "no-store")
							next.ServeHTTP(w, r)
						})
					})
					users.Get("/users", usersHandler.ListUsers)
					users.Get("/users/{userID}", usersHandler.GetUser)
					users.Put("/users/{userID}", usersHandler.UpdateUser)
					users.Delete("/users/{userID}", usersHandler.DeleteUser)
					users.Post("/users/{userID}/password", usersHandler.ChangePassword)
					users.Post("/users/{userID}/revoke-sessions", usersHandler.RevokeSessions)
					users.Get("/users/{userID}/attempts", usersHandler.UserAttempts)
					users.Get("/users/{userID}/attempts/{attemptID}", usersHandler.UserAttempt)
				})
				adminRouter.Group(func(aiAdmin chi.Router) {
					aiAdmin.Use(auth.RequireAnyRole(auth.RoleAdmin))
					aiAdmin.Get("/ai-providers", aiProvidersHandler.List)
					aiAdmin.Post("/ai-providers", aiProvidersHandler.Save)
					aiAdmin.Post("/ai-providers/test", aiProvidersHandler.Test)
					aiAdmin.Post("/ai-providers/order", aiProvidersHandler.Reorder)
					aiAdmin.Get("/ai-providers/routing", aiProvidersHandler.Routing)
					aiAdmin.Put("/ai-providers/routing", aiProvidersHandler.SaveRouting)
					aiAdmin.Get("/ai-providers/stats", aiProvidersHandler.Stats)
					aiAdmin.Put("/ai-providers/{providerID}", aiProvidersHandler.Save)
					aiAdmin.Delete("/ai-providers/{providerID}", aiProvidersHandler.Delete)
					aiAdmin.Post("/ai-providers/{providerID}/test", aiProvidersHandler.Test)
					aiAdmin.Get("/ai-limits", aiLimitsHandler.Get)
					aiAdmin.Put("/ai-limits", aiLimitsHandler.Update)
				})
				adminRouter.Group(func(content chi.Router) {
					content.Use(auth.RequireAnyPermission(auth.PermissionContentEditor))
					content.Get("/full-mocks", fullMockHandler.List)
					content.Get("/full-mocks/{mockID}", fullMockHandler.Get)
					content.Post("/full-mocks/{mockID}/archive", fullMockHandler.Archive)
					content.Get("/reading/materials", readingHandler.List)
					content.Post("/reading/materials", readingHandler.Create)
					content.Post("/reading/import/parse", readingHandler.ParseImport)
					content.Post("/reading/import", readingHandler.BulkImport)
					content.Get("/reading/materials/{materialID}", readingHandler.Get)
					content.Get("/reading/materials/{materialID}/preview", readingHandler.Preview)
					content.Put("/reading/materials/{materialID}", readingHandler.Update)
					content.Post("/reading/materials/{materialID}/publish", readingHandler.Publish)
					content.Post("/reading/materials/{materialID}/archive", readingHandler.Archive)
					content.Get("/writing/materials", writingHandler.List)
					content.Post("/writing/materials", writingHandler.Create)
					content.Post("/writing/import/parse", writingHandler.ParseImport)
					content.Post("/writing/import", writingHandler.BulkImport)
					content.Post("/writing/media", writingHandler.UploadMedia)
					content.Get("/writing/materials/{materialID}", writingHandler.Get)
					content.Put("/writing/materials/{materialID}", writingHandler.Update)
					content.Post("/writing/materials/{materialID}/publish", writingHandler.Publish)
					content.Post("/writing/materials/{materialID}/archive", writingHandler.Archive)
					content.Get("/speaking/materials", speakingHandler.List)
					content.Post("/speaking/materials", speakingHandler.Create)
					content.Post("/speaking/import/parse", speakingHandler.ParseImport)
					content.Post("/speaking/import", speakingHandler.BulkImport)
					content.Get("/speaking/materials/{materialID}", speakingHandler.Get)
					content.Put("/speaking/materials/{materialID}", speakingHandler.Update)
					content.Post("/speaking/materials/{materialID}/publish", speakingHandler.Publish)
					content.Post("/speaking/materials/{materialID}/archive", speakingHandler.Archive)
					content.Get("/listening/tests", listeningHandler.ListAdmin)
					content.Post("/listening/tests", listeningHandler.Create)
					content.Get("/listening/tests/{testID}", listeningHandler.GetAdmin)
					content.Get("/listening/tests/{testID}/preview", listeningHandler.Preview)
					content.Put("/listening/tests/{testID}", listeningHandler.Update)
					content.Post("/listening/import/parse", listeningHandler.ParseImport)
					content.Post("/listening/import", listeningHandler.Import)
					content.Post("/listening/media", listeningHandler.UploadMedia)
					content.Post("/listening/tests/{testID}/transcribe", listeningHandler.Transcribe)
					content.Post("/listening/tests/{testID}/publish", listeningHandler.Publish)
					content.Post("/listening/tests/{testID}/archive", listeningHandler.Archive)
				})
				adminRouter.With(auth.RequireAnyRole(auth.RoleAdmin)).Get("/analytics/overview", analyticsHandler.Overview)
				adminRouter.With(auth.RequireAnyRole(auth.RoleAdmin)).Get("/analytics/realtime", analyticsHandler.Realtime)
				adminRouter.With(auth.RequireAnyRole(auth.RoleAdmin)).Get("/analytics/export/{dataset}", analyticsHandler.Export)
				adminRouter.With(auth.RequireAnyRole(auth.RoleAdmin)).Get("/waitlist", waitlistHandler.AdminList)
				adminRouter.With(auth.RequireAnyRole(auth.RoleAdmin)).Get("/super-admins", waitlistHandler.AdminListAdmins)
				adminRouter.With(auth.RequireAnyRole(auth.RoleAdmin)).Post("/super-admins", waitlistHandler.AdminAddAdmin)
				adminRouter.With(auth.RequireAnyRole(auth.RoleAdmin)).Delete("/super-admins/{email}", waitlistHandler.AdminRemoveAdmin)

				// Blog moderation and writer applications review.
				adminRouter.Group(func(blogModeration chi.Router) {
					blogModeration.Use(auth.RequireAnyPermission(auth.PermissionBlogModerator))
					blogModeration.Get("/blog/posts", blogHandler.List)
					blogModeration.Get("/blog/posts/{postID}", blogHandler.Get)
					blogModeration.Put("/blog/posts/{postID}", blogHandler.Update)
					blogModeration.Post("/blog/posts/{postID}/publish", blogHandler.Publish)
					blogModeration.Post("/blog/posts/{postID}/archive", blogHandler.Archive)
					blogModeration.Get("/writers/applications", blogHandler.ListApplications)
					blogModeration.Get("/writers/applications/{applicationID}", blogHandler.GetApplication)
					blogModeration.Post("/writers/applications/{applicationID}/approve", blogHandler.ApproveApplication)
					blogModeration.Post("/writers/applications/{applicationID}/reject", blogHandler.RejectApplication)
				})
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

// Media uploads can legitimately take longer than ordinary JSON requests,
// especially when the object store is outside the local network. Keep the
// normal API deadline strict while giving only upload endpoints more time.
func timeoutByRequest(requestTimeout, mediaUploadTimeout, aiEvaluationTimeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		regular := chimiddleware.Timeout(requestTimeout)(next)
		mediaUpload := chimiddleware.Timeout(mediaUploadTimeout)(next)
		aiEvaluation := chimiddleware.Timeout(aiEvaluationTimeout)(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isMediaUpload(r) {
				mediaUpload.ServeHTTP(w, r)
				return
			}
			if isAIEvaluation(r) {
				aiEvaluation.ServeHTTP(w, r)
				return
			}
			regular.ServeHTTP(w, r)
		})
	}
}

func isAIEvaluation(r *http.Request) bool {
	if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/v1/attempts/") && strings.HasSuffix(r.URL.Path, "/submit") {
		return true
	}
	if r.Method == http.MethodPost && (r.URL.Path == "/api/v1/assistant/chat" || r.URL.Path == "/api/v1/assistant/chat/stream" || strings.HasPrefix(r.URL.Path, "/api/v1/admin/ai-providers")) {
		return true
	}
	return false
}

func isMediaUpload(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	if r.URL.Path == "/api/v1/admin/listening/media" {
		return true
	}
	if r.URL.Path == "/api/v1/admin/writing/media" {
		return true
	}
	if r.URL.Path == "/api/v1/blog/media" {
		return true
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/admin/listening/tests/") && strings.HasSuffix(r.URL.Path, "/transcribe") {
		return true
	}
	return strings.HasPrefix(r.URL.Path, "/api/v1/attempts/") && strings.HasSuffix(r.URL.Path, "/recordings")
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

// analyticsRateLimit caps heartbeats per IP so a script cannot inflate the
// online counter. Unlike auth limits it fails open: analytics is optional.
func analyticsRateLimit(limiter limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			allowed, err := limiter.Allow(r.Context(), "rate-limit:public:analytics:"+remoteIP(r), 60, time.Minute)
			if err == nil && !allowed {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func dailyAssistantLimit(
	limitsService *ailimits.Service,
	logger *slog.Logger,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if limitsService == nil {
				next.ServeHTTP(w, r)
				return
			}
			role := auth.Role(r.Context())
			if role == auth.RoleAdmin {
				next.ServeHTTP(w, r)
				return
			}
			var scope, id string
			if userID, ok := auth.UserID(r.Context()); ok {
				scope = "assistant:user"
				id = userID.String()
			} else {
				scope = "assistant:guest"
				id = remoteIP(r)
			}

			res, err := limitsService.Allow(r.Context(), scope, id)
			if err != nil {
				logger.ErrorContext(r.Context(), "daily assistant limiter unavailable", "error", err)
				httpx.WriteError(w, r, http.StatusServiceUnavailable, "RATE_LIMITER_UNAVAILABLE", "Rate limiter service unavailable", nil)
				return
			}
			if !res.Allowed {
				httpx.WriteError(
					w, r,
					http.StatusTooManyRequests,
					"DAILY_LIMIT_EXCEEDED",
					fmt.Sprintf("Достигнут дневной лимит сообщений ассистента (%d в день). Лимит обновится в полночь.", res.Limit),
					map[string]string{
						"limit":     strconv.FormatInt(res.Limit, 10),
						"remaining": strconv.FormatInt(res.Remaining, 10),
						"resetAt":   res.ResetAt.Format(time.RFC3339),
					},
				)
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

type speakingSTTAdapter struct {
	stt *listening.STTService
}

func (a *speakingSTTAdapter) Transcribe(ctx context.Context, filename string, audioStream io.Reader) (string, error) {
	res, err := a.stt.Transcribe(ctx, filename, audioStream)
	if err != nil {
		return "", err
	}
	return res.Text, nil
}
