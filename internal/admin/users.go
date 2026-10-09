package admin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/almatkai/ielts-after-cigarette-back/internal/objectstorage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

type UsersHandler struct {
	pool            *pgxpool.Pool
	stores          map[string]objectstorage.Store
	redis           *redis.Client
	logger          *slog.Logger
	protectedEmails map[string]struct{}
}

func NewUsersHandler(pool *pgxpool.Pool, stores map[string]objectstorage.Store, client *redis.Client, logger *slog.Logger, protectedLists ...[]string) *UsersHandler {
	h := &UsersHandler{pool: pool, stores: stores, redis: client, logger: logger, protectedEmails: map[string]struct{}{}}
	for _, list := range protectedLists {
		for _, email := range list {
			h.protectedEmails[strings.ToLower(strings.TrimSpace(email))] = struct{}{}
		}
	}
	return h
}

// AccountState is used both on token issuance and validation. No credential hashes leave the server.
func (h *UsersHandler) AccountState(id uuid.UUID) (auth.AccountState, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var state auth.AccountState
	err := h.pool.QueryRow(ctx, `SELECT token_version, role, blocked, admin_permissions FROM users WHERE id=$1`, id).Scan(&state.Version, &state.Role, &state.Blocked, &state.Permissions)
	return state, err
}

const userJSON = `jsonb_build_object(
 'id',u.id,'email',u.email,'phone',u.phone,'displayName',COALESCE(p.display_name, NULLIF(concat_ws(' ',u.first_name,u.last_name),''),u.email),
 'firstName',u.first_name,'lastName',u.last_name,'role',u.role,'status',u.status,'blocked',u.blocked,
	'permissions',COALESCE(u.admin_permissions,'{}'),
 'createdAt',u.created_at,'updatedAt',GREATEST(u.updated_at,p.updated_at),'source',u.source,
 'referralCode',u.referral_code,'referredByCode',u.referred_by_code,'termsAcceptedAt',u.terms_accepted_at,
 'googleConnected',u.google_sub IS NOT NULL,'hasPassword',u.password_hash IS NOT NULL,
 'currentBand',p.current_band,'targetBand',p.target_band,'examDate',p.exam_date,'examType',p.exam_type,'timezone',p.timezone,
 'completedTests',(SELECT count(*) FROM attempts a WHERE a.user_id=u.id AND a.status='SUBMITTED'),
 'unfinishedTests',(SELECT count(*) FROM attempts a WHERE a.user_id=u.id AND a.status='IN_PROGRESS'),
 'lastActiveAt',(SELECT max(seen) FROM (SELECT max(first_seen_at) seen FROM user_activity_days WHERE user_id=u.id UNION ALL SELECT max(created_at) FROM refresh_sessions WHERE user_id=u.id) recent))`

func (h *UsersHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	if page > 100000 {
		page = 100000
	}
	search := strings.TrimSpace(r.URL.Query().Get("q"))
	role := auth.NormalizeRole(r.URL.Query().Get("role"))
	if len(search) > 200 || (role != "" && !auth.ValidRole(role)) {
		httpx.WriteError(w, r, 422, "VALIDATION_ERROR", "Некорректный фильтр", nil)
		return
	}
	// Literal matching: percent/underscore in user input never become wildcards.
	search = strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(search)
	where := ` WHERE ($1='' OR concat_ws(' ',u.email,u.phone,p.display_name,u.first_name,u.last_name) ILIKE '%'||$1||'%') AND ($2='' OR u.role=$2)`
	rows, err := h.pool.Query(r.Context(), `SELECT `+userJSON+` FROM users u LEFT JOIN user_profiles p ON p.user_id=u.id`+where+` ORDER BY u.created_at DESC,u.id LIMIT 30 OFFSET $3`, search, role, (page-1)*30)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var item any
		if err = rows.Scan(&item); err != nil {
			h.fail(w, r, err)
			return
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		h.fail(w, r, err)
		return
	}
	var total int
	err = h.pool.QueryRow(r.Context(), `SELECT count(*) FROM users u LEFT JOIN user_profiles p ON p.user_id=u.id`+where, search, role).Scan(&total)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"items": items, "total": total, "page": page, "pageSize": 30})
}

