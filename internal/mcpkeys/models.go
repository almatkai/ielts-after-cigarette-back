package mcpkeys

import (
	"time"

	"github.com/google/uuid"
)

type MCPKey struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	KeyPrefix  string     `json:"keyPrefix"`
	CreatedBy  uuid.UUID  `json:"createdBy"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
}

type CreateKeyInput struct {
	Name string `json:"name"`
}

type CreateKeyResponse struct {
	MCPKey
	SecretKey string `json:"secretKey"`
}

type ListKeysResponse struct {
	Keys []MCPKey `json:"keys"`
}
