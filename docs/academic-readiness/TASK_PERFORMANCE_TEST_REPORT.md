# Question-format evidence — implementation freeze

Selected checkout: `ielts-after-cigarette-back-adaptive-plan`, branch
`feat/adaptive-study-plan`. Additive projection only; no migration, commit,
runtime database, environment file, production action or other checkout change.
Parent owns the final application rebuild and UI journey.

## Frozen API shape

`Learning.taskPerformance: TaskPerformance[]`:

```ts
type TaskPerformance = {
  section: 'reading' | 'listening'; questionType: string; title: string;
  answered: number; correct: number; incorrect: number;
  sourceCount: number; practiceAnswered: number;
  sourceRefs: {
    sourceId: string; attemptId: string; title: string; occurredAt: string;
    answered: number; correct: number; incorrect: number;
  }[];
  limitations: string[];
};
```

All 25 supported section/format pairs appear (zero rows may be hidden by FE).
Unknown empty types become `unknown`; future nonempty keys remain intact with
readable unknown labels. Arrays are non-null. References group the first graded
work by logical source and exact format, newest first, capped at five;
`sourceCount` includes the omitted groups. Reference `attemptId` opens the existing
owned review. Multiple child passage references may open the same TEST review.

## Coverage and limits

Only owned persisted submitted graded L/R answers with proven version/question
membership are counted, including correct and graded blank answers. NULL grades,
in-progress attempts, unrelated question IDs and hidden/abandoned Mocks are
excluded. Completed visible Mock attempts are eligible under the same rules.
The first eligible graded submitted attempt per source supplies the main counts;
all later attempts contribute only repeat-practice answers. Partial work has only
its actual graded-answer count, never a section score or band.

Reading source identity is the child passage, including TEST/standalone reuse,
stable material IDs across versions, and equal normalized nonempty text across
catalog IDs. Listening identity is the whole test material across versions;
equal complete ordered audio-asset sets collapse known copies across materials.
Public source IDs follow the earliest observed material. These guards do not
establish unseen content, editorial approval or calibrated independence, and
cannot identify every edited/reuploaded copy.

Drills, saved corrections and transfer checks remain separate. Writing/Speaking
retain their existing explicit AI suggestions/current cycles; no answer ratio is
invented. Format observations have no mastery/diagnosis status or percentage.
The projection is excluded from the planner fingerprint and does not change R4,
reserved-family guards, mastery requirements or the latest-cycle profile rule.
Stored grades are projected as saved; no retroactive regrading is performed.
The parent's separate NOT_GIVEN alias fix is outside these implementation files.

## Verification log

Every PostgreSQL test used only:

```text
TEST_DATABASE_URL=postgres://iac_test@127.0.0.1:55439/iac_adaptive_test?sslmode=disable
```

`testdb.Open` creates and drops a random `regression_<uuid>` schema for each test;
it reads neither application DATABASE_URL nor environment files.

```text
go test ./internal/studyplan -run '^TestTaskPerformance' -count=1 -v
PASS: TestTaskPerformanceHTTPAllCorrectAndSingleErrorAreObservedFacts
PASS: TestTaskPerformanceHTTPReadingTestPassagesVersionsAndCopiedStimuli
PASS: TestTaskPerformanceHTTPListeningCountsWholeMaterialsAndFirstGradedAttempt
PASS: TestTaskPerformanceHTTPOwnershipPendingHiddenMockAndQuestionMembership
PASS: TestTaskPerformanceHTTPUnknownFormatsBoundedRefsAndNoData
PASS: TestTaskPerformanceDoesNotChangePlannerFingerprint
ok internal/studyplan 1.254s

go test -race ./internal/studyplan -count=1
ok internal/studyplan 20.663s

go vet ./internal/studyplan
exit 0, no output

git diff --check
exit 0, no output
```

The all-correct/one-error test uses the real attempt submission/grading service
and authenticated HTTP Learning endpoint against PostgreSQL. Other fixtures
persist actual source/version/question/attempt relationships and query that same
HTTP endpoint. They verify repeat/new-version/copy deduplication, Reading TEST
child sources, Listening whole-material references, ownership, visibility,
ungraded exclusion, graded blanks, unknown/empty types, bounded source references,
no numeric band/status claims, and unchanged counts after a saved correction and
drill submission. The only schema constraint relaxation is inside a random test
schema to exercise legacy/future format labels, never an application migration.

`git diff --no-index --check /dev/null <file>` also checked the new/untracked
implementation, integration tests, touched studyplan DTO/projection/input files
and contract, because the surrounding authorized task files are still untracked.
