package admin

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
)

func TestUserUpdateNormalizesCombinedPermissions(t *testing.T) {
	input := UserUpdate{DisplayName: "Writer", Email: "writer@example.test", Role: auth.RoleWriter, Permissions: []string{" content_editor ", "BLOG_MODERATOR", "CONTENT_EDITOR"}}
	if err := input.validate(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(input.Permissions, []string{auth.PermissionBlogModerator, auth.PermissionContentEditor}) {
		t.Fatalf("permissions: %v", input.Permissions)
	}
	input.Permissions = []string{"ADMIN"}
	if err := input.validate(); err == nil {
		t.Fatal("unknown capability accepted")
	}
}

func TestSavingPermissionsRevokesSessionsAndKeepsBaseRole(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	actor, target := testdb.User(t, pool), testdb.User(t, pool)
	h := NewUsersHandler(pool, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	tokens := auth.NewTokenManager("test-secret", "test", "test", time.Hour, time.Hour).WithAccountState(h.AccountState)
	save := func(permissions []string) {
		t.Helper()
		body, _ := json.Marshal(UserUpdate{DisplayName: "Автор и редактор", Email: target.String() + "@example.test", Role: auth.RoleWriter, Permissions: permissions})
		w := userRequest(h, actor, target, "PUT", "", string(body), h.UpdateUser)
		if w.Code != 200 {
			t.Fatalf("save: %d %s", w.Code, w.Body.String())
		}
		var result struct {
			Role        string
			Permissions []string
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Role != auth.RoleWriter || !reflect.DeepEqual(result.Permissions, permissions) {
			t.Fatalf("saved: %+v", result)
		}
	}
	save([]string{auth.PermissionBlogModerator, auth.PermissionContentEditor})
	old, _, err := tokens.NewAccessToken(target, auth.RoleWriter)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO refresh_sessions(id,user_id,token_hash,expires_at) VALUES(gen_random_uuid(),$1,'test-hash',CURRENT_TIMESTAMP+INTERVAL '1 day')`, target); err != nil {
		t.Fatal(err)
	}
	save([]string{auth.PermissionContentEditor})
	if _, err := tokens.ParseAccessToken(old); err == nil {
		t.Fatal("removed capability remains in active token")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM refresh_sessions WHERE user_id=$1`, target).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("refresh sessions survived capability change")
	}
	fresh, _, err := tokens.NewAccessToken(target, auth.RoleWriter, []string{auth.PermissionBlogModerator})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := tokens.ParseAccessToken(fresh)
	if err != nil || !reflect.DeepEqual(claims.Permissions, []string{auth.PermissionContentEditor}) {
		t.Fatalf("new claims: %+v %v", claims, err)
	}
	save([]string{})
}

func TestEditorPermissionMigrationPreservesAccessAndInvalidatesOldTokens(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	apply := func(name string) {
		t.Helper()
		sql, err := os.ReadFile("../../migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatal(err)
		}
	}
	apply("000037_editor_permissions.down.sql")
	editor, student := testdb.User(t, pool), testdb.User(t, pool)
	if _, err := pool.Exec(ctx, `UPDATE users SET role='EDITOR' WHERE id=$1`, editor); err != nil {
		t.Fatal(err)
	}
	tokens := auth.NewTokenManager("test-secret", "test", "test", time.Hour, time.Hour)
	old, _, err := tokens.NewAccessToken(editor, auth.RoleEditor)
	if err != nil {
		t.Fatal(err)
	}
	apply("000037_editor_permissions.up.sql")
	h := NewUsersHandler(pool, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	state, err := h.AccountState(editor)
	if err != nil {
		t.Fatal(err)
	}
	if state.Version != 1 || !reflect.DeepEqual(state.Permissions, []string{auth.PermissionBlogModerator, auth.PermissionContentEditor}) {
		t.Fatalf("editor migration: %+v", state)
	}
	studentState, err := h.AccountState(student)
	if err != nil || len(studentState.Permissions) != 0 || studentState.Version != 0 {
		t.Fatalf("student migration: %+v %v", studentState, err)
	}
	tokens.WithAccountState(h.AccountState)
	if _, err := tokens.ParseAccessToken(old); err == nil {
		t.Fatal("pre-migration token remains valid")
	}
}
