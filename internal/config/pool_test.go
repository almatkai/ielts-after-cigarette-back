package config

import (
	"strings"
	"testing"
)

func TestDatabasePoolEnvironmentIsBounded(t *testing.T) {
	for _, value := range []string{"0", "-1", "10001", "invalid"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("DB_MAX_CONNS", value)
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), "DB_MAX_CONNS") {
				t.Fatalf("invalid pool size: %v", err)
			}
		})
	}
}
