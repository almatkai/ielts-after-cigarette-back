package attempts

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
)

// Execute both the checked-in migration and the generator's output. The small
// dictionary uses reviewed strings from the migration, without duplicating or
// rewriting translation text in fixtures.
func TestQuestionTranslationBackfillSQL(t *testing.T) {
	data, err := os.ReadFile("../../migrations/000044_question_translations_backfill.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	migration := string(data)
	statements := strings.Split(migration, "INSERT INTO question_translations")
	if len(statements) != 9 {
		t.Fatalf("expected eight backfill statements, got %d", len(statements)-1)
	}
	dictionary := map[string]map[string]string{}
	var sources [2]string
	for n, lang := range []string{"kk", "en"} {
		texts := strings.Split(statements[n+1], "$tr$")
		if len(texts) < 9 {
			t.Fatal("missing reviewed dictionary entries")
		}
		for i := range sources {
			ru, translated := texts[1+4*i], texts[3+4*i]
			if n == 0 {
				sources[i] = ru
				dictionary[ru] = map[string]string{}
			} else if ru != sources[i] {
				t.Fatal("dictionary language order differs")
			}
			dictionary[ru][lang] = translated
		}
	}

	for _, mode := range []string{"checked_in", "generated"} {
		t.Run(mode, func(t *testing.T) {
			pool := testdb.Open(t)
			sql := migration
			if mode == "generated" {
				fixture, err := json.Marshal(dictionary)
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(t.TempDir(), "union.json")
				if err := os.WriteFile(path, fixture, 0600); err != nil {
					t.Fatal(err)
				}
				out, err := exec.Command("python3", "../../scripts/backfill_question_translations.py", path).CombinedOutput()
				if err != nil {
					t.Fatalf("generator: %v: %s", err, out)
				}
				sql = string(out)
			}
			ctx := context.Background()
			conn, err := pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Release()
			// Shadow only the source tables on this connection. Translation writes
			// still use the real table created by migrations in testdb's schema.
			for _, skill := range []string{"reading", "listening"} {
				if _, err := conn.Exec(ctx, "CREATE TEMP TABLE "+skill+"_questions (id uuid PRIMARY KEY, explanation text, content jsonb)"); err != nil {
					t.Fatal(err)
				}
			}
			type expectedRow struct {
				id                                   uuid.UUID
				skill, name, lang, explanation, hint string
				absent                               bool
			}
			var expected []expectedRow
			for _, skill := range []string{"reading", "listening"} {
				for _, tc := range []struct {
					name, explanation, hint, oldExplanation, oldHint string
					existing, contaminated                           bool
				}{
					{name: "both", explanation: sources[0], hint: sources[1]},
					{name: "hint_only", explanation: "unmatched", hint: sources[1]},
					{name: "explanation_only", explanation: sources[0], hint: "unmatched"},
					{name: "existing_hint", explanation: "unmatched", hint: sources[1], existing: true, oldExplanation: "legitimate explanation", oldHint: "old hint"},
					{name: "existing_explanation", explanation: sources[0], hint: "unmatched", existing: true, oldExplanation: "old explanation", oldHint: "legitimate hint"},
					{name: "existing_both", explanation: sources[0], hint: sources[1], existing: true, oldExplanation: "old explanation", oldHint: "old hint"},
					{name: "unmatched", explanation: "unmatched", hint: "unmatched"},
					{name: "existing_unmatched", explanation: "unmatched", hint: "unmatched", existing: true, oldExplanation: "legitimate explanation", oldHint: "legitimate hint"},
					// A rerun cannot safely infer that this explanation was inserted
					// by the old bug. Document and lock down that repair limitation.
					{name: "previously_broken_hint_only", explanation: "unmatched", hint: sources[1], existing: true, contaminated: true},
				} {
					id := uuid.New()
					content, err := json.Marshal(map[string]string{"hint": tc.hint})
					if err != nil {
						t.Fatal(err)
					}
					if _, err := conn.Exec(ctx, "INSERT INTO "+skill+"_questions VALUES ($1,$2,$3)", id, tc.explanation, content); err != nil {
						t.Fatal(err)
					}
					for _, lang := range []string{"kk", "en"} {
						explanation, hint := tc.oldExplanation, tc.oldHint
						if tc.contaminated {
							explanation = dictionary[sources[1]][lang]
						}
						if tc.existing {
							_, err := conn.Exec(ctx, `INSERT INTO question_translations (question_id,skill,lang,explanation,hint) VALUES ($1,$2,$3,$4,$5)`, id, skill, lang, explanation, hint)
							if err != nil {
								t.Fatal(err)
							}
						}
						if translated := dictionary[tc.explanation][lang]; translated != "" {
							explanation = translated
						}
						if translated := dictionary[tc.hint][lang]; translated != "" {
							hint = translated
						}
						expected = append(expected, expectedRow{id, skill, tc.name, lang, explanation, hint, !tc.existing && explanation == "" && hint == ""})
					}
				}
			}
			for run := 1; run <= 2; run++ {
				if _, err := conn.Exec(ctx, sql); err != nil {
					t.Fatalf("backfill run %d: %v", run, err)
				}
				for _, want := range expected {
					var count int
					if err := conn.QueryRow(ctx, `SELECT count(*) FROM question_translations WHERE question_id=$1 AND lang=$2`, want.id, want.lang).Scan(&count); err != nil {
						t.Fatal(err)
					}
					if want.absent {
						if count != 0 {
							t.Errorf("%s/%s/%s run %d: unexpected translation", want.skill, want.name, want.lang, run)
						}
						continue
					}
					var skill, explanation, hint string
					if err := conn.QueryRow(ctx, `SELECT skill,explanation,hint FROM question_translations WHERE question_id=$1 AND lang=$2`, want.id, want.lang).Scan(&skill, &explanation, &hint); err != nil {
						t.Fatal(err)
					}
					if skill != want.skill || explanation != want.explanation || hint != want.hint {
						t.Errorf("%s/%s/%s run %d: skill correct=%t explanation correct=%t hint correct=%t", want.skill, want.name, want.lang, run, skill == want.skill, explanation == want.explanation, hint == want.hint)
					}
				}
			}
		})
	}
}
