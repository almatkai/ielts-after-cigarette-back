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
