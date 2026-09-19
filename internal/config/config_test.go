package config

import (
	"strings"
	"testing"
	"time"
)

func TestValidateRejectsUnsafeConfiguration(t *testing.T) {
	cfg := Config{
		Environment:           "production",
		DatabaseURL:           "postgres://example",
		RedisURL:              "redis://example",
		JWTSecret:             "change-me-but-this-value-is-long-enough",
		JWTIssuer:             "issuer",
		JWTAudience:           "audience",
		RefreshCookieName:     "ielts_refresh",
		RefreshCookieSameSite: "lax",
		CORSAllowedOrigins:    []string{"*"},
		AccessTokenTTL:        time.Minute,
		RefreshTokenTTL:       time.Hour,
		MaxRequestBody:        1024,
		AuthRateLimit:         1,
		AuthRateWindow:        time.Minute,
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "development value") || !strings.Contains(err.Error(), "wildcard") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateAcceptsDisabledInfobip(t *testing.T) {
	cfg := Config{
		Environment:             "production",
		DatabaseURL:             "postgres://example",
		RedisURL:                "redis://example",
		JWTSecret:               "0123456789abcdef0123456789abcdef",
		JWTIssuer:               "issuer",
		JWTAudience:             "audience",
		AccessTokenTTL:          time.Minute,
		RefreshTokenTTL:         time.Hour,
		RefreshCookieName:       "ielts_refresh",
		RefreshCookieSameSite:   "lax",
		CORSAllowedOrigins:      []string{"http://78.40.109.172"},
		MaxRequestBody:          1024,
		ObjectStorageBackend:    "filesystem",
		ListeningMediaDir:       "./var/listening-media",
		SpeakingMediaDir:        "./var/speaking-media",
		AuthRateLimit:           10,
		AuthRateWindow:          time.Minute,
		PhoneVerificationSecret: "abcdef0123456789abcdef0123456789",
		PhoneCodeTTL:            5 * time.Minute,
		PhoneTokenTTL:           10 * time.Minute,
		PhoneResendInterval:     time.Minute,
		PhoneMaxAttempts:        5,
		InfobipBaseURL:          "https://l2vz85.api.infobip.com",
		InfobipEnabled:          false,
		InfobipWhatsAppLanguage: "en",
		InfobipTimeout:          10 * time.Second,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error=%v", err)
	}
}

func TestValidateRequiresCompleteMinIOConfiguration(t *testing.T) {
	cfg := Config{ObjectStorageBackend: "minio", ObjectStorageEndpoint: "minio:9000"}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "OBJECT_STORAGE_ACCESS_KEY") {
		t.Fatalf("expected incomplete MinIO configuration error, got %v", err)
	}
}

func TestValidateRejectsInsecureProductionAIEndpoint(t *testing.T) {
	cfg := Config{
		Environment:          "production",
		AIAPIKey:             "secret",
		AIChatCompletionsURL: "http://llm.example/chat/completions",
		AIModel:              "qwen3-8",
		AISpeakingModel:      "qwen3-8",
		AITimeout:            time.Minute,
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "AI_CHAT_COMPLETIONS_URL must use https") {
		t.Fatalf("expected insecure AI endpoint error, got %v", err)
	}
}
