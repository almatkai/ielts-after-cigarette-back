package config

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func TestAIEncryptionWithoutGlitchTipOnlyInExplicitDevelopment(t *testing.T) {
	for _, tc := range []struct {
		environment                   string
		optional, required, forbidden bool
	}{
		{"development", true, false, false}, {"development", false, true, false},
		{"production", true, true, true}, {"production", false, true, false}, {"staging", true, true, true},
	} {
		cfg := Config{Environment: tc.environment, AIProviderEncryptionKey: base64.StdEncoding.EncodeToString(make([]byte, 32)), AIProviderTelemetryOptional: tc.optional}
		message := fmt.Sprint(cfg.Validate())
		if strings.Contains(message, "SENTRY_DSN") != tc.required {
			t.Fatalf("unexpected DSN validation: %s optional=%t", tc.environment, tc.optional)
		}
		if strings.Contains(message, "AI_PROVIDER_TELEMETRY_OPTIONAL is only allowed") != tc.forbidden {
			t.Fatalf("production opt-out validation missing: %s", tc.environment)
		}
	}
}