func userParam(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		httpx.WriteError(w, r, 400, "INVALID_ID", "Некорректный ID", nil)
		return uuid.Nil, false
	}
	return id, true
}
func (h *UsersHandler) GetUser(w http.ResponseWriter, r *http.Request) {
	id, ok := userParam(w, r)
	if !ok {
		return
	}
	var result any
	err := h.pool.QueryRow(r.Context(), `SELECT `+userJSON+` || jsonb_build_object(
 'skills',COALESCE((SELECT jsonb_agg(jsonb_build_object('skill',skill,'band',estimated_band,'accuracy',accuracy_percent,'completedTasks',completed_tasks)) FROM user_skill_progress WHERE user_id=u.id),'[]'::jsonb),
 'activityDays',COALESCE((SELECT jsonb_agg(day ORDER BY day DESC) FROM user_activity_days WHERE user_id=u.id),'[]'::jsonb),
 'sessions',COALESCE((SELECT jsonb_agg(jsonb_build_object('createdAt',created_at,'expiresAt',expires_at,'revokedAt',revoked_at,'userAgent',user_agent,'ipAddress',ip_address) ORDER BY created_at DESC) FROM (SELECT * FROM refresh_sessions WHERE user_id=u.id ORDER BY created_at DESC LIMIT 30) s),'[]'::jsonb))
 FROM users u LEFT JOIN user_profiles p ON p.user_id=u.id WHERE u.id=$1`, id).Scan(&result)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, 200, result)
}

