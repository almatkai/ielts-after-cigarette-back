package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/objectstorage"
	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func userRequest(h *UsersHandler, actor, target uuid.UUID, method, suffix, body string, fn http.HandlerFunc) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "/users/"+target.String()+suffix, strings.NewReader(body))
	route := chi.NewRouteContext()
	route.URLParams.Add("userID", target.String())
	if strings.HasPrefix(suffix, "/attempts/") {
		route.URLParams.Add("attemptID", strings.TrimPrefix(suffix, "/attempts/"))
	}
	request = request.WithContext(context.WithValue(auth.WithUser(request.Context(), actor, auth.RoleAdmin), chi.RouteCtxKey, route))
	w := httptest.NewRecorder()
	fn(w, request)
	return w
}
func TestUserManagementPasswordRolesAndHistory(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	actor := testdb.User(t, pool)
	target := testdb.User(t, pool)
	h := NewUsersHandler(pool, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	tokens := auth.NewTokenManager("test-secret", "test", "test", time.Hour, time.Hour).WithAccountState(h.AccountState)
	old, _, err := tokens.NewAccessToken(target, auth.RoleStudent)
	if err != nil {
		t.Fatal(err)
	}
	w := userRequest(h, actor, target, "POST", "/password", `{"password":"new-password-123"}`, h.ChangePassword)
	if w.Code != 204 {
		t.Fatalf("password: %d %s", w.Code, w.Body.String())
	}
	if _, err = tokens.ParseAccessToken(old); err == nil {
		t.Fatal("old access token remains valid")
	}
	var hash string
	if err = pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id=$1`, target).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("new-password-123")) != nil {
		t.Fatal("password not hashed")
	}
	email := target.String() + "@example.test"
	body, _ := json.Marshal(UserUpdate{DisplayName: "Тестовый автор", Email: email, Phone: "+77012345678", Role: auth.RoleWriter})
	w = userRequest(h, actor, target, "PUT", "", string(body), h.UpdateUser)
	if w.Code != 200 {
		t.Fatalf("writer: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "password_hash") {
		t.Fatal("credential leaked")
	}
	token, _, err := tokens.NewAccessToken(target, auth.RoleWriter)
	if err != nil {
		t.Fatal(err)
	}
	input := UserUpdate{DisplayName: "Тестовый автор", Email: email, Phone: "+77012345678", Role: auth.RoleWriter, Blocked: true}
	body, _ = json.Marshal(input)
	w = userRequest(h, actor, target, "PUT", "", string(body), h.UpdateUser)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if _, err = tokens.ParseAccessToken(token); err == nil {
		t.Fatal("blocked account can use token")
	}
	if _, _, err = tokens.NewAccessToken(target, auth.RoleWriter); err == nil {
		t.Fatal("blocked account can sign in")
	}
	w = httptest.NewRecorder()
	h.ListUsers(w, httptest.NewRequest("GET", "/users?q=%D0%B0%D0%B2%D1%82%D0%BE%D1%80&role=WRITER", nil))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var list struct {
		Items []map[string]any
		Total int
	}
	if err = json.Unmarshal(w.Body.Bytes(), &list); err != nil || list.Total != 1 {
		t.Fatalf("list: %s %v", w.Body.String(), err)
	}
	w = userRequest(h, actor, target, "GET", "/attempts", "", h.UserAttempts)
	if w.Code != 200 {
		t.Fatalf("attempts query: %d %s", w.Code, w.Body.String())
	}
	w = userRequest(h, actor, target, "POST", "/password", `{"password":"short"}`, h.ChangePassword)
	if w.Code != 422 {
		t.Fatal("short password accepted")
	}
}
func TestDeleteUserPurgesOwnedDataAndFiles(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	actor := testdb.User(t, pool)
	target := testdb.User(t, pool)
	store := objectstorage.NewFileStore(t.TempDir())
	h := NewUsersHandler(pool, map[string]objectstorage.Store{"blog": store, "speaking": store}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	email := target.String() + "@example.test"
	phone := "+77012345678"
	exec(`UPDATE users SET phone=$2,referral_code='purge-me' WHERE id=$1`, target, phone)
	exec(`INSERT INTO user_profiles(user_id,display_name) VALUES($1,'Erase me')`, target)
	exec(`INSERT INTO user_activity_days(day,user_id) VALUES(CURRENT_DATE,$1)`, target)
	exec(`INSERT INTO user_skill_progress(id,user_id,skill) VALUES($1,$2,'speaking')`, uuid.New(), target)
	// A publisher must be erasable while the shared published test survives.
	material, version := uuid.New(), uuid.New()
	exec(`INSERT INTO reading_materials(id,slug,exam_type,difficulty,created_by,updated_by) VALUES($1,'shared-test','academic','intermediate',$2,$2)`, material, target)
	exec(`INSERT INTO reading_material_versions(id,material_id,version_number,title,body,created_by) VALUES($1,$2,1,'Shared test','Test body',$3)`, version, material, target)
	exec(`UPDATE reading_materials SET status='PUBLISHED',current_version_id=$2,published_version_id=$2,published_at=CURRENT_TIMESTAMP,published_by=$3 WHERE id=$1`, material, version, target)
	attempt, answer := uuid.New(), uuid.New()
	exec(`INSERT INTO attempts(id,user_id,material_type,material_id,material_version_id) VALUES($1,$2,'speaking',$3,$4)`, attempt, target, uuid.New(), uuid.New())
	exec(`INSERT INTO attempt_answers(attempt_id,question_id,answer) VALUES($1,$2,'{"text":"private answer"}')`, attempt, answer)
	detailResponse := userRequest(h, actor, target, "GET", "/attempts/"+attempt.String(), "", h.UserAttempt)
	if detailResponse.Code != 200 || !strings.Contains(detailResponse.Body.String(), "private answer") {
		t.Fatalf("attempt detail: %d %s", detailResponse.Code, detailResponse.Body.String())
	}
	mock, session := uuid.New(), uuid.New()
	exec(`INSERT INTO full_mock_tests(id,slug,exam_type,title,listening_material_id,reading_material_id,writing_material_id,speaking_material_id) VALUES($1,'mock','academic','Mock',$2,$2,$2,$2)`, mock, uuid.New())
	exec(`INSERT INTO full_mock_sessions(id,mock_test_id,user_id) VALUES($1,$2,$3)`, session, mock, target)
	exec(`INSERT INTO full_mock_session_sections(session_id,position,skill,attempt_id) VALUES($1,4,'speaking',$2)`, session, attempt)
	exec(`INSERT INTO speaking_recordings(id,attempt_id,part_id,original_name,mime_type,storage_key,byte_size) VALUES($1,$2,$3,'recording.wav','audio/wav','private-recording',3)`, uuid.New(), attempt, uuid.New())
	exec(`INSERT INTO writer_applications(id,user_id,overall_band,reading_band,writing_band,speaking_band,trf_number,certificate_storage_key,certificate_mime_type) VALUES($1,$2,8,8,8,8,'private-trf','private-certificate','application/pdf')`, uuid.New(), target)
	media, post := uuid.New(), uuid.New()
	exec(`INSERT INTO blog_media(id,original_name,mime_type,storage_key,byte_size,uploaded_by) VALUES($1,'image.png','image/png','private-blog-image',3,$2)`, media, target)
	exec(`INSERT INTO blog_posts(id,author_id,slug,title,cover_media_id,body_html) VALUES($1,$2,'private-post','Private post',$3,'<p>Private</p>')`, post, target, media)
	exec(`INSERT INTO super_admins(email) VALUES($1)`, email)
	for _, key := range []string{"private-recording", "private-certificate", "private-blog-image"} {
		if _, err := store.Put(ctx, key, "application/octet-stream", bytes.NewReader([]byte("abc")), 3); err != nil {
			t.Fatal(err)
		}
	}
	w := userRequest(h, actor, target, "DELETE", "", `{"email":"wrong@example.test"}`, h.DeleteUser)
	if w.Code != 422 {
		t.Fatal("wrong confirmation accepted")
	}
	w = userRequest(h, actor, actor, "DELETE", "", `{"email":"irrelevant"}`, h.DeleteUser)
	if w.Code != 409 {
		t.Fatal("self deletion accepted")
	}
	body, _ := json.Marshal(map[string]string{"email": email})
	w = userRequest(h, actor, target, "DELETE", "", string(body), h.DeleteUser)
	if w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	for _, table := range []string{"users", "attempts", "attempt_answers", "speaking_recordings", "writer_applications", "blog_posts", "blog_media", "account_object_deletions", "full_mock_sessions", "full_mock_session_sections"} {
		var count int
		sql := "SELECT count(*) FROM " + table
		if table == "users" {
			sql += " WHERE id='" + target.String() + "'"
		}
		if err := pool.QueryRow(ctx, sql).Scan(&count); err != nil || count != 0 {
			t.Fatalf("remaining %s rows: %d (%v)", table, count, err)
		}
	}
	for _, key := range []string{"private-recording", "private-certificate", "private-blog-image"} {
		if _, err := store.Open(ctx, key); !objectstorage.IsNotFound(err) {
			t.Fatalf("object retained: %s %v", key, err)
		}
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM reading_materials WHERE id=$1 AND status='PUBLISHED' AND created_by IS NULL AND published_by IS NULL`, material).Scan(&count); err != nil || count != 1 {
		t.Fatalf("shared test corrupted: %d %v", count, err)
	}
}

