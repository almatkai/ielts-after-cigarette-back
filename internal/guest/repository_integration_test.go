package guest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
)

func TestGuestRepositoryIsolatesIdentityAndRetainsConsumedTokens(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool)
	item, err := repo.Create(ctx, []byte("unique-token-hash"), "general", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	found, err := repo.Find(ctx, []byte("unique-token-hash"))
	if err != nil || found.UserID != item.UserID || found.SessionID != nil {
		t.Fatalf("find=%+v err=%v", found, err)
	}
	var role, status, exam, password string
	err = pool.QueryRow(ctx, `SELECT u.role,u.status,p.exam_type,u.password_hash FROM users u JOIN user_profiles p ON p.user_id=u.id WHERE u.id=$1`, item.UserID).Scan(&role, &status, &exam, &password)
	if err != nil || role != "GUEST" || status != "GUEST" || exam != "general" || password != "!" {
		t.Fatalf("guest provision=%s %s %s %s err=%v", role, status, exam, password, err)
	}
	if _, err = repo.Find(ctx, []byte("forged-hash")); !errors.Is(err, ErrNotFound) {
		t.Fatal("forged cookie resolved")
	}
	if _, err = repo.Create(ctx, []byte("unique-token-hash"), "academic", time.Now()); err == nil {
		t.Fatal("duplicate token accepted")
	}
	if _, err = pool.Exec(ctx, `UPDATE guest_trials SET expires_at=CURRENT_TIMESTAMP-INTERVAL '1 hour' WHERE user_id=$1`, item.UserID); err != nil {
		t.Fatal(err)
	}
	found, err = repo.Find(ctx, []byte("unique-token-hash"))
	if err != nil || found.ExpiresAt.After(time.Now()) {
		t.Fatal("expired trial must remain identifiable as consumed")
	}
	var registered int
	if err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE status='REGISTERED'`).Scan(&registered); err != nil || registered != 0 {
		t.Fatalf("guest counted as registered: %d %v", registered, err)
	}
}

func TestGuestMediaRequiresOwnedPinnedMockVersion(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool)
	actor := testdb.User(t, pool)
	other := testdb.User(t, pool)
	seed := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	listening, version, part, audio, session, attempt := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seed(`INSERT INTO listening_tests (id,slug,exam_type,created_by,updated_by) VALUES ($1,$2,'academic',$3,$3)`, listening, listening.String(), actor)
	seed(`INSERT INTO listening_test_versions (id,test_id,version_number,title,created_by) VALUES ($1,$2,1,'Listening',$3)`, version, listening, actor)
	seed(`INSERT INTO listening_media (id,kind,original_name,mime_type,storage_key,byte_size,created_by) VALUES ($1,'audio','test.webm','audio/webm',$2,1,$3)`, audio, audio.String(), actor)
	seed(`INSERT INTO listening_parts (id,test_version_id,position,audio_asset_id) VALUES ($1,$2,1,$3)`, part, version, audio)
	seed(`INSERT INTO full_mock_sessions (id,user_id,exam_type,title,duration_minutes) VALUES ($1,$2,'academic','Guest',165)`, session, actor)
	seed(`INSERT INTO attempts (id,user_id,material_type,material_id,material_version_id) VALUES ($1,$2,'listening',$3,$4)`, attempt, actor, listening, version)
	seed(`INSERT INTO full_mock_session_sections (session_id,position,skill,attempt_id) VALUES ($1,1,'listening',$2)`, session, attempt)
	writing, writingVersion, writingAttempt, image := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seed(`INSERT INTO writing_materials (id,slug) VALUES ($1,$2)`, writing, writing.String())
	seed(`INSERT INTO writing_material_versions (id,material_id,version_number,exam_type,difficulty,title,tasks) VALUES ($1,$2,1,'academic','intermediate','Writing',jsonb_build_array(jsonb_build_object('visualAssetId',$3::text)))`, writingVersion, writing, image)
	seed(`INSERT INTO attempts (id,user_id,material_type,material_id,material_version_id) VALUES ($1,$2,'writing',$3,$4)`, writingAttempt, actor, writing, writingVersion)
	seed(`INSERT INTO full_mock_session_sections (session_id,position,skill,attempt_id) VALUES ($1,3,'writing',$2)`, session, writingAttempt)
	for _, tc := range []struct {
		actor   uuid.UUID
		skill   string
		media   uuid.UUID
		allowed bool
	}{
		{actor, "listening", audio, true}, {other, "listening", audio, false}, {actor, "listening", uuid.New(), false},
		{actor, "writing", image, true}, {other, "writing", image, false}, {actor, "writing", uuid.New(), false},
	} {
		allowed, err := repo.OwnsMedia(ctx, tc.actor, tc.skill, tc.media)
		if err != nil || allowed != tc.allowed {
			t.Fatalf("media %s actor=%s: allowed=%v err=%v", tc.skill, tc.actor, allowed, err)
		}
	}
	seed(`DELETE FROM full_mock_session_sections WHERE attempt_id=$1`, attempt)
	allowed, err := repo.OwnsMedia(ctx, actor, "listening", audio)
	if err != nil || allowed {
		t.Fatalf("practice media allowed without mock membership: %v %v", allowed, err)
	}
}