type UserUpdate struct {
	DisplayName string   `json:"displayName"`
	Email       string   `json:"email"`
	Phone       string   `json:"phone"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
	Blocked     bool     `json:"blocked"`
}

func samePermissions(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (v *UserUpdate) validate() error {
	v.DisplayName = strings.TrimSpace(v.DisplayName)
	v.Email = strings.ToLower(strings.TrimSpace(v.Email))
	v.Phone = strings.TrimSpace(v.Phone)
	v.Role = auth.NormalizeRole(v.Role)
	permissions := make([]string, 0, len(v.Permissions))
	seen := make(map[string]struct{}, len(v.Permissions))
	for _, value := range v.Permissions {
		value = auth.NormalizeRole(value)
		if !auth.ValidPermission(value) {
			return fmt.Errorf("Некорректный доступ")
		}
		if _, ok := seen[value]; !ok {
			permissions = append(permissions, value)
			seen[value] = struct{}{}
		}
	}
	sort.Strings(permissions)
	v.Permissions = permissions
	address, err := mail.ParseAddress(v.Email)
	if v.DisplayName == "" || utf8.RuneCountInString(v.DisplayName) > 100 || err != nil || address.Address != v.Email || len(v.Email) > 254 || !auth.ValidRole(v.Role) {
		return fmt.Errorf("Проверьте имя, email и роль")
	}
	if v.Phone != "" {
		if len(v.Phone) < 8 || len(v.Phone) > 16 || v.Phone[0] != '+' {
			return fmt.Errorf("Телефон: + и код страны")
		}
		for _, c := range v.Phone[1:] {
			if c < '0' || c > '9' {
				return fmt.Errorf("Некорректный телефон")
			}
		}
	}
	return nil
}
func (h *UsersHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := userParam(w, r)
	if !ok {
		return
	}
	var input UserUpdate
	if err := httpx.DecodeJSON(w, r, 4096, &input); err != nil {
		httpx.WriteError(w, r, 422, "VALIDATION_ERROR", "Проверьте поля", nil)
		return
	}
	if err := input.validate(); err != nil {
		httpx.WriteError(w, r, 422, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	actor, _ := auth.UserID(r.Context())
	if actor == id && (input.Blocked || input.Role != auth.RoleAdmin) {
		httpx.WriteError(w, r, 409, "SELF_CHANGE", "Нельзя заблокировать себя или снять свою роль ADMIN", nil)
		return
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())
	var email, role string
	var permissions []string
	var blocked bool
	err = tx.QueryRow(r.Context(), `SELECT email,role,blocked,admin_permissions FROM users WHERE id=$1 FOR UPDATE`, id).Scan(&email, &role, &blocked, &permissions)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	// Allowlist admins must be managed through the dedicated administrators page.
	if role != input.Role || email != input.Email {
		var allowed bool
		err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM super_admins WHERE email=$1)`, email).Scan(&allowed)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		_, protected := h.protectedEmails[email]
		if allowed || protected {
			httpx.WriteError(w, r, 409, "ADMIN_ALLOWLIST", "Сначала измените доступ в разделе «Администраторы»", nil)
			return
		}
	}
	sort.Strings(permissions)
	revoke := email != input.Email || role != input.Role || blocked != input.Blocked || !samePermissions(permissions, input.Permissions)
	_, err = tx.Exec(r.Context(), `UPDATE users SET email=$2,phone=NULLIF($3,''),role=$4,blocked=$5,admin_permissions=$6,token_version=token_version+CASE WHEN $7 THEN 1 ELSE 0 END WHERE id=$1`, id, input.Email, input.Phone, input.Role, input.Blocked, input.Permissions, revoke)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO user_profiles(user_id,display_name) VALUES($1,$2) ON CONFLICT(user_id) DO UPDATE SET display_name=$2`, id, input.DisplayName)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if revoke {
		_, err = tx.Exec(r.Context(), `DELETE FROM refresh_sessions WHERE user_id=$1`, id)
		if err != nil {
			h.fail(w, r, err)
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		h.fail(w, r, err)
		return
	}
	h.GetUser(w, r)
}
func (h *UsersHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	id, ok := userParam(w, r)
	if !ok {
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if err := httpx.DecodeJSON(w, r, 2048, &input); err != nil || len(input.Password) < 8 || len(input.Password) > 72 {
		httpx.WriteError(w, r, 422, "VALIDATION_ERROR", "Пароль: 8–72 байта", nil)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.invalidate(w, r, id, string(hash))
}
func (h *UsersHandler) RevokeSessions(w http.ResponseWriter, r *http.Request) {
	id, ok := userParam(w, r)
	if ok {
		h.invalidate(w, r, id, "")
	}
}
func (h *UsersHandler) invalidate(w http.ResponseWriter, r *http.Request, id uuid.UUID, hash string) {
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())
	tag, err := tx.Exec(r.Context(), `UPDATE users SET token_version=token_version+1,password_hash=CASE WHEN $2='' THEN password_hash ELSE $2 END WHERE id=$1`, id, hash)
	if err == nil && tag.RowsAffected() == 0 {
		err = pgx.ErrNoRows
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	_, err = tx.Exec(r.Context(), `DELETE FROM refresh_sessions WHERE user_id=$1`, id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *UsersHandler) UserAttempts(w http.ResponseWriter, r *http.Request) {
	id, ok := userParam(w, r)
	if !ok {
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	if page > 100000 {
		page = 100000
	}
	var exists bool
	err := h.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1)`, id).Scan(&exists)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if !exists {
		h.fail(w, r, pgx.ErrNoRows)
		return
	}
	var items any
	var total int
	err = h.pool.QueryRow(r.Context(), `SELECT COALESCE(jsonb_agg(row ORDER BY started DESC),'[]'::jsonb) FROM (
 SELECT a.started_at started,jsonb_build_object('id',a.id,'skill',a.material_type,'status',a.status,'band',a.band,'score',a.score,'maxScore',a.max_score,'startedAt',a.started_at,'submittedAt',a.submitted_at,'fullMock',f.session_id,
 'title',COALESCE(l.title,rd.title,wr.title,sp.title,'Тест')) row FROM attempts a
 LEFT JOIN listening_test_versions l ON a.material_type='listening' AND l.id=a.material_version_id
 LEFT JOIN reading_material_versions rd ON a.material_type='reading' AND rd.id=a.material_version_id
 LEFT JOIN writing_material_versions wr ON a.material_type='writing' AND wr.id=a.material_version_id
 LEFT JOIN speaking_material_versions sp ON a.material_type='speaking' AND sp.id=a.material_version_id
 LEFT JOIN full_mock_session_sections f ON f.attempt_id=a.id
 WHERE a.user_id=$1 ORDER BY a.started_at DESC,a.id LIMIT 30 OFFSET $2) history`, id, (page-1)*30).Scan(&items)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	err = h.pool.QueryRow(r.Context(), `SELECT count(*) FROM attempts WHERE user_id=$1`, id).Scan(&total)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"items": items, "total": total, "page": page, "pageSize": 30})
}
func (h *UsersHandler) UserAttempt(w http.ResponseWriter, r *http.Request) {
	id, ok := userParam(w, r)
	if !ok {
		return
	}
	attempt, err := uuid.Parse(chi.URLParam(r, "attemptID"))
	if err != nil {
		httpx.WriteError(w, r, 400, "INVALID_ID", "Некорректный ID", nil)
		return
	}
	var result any
	err = h.pool.QueryRow(r.Context(), `SELECT jsonb_build_object('attempt',to_jsonb(a),'answers',COALESCE((SELECT jsonb_agg(to_jsonb(ans) || jsonb_build_object('prompt',COALESCE(rq.prompt,lq.prompt))) FROM attempt_answers ans LEFT JOIN reading_questions rq ON rq.id=ans.question_id LEFT JOIN listening_questions lq ON lq.id=ans.question_id WHERE ans.attempt_id=a.id),'[]'::jsonb),'writing', (SELECT to_jsonb(e) FROM writing_evaluations e WHERE attempt_id=a.id),'speaking',(SELECT to_jsonb(e) FROM speaking_evaluations e WHERE attempt_id=a.id)) FROM attempts a WHERE a.id=$1 AND a.user_id=$2`, attempt, id).Scan(&result)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, 200, result)
}

