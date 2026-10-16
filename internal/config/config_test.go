package config

import (
	"strings"
	"testing"
	"time"
)

func TestGuestTrialConfigurationRequiresProductionProtection(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://test")
	t.Setenv("REDIS_URL", "redis://test")
	t.Setenv("JWT_SECRET", "test-secret-at-least-32-characters-long")
	t.Setenv("PHONE_VERIFICATION_SECRET", "test-phone-secret-at-least-32-characters-long")
	t.Setenv("APP_ENV", "development")
	t.Setenv("GUEST_TRIAL_ENABLED", "true")
	t.Setenv("TURNSTILE_SITE_KEY", "")
	t.Setenv("TURNSTILE_SECRET_KEY", "")
	t.Setenv("TURNSTILE_HOSTNAMES", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Environment = "production"
	if err = cfg.Validate(); err == nil || !strings.Contains(err.Error(), "guest trials require Turnstile") {
		t.Fatalf("unprotected guest launch: %v", err)
	}
	cfg.TurnstileSiteKey = "site-key"
	cfg.TurnstileSecretKey = "server-secret"
	cfg.TurnstileHostnames = []string{"app.example"}
	cfg.RefreshCookieSecure = true
	if err = cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.GuestTrialGlobalLimit = 0
	if err = cfg.Validate(); err == nil || !strings.Contains(err.Error(), "guest trial quotas must be positive") {
		t.Fatalf("unbounded guest launches allowed: %v", err)
	}
}

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
		RequestTimeout:          time.Minute,
		MediaUploadTimeout:      5 * time.Minute,
		RefreshCookieName:       "ielts_refresh",
		RefreshCookieSameSite:   "lax",
		CORSAllowedOrigins:      []string{"http://78.40.109.172"},
		MaxRequestBody:          1024,
		MaxMediaUploadBytes:     50 << 20,
		ObjectStorageBackend:    "filesystem",
		ListeningMediaDir:       "./var/listening-media",
		WritingMediaDir:         "./var/writing-media",
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
