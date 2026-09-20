package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/almatkai/ielts-after-cigarette-back/internal/cache"
	"github.com/almatkai/ielts-after-cigarette-back/internal/config"
	"github.com/almatkai/ielts-after-cigarette-back/internal/database"
	"github.com/almatkai/ielts-after-cigarette-back/internal/jobs"
	"github.com/almatkai/ielts-after-cigarette-back/internal/objectstorage"
	"github.com/almatkai/ielts-after-cigarette-back/internal/speaking"
	"github.com/almatkai/ielts-after-cigarette-back/internal/speakingpipeline"
	"github.com/almatkai/ielts-after-cigarette-back/internal/speech"
)

func main() { os.Exit(run()) }

func run() int {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("load configuration", "error", err)
		return 1
	}
	if !cfg.SpeechEnabled {
		logger.Error("speaking worker requires SPEECH_ENABLED=true")
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	startupCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	pool, err := database.Open(startupCtx, cfg.DatabaseURL)
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
	repository := attempts.NewPostgresRepository(pool)
	evaluator := attempts.NewChatCompletionsEvaluator(cfg.AIChatCompletionsURL, cfg.AIAPIKey, cfg.AIModel, &http.Client{Timeout: cfg.AITimeout}).
		WithSpeakingModel(cfg.AISpeakingModel).WithSpeakingAudio(false)
	speechClient := speech.NewClient(cfg.SpeechServiceURL, cfg.SpeechServiceToken, &http.Client{Timeout: cfg.SpeechTimeout})
	worker := speakingpipeline.NewWorker(repository, provider, store, speechClient, evaluator, jobs.NewSpeakingQueue(redisClient), logger)
	logger.Info("speaking worker started")
	if err := worker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("speaking worker stopped", "error", err)
		return 1
	}
	return 0
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
