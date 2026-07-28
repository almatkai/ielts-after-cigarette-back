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
