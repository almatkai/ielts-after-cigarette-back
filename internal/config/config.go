package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Environment           string
	HTTPAddr              string
	DatabaseURL           string
	RedisURL              string
	JWTSecret             string
	JWTIssuer             string
	JWTAudience           string
	AccessTokenTTL        time.Duration
	RefreshTokenTTL       time.Duration
	RefreshCookieName     string
	RefreshCookieSecure   bool
	RefreshCookieSameSite string
	CORSAllowedOrigins    []string
	ShutdownTimeout       time.Duration
	RequestTimeout        time.Duration
	MaxRequestBody        int64
	AuthRateLimit         int64
	AuthRateWindow        time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		Environment:           env("APP_ENV", "development"),
		HTTPAddr:              env("HTTP_ADDR", ":8080"),
		DatabaseURL:           os.Getenv("DATABASE_URL"),
		RedisURL:              os.Getenv("REDIS_URL"),
		JWTSecret:             os.Getenv("JWT_SECRET"),
		JWTIssuer:             env("JWT_ISSUER", "ielts-api"),
		JWTAudience:           env("JWT_AUDIENCE", "ielts-web"),
		RefreshCookieName:     env("REFRESH_COOKIE_NAME", "ielts_refresh"),
		RefreshCookieSameSite: strings.ToLower(env("REFRESH_COOKIE_SAME_SITE", "lax")),
		CORSAllowedOrigins:    splitCSV(env("CORS_ALLOWED_ORIGINS", "http://localhost:3000")),
	}

	var err error
	if cfg.AccessTokenTTL, err = durationEnv("ACCESS_TOKEN_TTL", 15*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.RefreshTokenTTL, err = durationEnv("REFRESH_TOKEN_TTL", 30*24*time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout, err = durationEnv("SHUTDOWN_TIMEOUT", 10*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.RequestTimeout, err = durationEnv("REQUEST_TIMEOUT", 15*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.AuthRateWindow, err = durationEnv("AUTH_RATE_WINDOW", time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.MaxRequestBody, err = int64Env("MAX_REQUEST_BODY_BYTES", 1<<20); err != nil {
		return Config{}, err
	}
	if cfg.AuthRateLimit, err = int64Env("AUTH_RATE_LIMIT", 10); err != nil {
		return Config{}, err
	}
	if cfg.RefreshCookieSecure, err = boolEnv("REFRESH_COOKIE_SECURE", false); err != nil {
		return Config{}, err
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	var problems []string
	if c.DatabaseURL == "" {
		problems = append(problems, "DATABASE_URL is required")
	}
	if c.RedisURL == "" {
		problems = append(problems, "REDIS_URL is required")
	}
	if len(c.JWTSecret) < 32 {
		problems = append(problems, "JWT_SECRET must contain at least 32 characters")
	}
	if strings.EqualFold(c.Environment, "production") && strings.Contains(c.JWTSecret, "change-me") {
		problems = append(problems, "JWT_SECRET must not use a development value in production")
	}
	if c.JWTIssuer == "" {
		problems = append(problems, "JWT_ISSUER is required")
	}
	if c.JWTAudience == "" {
		problems = append(problems, "JWT_AUDIENCE is required")
	}
	if c.RefreshCookieName == "" || strings.ContainsAny(c.RefreshCookieName, " ;,\r\n\t") {
		problems = append(problems, "REFRESH_COOKIE_NAME is invalid")
	}
	switch c.RefreshCookieSameSite {
	case "lax", "strict":
	case "none":
		if !c.RefreshCookieSecure {
			problems = append(problems, "REFRESH_COOKIE_SECURE must be true when SameSite=None")
		}
	default:
		problems = append(problems, "REFRESH_COOKIE_SAME_SITE must be lax, strict, or none")
	}
	if len(c.CORSAllowedOrigins) == 0 {
		problems = append(problems, "CORS_ALLOWED_ORIGINS must contain at least one origin")
	}
	for _, origin := range c.CORSAllowedOrigins {
		if origin == "*" {
			problems = append(problems, "wildcard CORS origin is not allowed")
		}
	}
	if c.AccessTokenTTL <= 0 || c.RefreshTokenTTL <= 0 {
		problems = append(problems, "token TTL values must be positive")
	}
	if c.MaxRequestBody <= 0 {
		problems = append(problems, "MAX_REQUEST_BODY_BYTES must be positive")
	}
	if c.AuthRateLimit <= 0 || c.AuthRateWindow <= 0 {
		problems = append(problems, "auth rate limit values must be positive")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}

func int64Env(key string, fallback int64) (int64, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}

func boolEnv(key string, fallback bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}

func splitCSV(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, strings.TrimRight(item, "/"))
		}
	}
	return result
}
