# Question translation backfill: fresh rollout and existing data

Migration 44 originally put `v.tr, ''` into `(explanation, hint)` even for
hint matches. New hint-only rows therefore acquired a translated hint in their
explanation; conflicts cleared the hint. The corrected generator and migration
use `'', v.tr` for hints, and each conflict updates only its matching field.
No reviewed RU/KK/EN dictionary text changed.

## Fresh rollout

At the checked origin/main (`4e9df61`), migrations end at 42. A database that
has never applied 43/44 can apply the corrected 43/44 normally, once release is
approved. No migration 45 is needed for that path. An already recorded 44 will
not run again merely because its file changed. Do not roll back/drop 43, reset
the migration version, or treat a rerun of fixed 44 as a complete repair.

## Already-applied 44: prepare a separate, reviewed repair

No live database was inspected or changed for this fix. Whether dev applied 44,
which rows it affected, and their edit history still need confirmation. The
schema has no provenance marker that distinguishes a legitimate explanation
from the same text accidentally inserted by 44. `updated_at` is only a default,
and the original backfill does not change it on conflict: it is not sufficient
evidence of ownership or absence of later edits.

1. With separate authorization, capture migration status, the translation table,
   source question IDs/explanations/content, and available pre-44 backup/audit
   history. Keep that snapshot as the rollback reference. Rehearse on an isolated
   local database restored from the snapshot first.
2. In another isolated copy, preserve the sources, empty only the copied
   `question_translations`, and execute corrected 44. This produces expected
   dictionary matches using the existing SQL, without copying its dictionaries
   into a second migration. Join these expected rows to the captured rows by
   `(question_id, skill, lang)` to prepare a review manifest.
3. Propose filling empty hints from nonempty expected hints. Preserve later
   manual/nonempty hints unless their replacement is separately approved.
   A suspicious hint-only row has expected explanation `''`, expected hint
   nonempty, captured hint `''`, and captured explanation equal to expected hint.
   This equality is a **candidate**, not permission to clear its explanation.
   Clear to `''` only when pre-44/audit evidence proves that 44 created the row
   with that misplaced explanation, the source used by 44 is known, and no later
   legitimate explanation edit needs preserving. Otherwise keep the explanation
   and report the unresolved row for review. Restore a known legitimate value
   only from reviewed evidence; do not generate translations.
4. Build an explicit manifest containing the captured source strings, captured
   translation values, and approved replacement values for each individual row.
   Unrelated explanations must have identical before/after values. Rehearse the
   following guarded update, check returned rows and resulting review content,
   and retain unresolved candidates. Do not replay all of 44 indiscriminately:
   its explanation statements also refresh dictionary matches and could replace
   legitimate edits made since the first application.

The following is a transaction template for a separately authorized repair.
Load only the reviewed manifest into the temporary table (for example via local
CSV import); no dictionary or broad text-equality cleanup is involved. Locks
make source/value checks stable until the transaction ends. An empty manifest
does nothing. For rollback use a separately reviewed inverse manifest with the
same compare-and-set checks, rather than restoring over newer edits.

```sql
BEGIN;
CREATE TEMP TABLE translation_repair (
    question_id uuid NOT NULL,
    skill text NOT NULL CHECK (skill IN ('reading', 'listening')),
    lang text NOT NULL CHECK (lang IN ('kk', 'en')),
    source_explanation text,
    source_hint text,
    before_explanation text NOT NULL,
    before_hint text NOT NULL,
    after_explanation text NOT NULL,
    after_hint text NOT NULL,
    PRIMARY KEY (question_id, lang)
) ON COMMIT DROP;
-- Load reviewed rows here. Text values must be imported without rewriting them.
LOCK TABLE reading_questions, listening_questions IN SHARE MODE;
LOCK TABLE question_translations IN SHARE ROW EXCLUSIVE MODE;
CREATE TEMP TABLE repaired_translations ON COMMIT DROP AS
WITH sources AS (
    SELECT id, 'reading' AS skill, explanation, content->>'hint' AS hint
    FROM reading_questions
    UNION ALL
    SELECT id, 'listening', explanation, content->>'hint'
    FROM listening_questions
), repaired AS (
    UPDATE question_translations t
    SET explanation = r.after_explanation, hint = r.after_hint,
        updated_at = CURRENT_TIMESTAMP
    FROM translation_repair r, sources q
    WHERE t.question_id = r.question_id AND t.lang = r.lang AND t.skill = r.skill
      AND q.id = r.question_id AND q.skill = r.skill
      AND q.explanation IS NOT DISTINCT FROM r.source_explanation
      AND q.hint IS NOT DISTINCT FROM r.source_hint
      AND t.explanation = r.before_explanation AND t.hint = r.before_hint
    RETURNING t.question_id, t.skill, t.lang, t.explanation, t.hint
)
SELECT * FROM repaired;
DO $$ BEGIN
    IF (SELECT count(*) FROM repaired_translations)
       <> (SELECT count(*) FROM translation_repair) THEN
        RAISE EXCEPTION 'Repair manifest is stale or references missing rows';
    END IF;
END $$;
TABLE repaired_translations;
ROLLBACK; -- Rehearsal default. Commit requires separate review/authorization.
```

This fix deliberately adds no automatic corrective migration: choosing 45
would not solve the missing provenance. Hint repair can be prepared from the
dictionary and captured values, but ambiguous explanation contamination remains
unresolved until evidence or a content owner selects the correct value.

## Regression

With an explicitly supplied disposable local `TEST_DATABASE_URL`:

```sh
go test ./internal/attempts -run '^TestQuestionTranslationBackfillSQL$' -count=1 -v
go test ./internal/attempts ./internal/fullmock -count=1
```

The first test executes the entire checked-in 44 and the actual Python
generator's SQL (a two-entry reviewed dictionary fixture) in PostgreSQL using
`internal/testdb`. It covers Reading/Listening, KK/EN, both fields, each field
alone, existing rows retaining the other field, no matches, and repeated runs.
It also asserts that a contaminated old explanation survives a fixed rerun.
Without `TEST_DATABASE_URL`, database tests skip; ordinary translation unit
tests or a CI pass without this SQL test do not prove the backfill is correct.
