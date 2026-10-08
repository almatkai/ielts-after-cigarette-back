package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/aiproviders"
	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/almatkai/ielts-after-cigarette-back/internal/cache"
	"github.com/almatkai/ielts-after-cigarette-back/internal/config"
	"github.com/almatkai/ielts-after-cigarette-back/internal/database"
	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/almatkai/ielts-after-cigarette-back/internal/jobs"
	"github.com/almatkai/ielts-after-cigarette-back/internal/objectstorage"
	"github.com/almatkai/ielts-after-cigarette-back/internal/observability"
	"github.com/almatkai/ielts-after-cigarette-back/internal/speaking"
	"github.com/almatkai/ielts-after-cigarette-back/internal/speakingpipeline"
	"github.com/almatkai/ielts-after-cigarette-back/internal/speech"
	"github.com/almatkai/ielts-after-cigarette-back/internal/writing"
	"github.com/almatkai/ielts-after-cigarette-back/internal/writingpipeline"
)

func main() { os.Exit(run()) }

func run() int {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("load configuration", "error", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	startupCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	errorSink, flushErrors, err := observability.SentryErrorSink(cfg.SentryWorkerDSN, cfg.SentryEnvironment, cfg.SentryRelease, logger)
	if err != nil {
		logger.Error("configure error sink", "error", err)
		return 1
	}
	httpx.SetErrorReporter(errorSink)
	defer flushErrors(context.Background())
	pool, err := database.Open(startupCtx, cfg.DatabaseURL, int32(cfg.DatabaseMaxConns))
	if err != nil {
		logger.Error("connect to PostgreSQL", "error", err)
		return 1
	}
	defer pool.Close()
	redisClient, err := cache.Open(cfg.RedisURL)
	if err != nil {
		logger.Error("configure Redis", "error", err)
		return 1
	}
	defer redisClient.Close()
	repository := attempts.NewPostgresRepository(pool)
	evaluator := attempts.NewChatCompletionsEvaluator(cfg.AIChatCompletionsURL, cfg.AIAPIKey, cfg.AIModel, &http.Client{Timeout: cfg.AITimeout}).
		WithSpeakingModel(cfg.AISpeakingModel).WithSpeakingAudio(false).WithProviders(aiproviders.NewConfigured(pool, cfg, logger))
	writingRepository := writing.NewPostgresRepository(pool)
	writingProvider := attempts.NewWritingProvider(writing.NewService(writingRepository))
	writingWorker := writingpipeline.NewWorker(repository, writingProvider, evaluator, jobs.NewWritingQueue(redisClient), logger)

	workerErrors := make(chan error, 2)
	var workers sync.WaitGroup
	start := func(run func(context.Context) error) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				workerErrors <- err
			}
		}()
	}
	if cfg.SpeechEnabled {
		store, err := openStore(cfg)
		if err != nil {
			logger.Error("configure object storage", "error", err)
			return 1
		}
		if err := store.Check(startupCtx); err != nil {
			logger.Error("connect to object storage", "error", err)
			return 1
		}
		speakingRepository := speaking.NewPostgresRepository(pool)
		provider := attempts.NewSpeakingProvider(speaking.NewService(speakingRepository))
		speechClient := speech.NewClient(cfg.SpeechServiceURL, cfg.SpeechServiceToken, &http.Client{Timeout: cfg.SpeechTimeout})
		speakingWorker := speakingpipeline.NewWorker(repository, provider, store, speechClient, evaluator, jobs.NewSpeakingQueue(redisClient), logger)
		start(speakingWorker.Run)
	}
	start(writingWorker.Run)
	logger.Info("workers started", "speaking", cfg.SpeechEnabled, "writing", true)
	code := 0
	select {
	case <-ctx.Done():
	case err := <-workerErrors:
		logger.Error("worker stopped with error", "error", err)
		code = 1
	}
	stop()
	workers.Wait() // Finish cancelled calls before closing the database pool.
	return code
}

func openStore(cfg config.Config) (objectstorage.Store, error) {
	if cfg.ObjectStorageBackend == "minio" {
		return objectstorage.NewMinIOStore(objectstorage.MinIOConfig{
			Endpoint: cfg.ObjectStorageEndpoint, AccessKey: cfg.ObjectStorageAccessKey,
			SecretKey: cfg.ObjectStorageSecretKey, Bucket: cfg.ObjectStorageBucket,
			Region: cfg.ObjectStorageRegion, UseSSL: cfg.ObjectStorageUseSSL,
		})
	}
	return objectstorage.NewFileStore(cfg.SpeakingMediaDir), nil
}
