package config

import (
	"strings"
	"testing"
	"time"
)

func TestBCCEnvironmentPlumbing(t *testing.T) {
	for k, v := range map[string]string{"DATABASE_URL": "postgres://test", "REDIS_URL": "redis://test", "JWT_SECRET": "test-secret-at-least-32-characters-long", "PHONE_VERIFICATION_SECRET": "test-phone-secret-at-least-32-characters-long", "APP_ENV": "development", "GUEST_TRIAL_ENABLED": "false", "PAYMENT_PROVIDER": "bcc", "PAYMENT_RETURN_URL": "https://app.example/app/billing/return", "BCC_GATEWAY": "https://test3ds.bcc.kz:5445/cgi-bin/cgi_link", "BCC_TERMINAL": "88888881", "BCC_MERCHANT": "00000001", "BCC_MERCHANT_NAME": "IELTS TEST", "BCC_MAC_KEY": "6BB0AC02E47BDF73D98FEB777F3B5294", "BCC_NOTIFY_URL": "https://pay.example:443/api/v1/billing/callbacks/bcc", "BCC_NOTIFY_USER": "bank-notify", "BCC_NOTIFY_PASSWORD": "test-only-long-password", "BCC_PUBLIC_BASE_URL": "https://pay.example", "BCC_BRIDGE_KEY": strings.Repeat("ab", 32), "BCC_BRIDGE_TTL": "10m", "BCC_LANGUAGE": "en", "BCC_TRUSTED_PROXY_CIDRS": "10.1.0.0/24, 127.0.0.1/32"} {
		t.Setenv(k, v)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.PaymentProvider != "bcc" || c.BCC.BridgeTTL != 10*time.Minute || c.BCC.Language != "en" || len(c.BCC.TrustedProxyCIDRs) != 2 || c.BCC.NotifyUser != "bank-notify" || c.BCC.NotifyPassword != "test-only-long-password" || c.BCC.Merchant != "00000001" || c.BCC.MerchantName != "IELTS TEST" {
		t.Fatal("BCC environment fields were not loaded")
	}
	t.Setenv("BCC_BRIDGE_TTL", "bad")
	if _, err := Load(); err == nil {
		t.Fatal("malformed TTL accepted")
	}
	t.Setenv("BCC_BRIDGE_TTL", "10m")
	t.Setenv("APP_ENV", "production")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "production BCC") {
		t.Fatal("public test settings accepted in production")
	}
}