// An unavailable object store cannot make an erased account recoverable, and
// must not lose the keys needed to complete the purge after a restart.
type unavailableStore struct {
	objectstorage.Store
	unavailable bool
}

func (s *unavailableStore) Delete(ctx context.Context, key string) error {
	if s.unavailable {
		return context.DeadlineExceeded
	}
	return s.Store.Delete(ctx, key)
}
func TestDeleteUserRetriesStorageCleanup(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	actor := testdb.User(t, pool)
	target := testdb.User(t, pool)
	store := &unavailableStore{Store: objectstorage.NewFileStore(t.TempDir()), unavailable: true}
	h := NewUsersHandler(pool, map[string]objectstorage.Store{"blog": store}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err := pool.Exec(ctx, `INSERT INTO blog_media(id,original_name,mime_type,storage_key,byte_size,uploaded_by) VALUES($1,'image.png','image/png','retry-object',3,$2)`, uuid.New(), target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Put(ctx, "retry-object", "image/png", bytes.NewReader([]byte("abc")), 3); err != nil {
		t.Fatal(err)
	}
	tokenManager := auth.NewTokenManager("test-secret", "test", "test", time.Hour, time.Hour).WithAccountState(h.AccountState)
	token, _, err := tokenManager.NewAccessToken(target, auth.RoleStudent)
	if err != nil {
		t.Fatal(err)
	}
	email := target.String() + "@example.test"
	body, _ := json.Marshal(map[string]string{"email": email})
	w := userRequest(h, actor, target, "DELETE", "", string(body), h.DeleteUser)
	if w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if _, err = tokenManager.ParseAccessToken(token); err == nil {
		t.Fatal("deleted account remains authenticated")
	}
	var pending int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM account_object_deletions`).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("cleanup job lost: %d %v", pending, err)
	}
	store.unavailable = false
	h.cleanup(ctx)
	if _, err = store.Open(ctx, "retry-object"); !objectstorage.IsNotFound(err) {
		t.Fatalf("purge did not retry: %v", err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM account_object_deletions`).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("cleanup remains: %d %v", pending, err)
	}
}
