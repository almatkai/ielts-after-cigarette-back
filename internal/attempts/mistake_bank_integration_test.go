package attempts

import (
	"context"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func seedBankReading(t *testing.T, pool *pgxpool.Pool, user uuid.UUID, kind string) (uuid.UUID, uuid.UUID, []uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	material, version, group := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO reading_materials (id,slug,exam_type,difficulty,created_by,updated_by,material_kind)
		VALUES ($1,$2,'academic','intermediate',$3,$3,$4)`, material, material.String(), user, kind); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO reading_material_versions (id,material_id,version_number,title,body,created_by)
		VALUES ($1,$2,1,'Pinned title','A passage body',$3)`, version, material, user); err != nil {
		t.Fatal(err)
	}
	if kind == "TEST" {
		return material, version, nil
	}
	if _, err := pool.Exec(ctx, `INSERT INTO reading_question_groups (id,material_version_id,position,question_type,created_by) VALUES ($1,$2,1,'short_answer',$3)`, group, version, user); err != nil {
		t.Fatal(err)
	}
	questions := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for i, question := range questions {
		// Weighted questions mean maxScore-score is NOT the mistake count.
		if _, err := pool.Exec(ctx, `INSERT INTO reading_questions (id,group_id,position,prompt,points,created_by) VALUES ($1,$2,$3,'Question',3,$4)`, question, group, i+1, user); err != nil {
			t.Fatal(err)
		}
	}
	return material, version, questions
}

func seedBankAttempt(t *testing.T, pool *pgxpool.Pool, user uuid.UUID, skill string, material, version uuid.UUID, score, max int) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `INSERT INTO attempts (id,user_id,material_type,material_id,material_version_id,status,score,max_score,band,submitted_at)
		VALUES ($1,$2,$3,$4,$5,'SUBMITTED',$6,$7,5,CURRENT_TIMESTAMP)`, id, user, skill, material, version, score, max); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestMistakeBankSQLPaginationAndCounts(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool)
	user, other := testdb.User(t, pool), testdb.User(t, pool)
	material, version, questions := seedBankReading(t, pool, user, "PASSAGE")
	newVersion := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO reading_material_versions (id,material_id,version_number,title,body,created_by)
		VALUES ($1,$2,2,'Changed title','Changed body',$3)`, newVersion, material, user); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE reading_materials SET current_version_id=$1 WHERE id=$2`, newVersion, material); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 15; i++ {
		id := seedBankAttempt(t, pool, user, MaterialReading, material, version, 3, 9)
		// One correct, one incorrect and one missing saved answer.
		if _, err := pool.Exec(ctx, `INSERT INTO attempt_answers (attempt_id,question_id,is_correct) VALUES ($1,$2,true),($1,$3,false)`, id, questions[0], questions[1]); err != nil {
			t.Fatal(err)
		}
	}
	seedBankAttempt(t, pool, other, MaterialReading, material, version, 3, 9)
	seedBankAttempt(t, pool, user, MaterialReading, material, version, 9, 9)
	if _, err := repo.Create(ctx, Attempt{ID: uuid.New(), UserID: user, MaterialType: MaterialReading, MaterialID: material, MaterialVersionID: version}); err != nil {
		t.Fatal(err)
	}
	first, err := repo.ListMistakeAttempts(ctx, user, MaterialReading, 1, 12)
	if err != nil || len(first.Items) != 12 || !first.HasNext {
		t.Fatalf("first page: %+v, %v", first, err)
	}
	second, err := repo.ListMistakeAttempts(ctx, user, MaterialReading, 2, 12)
	if err != nil || len(second.Items) != 3 || second.HasNext {
		t.Fatalf("second page: %+v, %v", second, err)
	}
	seen := map[uuid.UUID]bool{}
	for _, page := range []MistakeAttemptsPage{first, second} {
		for _, item := range page.Items {
			if seen[item.ID] || item.UserID != user || item.TestTitle != "Pinned title" || item.MistakeCount == nil || *item.MistakeCount != 2 {
				t.Fatalf("incorrect/duplicate list item: %+v", item)
			}
			seen[item.ID] = true
		}
	}
	empty, err := repo.ListMistakeAttempts(ctx, user, MaterialReading, 3, 12)
	if err != nil || empty.Items == nil || len(empty.Items) != 0 || empty.HasNext {
		t.Fatalf("empty page: %+v, %v", empty, err)
	}
	exact, err := repo.ListMistakeAttempts(ctx, user, MaterialReading, 1, 15)
	if err != nil || len(exact.Items) != 15 || exact.HasNext {
		t.Fatalf("exact page: %+v, %v", exact, err)
	}
}

