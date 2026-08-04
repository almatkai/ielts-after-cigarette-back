package admin

import (
	"context"
	"testing"
	"time"
)

type roleRepositoryStub struct {
	email string
	role  string
}

func (r *roleRepositoryStub) SetRole(_ context.Context, email, role string, _ time.Time) (RoleChange, error) {
	r.email = email
	r.role = role
	return RoleChange{Email: email, Role: role}, nil
}

func TestRoleServiceNormalizesInput(t *testing.T) {
	t.Parallel()
	repository := &roleRepositoryStub{}
	service := NewRoleService(repository)

	result, err := service.SetRole(context.Background(), "  ADMIN@Example.COM ", " editor ")
	if err != nil {
		t.Fatalf("SetRole() error = %v", err)
	}
	if result.Email != "admin@example.com" || result.Role != "EDITOR" {
		t.Fatalf("result = %#v", result)
	}
}

func TestRoleServiceRejectsUnknownRole(t *testing.T) {
	t.Parallel()
	service := NewRoleService(&roleRepositoryStub{})

	if _, err := service.SetRole(context.Background(), "admin@example.com", "OWNER"); err != ErrInvalidRole {
		t.Fatalf("SetRole() error = %v, want %v", err, ErrInvalidRole)
	}
}
