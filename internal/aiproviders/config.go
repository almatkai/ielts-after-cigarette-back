package aiproviders

import (
	"log/slog"
	"strings"

	"github.com/almatkai/ielts-after-cigarette-back/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

func NewConfigured(pool *pgxpool.Pool, cfg config.Config, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	cipher, err := NewCipher(cfg.AIProviderEncryptionKey, cfg.AIProviderPreviousEncryptionKey)
	if err != nil {
		logger.Error("AI provider encryption configuration is invalid")
	}
	service := NewService(NewRepository(pool), cipher, Provider{Endpoint: cfg.AIChatCompletionsURL, Model: cfg.AIModel, SpeakingModel: cfg.AISpeakingModel, APIKey: cfg.AIAPIKey, TimeoutSeconds: int(cfg.AITimeout.Seconds())}, logger)
	if pool != nil {
		service.routing = &postgresRoutingStore{pool: pool}
	}
	service.telemetryOptional = cfg.AIProviderTelemetryOptional && strings.EqualFold(cfg.Environment, "development")
	return service
}