func TestMistakeBankSQLReadingTestPassagesAndAI(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool)
	user := testdb.User(t, pool)
	passage, passageVersion, questions := seedBankReading(t, pool, user, "PASSAGE")
	material, version, _ := seedBankReading(t, pool, user, "TEST")
	if _, err := pool.Exec(ctx, `INSERT INTO reading_test_passages VALUES ($1,1,$2,$3)`, version, passage, passageVersion); err != nil {
		t.Fatal(err)
	}
	attempt := seedBankAttempt(t, pool, user, MaterialReading, material, version, 3, 9)
	if _, err := pool.Exec(ctx, `INSERT INTO attempt_answers (attempt_id,question_id,is_correct) VALUES ($1,$2,true)`, attempt, questions[0]); err != nil {
		t.Fatal(err)
	}
	page, err := repo.ListMistakeAttempts(ctx, user, MaterialReading, 1, 12)
	if err != nil || len(page.Items) != 1 || page.Items[0].MistakeCount == nil || *page.Items[0].MistakeCount != 2 {
		t.Fatalf("test passage count: %+v, %v", page, err)
	}
	writing := seedBankAttempt(t, pool, user, MaterialWriting, uuid.New(), uuid.New(), 50, 90)
	seedBankAttempt(t, pool, user, MaterialWriting, uuid.New(), uuid.New(), 50, 90) // no evaluation
	if _, err := pool.Exec(ctx, `INSERT INTO writing_evaluations (attempt_id,model,overall_band,task_response_band,coherence_band,lexical_resource_band,grammar_band,feedback)
		VALUES ($1,'test',5,5,5,5,5,'{}')`, writing); err != nil {
		t.Fatal(err)
	}
	page, err = repo.ListMistakeAttempts(ctx, user, MaterialWriting, 1, 12)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != writing || page.Items[0].MistakeCount != nil || page.HasNext {
		t.Fatalf("AI page: %+v, %v", page, err)
	}
}

func TestMistakeBankSQLListeningCounts(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool)
	user := testdb.User(t, pool)
	material, version, part, group := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO listening_tests (id,slug,exam_type,created_by,updated_by) VALUES ($1,$2,'academic',$3,$3)`, []any{material, material.String(), user}},
		{`INSERT INTO listening_test_versions (id,test_id,version_number,title,created_by) VALUES ($1,$2,1,'Listening',$3)`, []any{version, material, user}},
		{`INSERT INTO listening_parts (id,test_version_id,position) VALUES ($1,$2,1)`, []any{part, version}},
		{`INSERT INTO listening_question_groups (id,part_id,position,question_type) VALUES ($1,$2,1,'short_answer')`, []any{group, part}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	questions := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for i, id := range questions {
		if _, err := pool.Exec(ctx, `INSERT INTO listening_questions (id,group_id,position,number,prompt,points) VALUES ($1,$2,$3,$3,'Question',2)`, id, group, i+1); err != nil {
			t.Fatal(err)
		}
	}
	attempt := seedBankAttempt(t, pool, user, MaterialListening, material, version, 2, 6)
	if _, err := pool.Exec(ctx, `INSERT INTO attempt_answers (attempt_id,question_id,is_correct) VALUES ($1,$2,true),($1,$3,false)`, attempt, questions[0], questions[1]); err != nil {
		t.Fatal(err)
	}
	page, err := repo.ListMistakeAttempts(ctx, user, MaterialListening, 1, 12)
	if err != nil || len(page.Items) != 1 || page.Items[0].MistakeCount == nil || *page.Items[0].MistakeCount != 2 {
		t.Fatalf("listening count: %+v, %v", page, err)
	}
}