func (h *UsersHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := userParam(w, r)
	if !ok {
		return
	}
	actor, _ := auth.UserID(r.Context())
	if id == actor {
		httpx.WriteError(w, r, 409, "SELF_DELETE", "Нельзя удалить свой аккаунт", nil)
		return
	}
	var input struct {
		Email string `json:"email"`
	}
	if err := httpx.DecodeJSON(w, r, 1024, &input); err != nil {
		httpx.WriteError(w, r, 422, "VALIDATION_ERROR", "Введите email для подтверждения", nil)
		return
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())
	var email string
	var phone, referral *string
	err = tx.QueryRow(r.Context(), `SELECT email,phone,referral_code FROM users WHERE id=$1 FOR UPDATE`, id).Scan(&email, &phone, &referral)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if strings.ToLower(strings.TrimSpace(input.Email)) != email {
		httpx.WriteError(w, r, 422, "VALIDATION_ERROR", "Email не совпадает", nil)
		return
	}
	statements := []string{
		`INSERT INTO account_object_deletions SELECT 'speaking',storage_key FROM speaking_recordings WHERE attempt_id IN (SELECT id FROM attempts WHERE user_id=$1) ON CONFLICT DO NOTHING`,
		`INSERT INTO account_object_deletions SELECT 'blog',certificate_storage_key FROM writer_applications WHERE user_id=$1 ON CONFLICT DO NOTHING`,
		`INSERT INTO account_object_deletions SELECT 'blog',storage_key FROM blog_media WHERE uploaded_by=$1 ON CONFLICT DO NOTHING`,
		`DELETE FROM blog_posts WHERE author_id=$1`,
		`DELETE FROM blog_media WHERE uploaded_by=$1`,
		// Remove sections first: their attempt FK intentionally uses RESTRICT.
		`DELETE FROM full_mock_sessions WHERE user_id=$1`,
		`DELETE FROM users WHERE id=$1`,
	}
	for _, sql := range statements {
		if _, err = tx.Exec(r.Context(), sql, id); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	if _, err = tx.Exec(r.Context(), `DELETE FROM phone_verifications WHERE phone=$1`, phone); err != nil {
		h.fail(w, r, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `DELETE FROM super_admins WHERE email=$1`, email); err != nil {
		h.fail(w, r, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `UPDATE users SET referred_by_code=NULL WHERE referred_by_code=$1`, referral); err != nil {
		h.fail(w, r, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		h.fail(w, r, err)
		return
	}
	if h.redis != nil {
		h.redis.ZRem(r.Context(), "iac:analytics:online:users", id.String())
		iter := h.redis.Scan(r.Context(), 0, "*"+id.String()+"*", 100).Iterator()
		for iter.Next(r.Context()) {
			h.redis.Del(r.Context(), iter.Val())
		}
	}
	h.cleanup(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

func (h *UsersHandler) StartCleanup(ctx context.Context) {
	if h.pool == nil {
		return
	}
	go func() {
		h.cleanup(ctx)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.cleanup(ctx)
			}
		}
	}()
}
func (h *UsersHandler) cleanup(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := h.pool.Query(ctx, `SELECT store,storage_key FROM account_object_deletions LIMIT 100`)
	if err != nil {
		return
	}
	type object struct{ store, key string }
	var objects []object
	for rows.Next() {
		var o object
		if rows.Scan(&o.store, &o.key) == nil {
			objects = append(objects, o)
		}
	}
	rows.Close()
	for _, o := range objects {
		store := h.stores[o.store]
		if store == nil {
			continue
		}
		err = store.Delete(ctx, o.key)
		if err != nil && !objectstorage.IsNotFound(err) {
			h.logger.Error("purge account object", "error", err)
			continue
		}
		if _, err = h.pool.Exec(ctx, `DELETE FROM account_object_deletions WHERE store=$1 AND storage_key=$2`, o.store, o.key); err != nil {
			return
		}
	}
}
func (h *UsersHandler) fail(w http.ResponseWriter, r *http.Request, err error) {
	if httpx.ClientGone(w, r, err) {
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.WriteError(w, r, 404, "USER_NOT_FOUND", "Пользователь не найден", nil)
		return
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		httpx.WriteError(w, r, 409, "ACCOUNT_EXISTS", "Email или телефон уже занят", nil)
		return
	}
	h.logger.Error("admin users request failed", "error", err)
	httpx.WriteError(w, r, 500, "INTERNAL_ERROR", "Не удалось выполнить действие", nil)
}
