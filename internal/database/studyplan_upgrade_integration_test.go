package database

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
)

// Versions 1–45 are byte-identical to production main. Seed real billing rows
// before applying the release migrations; a fresh-schema test cannot catch a
// collision that silently skips the plan migration on an existing installation.
func TestStudyPlanUpgradePreservesProductionBilling(t *testing.T) {
	pool := testdb.OpenThrough(t, "000045")
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	user := testdb.User(t, pool)
	order, entitlement, source := uuid.New(), uuid.New(), uuid.New()
	exec(`UPDATE billing_plans SET sale_price_kzt=4321 WHERE code='group'`)
	exec(`UPDATE billing_settings SET free_reading=8 WHERE id=1`)
	exec(`INSERT INTO billing_orders(id,user_id,plan_code,list_price_kzt,amount_kzt,status,provider,paid_at,payload) VALUES($1,$2,'group',24990,4321,'PAID','none',CURRENT_TIMESTAMP,'{"release":"preserve"}')`, order, user)
	exec(`INSERT INTO user_entitlements(id,user_id,plan_code,order_id,source,starts_at,ends_at,meetings_total,meetings_used,granted_by,note) VALUES($1,$2,'group',$3,'ADMIN',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP+interval '30 days',4,1,$2,'manual grant')`, entitlement, user, order)
	exec(`INSERT INTO free_usage(user_id,skill,source_kind,source_id) VALUES($1,'reading','ATTEMPT',$2)`, user, source)
	tables := []string{"billing_plans", "billing_settings", "billing_orders", "user_entitlements", "free_usage"}
	snapshot := func() map[string]string {
		t.Helper()
		result := map[string]string{}
		for _, table := range tables {
			var data string
			if err := pool.QueryRow(ctx, `SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text),'[]'::jsonb)::text FROM `+table+` r`).Scan(&data); err != nil {
				t.Fatal(err)
			}
			result[table] = data
		}
		return result
	}
	before := snapshot()
	apply := func(name string) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("../../migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		exec(string(data))
	}
	assertBilling := func() {
		t.Helper()
		for table, data := range snapshot() {
			if data != before[table] {
				t.Fatalf("billing changed in %s: %s", table, data)
			}
		}
	}
	up := func() {
		apply("000046_adaptive_study_plan.up.sql")
		apply("000047_learning_content_metadata.up.sql")
		exec(`INSERT INTO study_plans(id,user_id,revision,state) VALUES($1,$2,1,'{}')`, uuid.New(), user)
		for _, table := range []string{"study_plans", "study_plan_attempts", "study_learning_content_metadata", "study_learning_content_reviews"} {
			var n int
			if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
				t.Fatal(err)
			}
		}
		assertBilling()
	}
	up()
	apply("000047_learning_content_metadata.down.sql")
	apply("000046_adaptive_study_plan.down.sql")
	assertBilling()
	up()
}
