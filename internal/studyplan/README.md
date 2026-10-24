# Persisted adaptive study plan

The public contract is `docs/ADAPTIVE_STUDY_PLAN_API.md`; its original scheduler shape is retained with additive learning fields documented in `docs/academic-readiness/ACADEMIC_LEARNING_CONTRACT.md`. `Handler.Routes` is mounted in the application's authenticated API group. Profile goals and IANA timezone remain owned by the existing user API.

## Persistence and lifecycle

Migration `000043` adds a per-user versioned aggregate and unique assignment/attempt claims. A transaction-scoped PostgreSQL advisory lock protects absent-row creation; the aggregate row is also locked during every read/reconciliation/mutation. All mutations validate the persisted revision. Only actual data/day changes make a GET increment its revision. Client date query parameters never control the clock.

The aggregate retains assignments, original skips and change descriptions. Replans/settings change future days only; completed or started days retain their original budgets. Unstarted past tasks become missed once on local-day transition, without debt. Repeat material practice may be allocated at least seven calendar days apart, explicitly marked as repeat rather than a new checkpoint. Missed/unstarted skipped tasks are not counted as actual practice exposures. Active attempts already claimed by another assignment cannot be allocated as new evidence.

GET does not silently replace an expired cycle. An explicit POST after expiry starts a new 30-day cycle within the same user aggregate (stable plan ID, increased revision), archiving the previous complete plan in private persisted `state.history`. The public response shows the current cycle only; there is no additional archived-cycle API in this contract. Existing attempt history remains available through the existing attempts API. Clients should offer the explicit POST when `today > plan.endDate`.

## Attempts, evidence and limitations

Start injects the existing `attempts.Service` via `AttemptStarter`: it resumes a standalone draft, verifies the real row's ownership/material/status, claims it uniquely and returns its ID plus the existing exam route. A resumed draft is explicitly labelled. A failed/rolled-back claim cannot complete two assignments. The dependency uses its own connection: if an error occurs after attempt creation but before the aggregate commits, an unlinked draft can remain; retry resumes it rather than fabricating completion. No new AI requests are made.

Completion is projected from real user-owned attempts: IN_PROGRESS / PROCESSING / SUBMITTED. Reviews can refer only to the user's reviewable submitted attempts; explicit acknowledgement records VIEWED, never mastery. Existing full-mock review locks are respected. Wrong-answer counts and persisted AI improvement strings are the only recommendation sources. System copy keeps the section names Listening/Reading/Writing/Speaking in English. The planner prioritizes executable missing-section practice, assigns one primary material per day, and caps review at one 10-minute block (roughly a quarter of the budget; 20+10 is allowed on a 30-minute day). A small bank is spread through the week; new-material checkpoints use the last selected study day. Weekly goals combine distinct phases with real missing sections and stored wrong-answer counts, without invented error tags. Reviews identify the original material title and local result date. Missed/no-time skipped reviews are not viewed and may be assigned again; unsuitable reviews are excluded for the rest of the cycle without recording a view. An unsuitable material skip sets a seven-day cooldown from the skipped date, without recording completion; acknowledged reviews are not duplicated. Blocked work links to `/practice` and distinguishes an empty bank, insufficient time, active claims and the next available repeat date. Day-transition change notices are emitted only when assignments actually become missed. Stored AI text/material titles retain their original language; system-generated reasons, changes and limitations are Russian.

All `band`, `practiceBand` and `overallBand` values remain null until validated provenance exists. L/R exposes original raw n/N without scaling; W/S exposes AI feedback without numeric bands. Latest results, not maxima, are selected; pending grading retains the previous submitted value/date with a pending warning. Distinct material IDs determine sample counts across versions, and repeats are flagged as non-independent. Missing/failed AI evaluation is not zero. Results at least 28 days old retain their value but are marked stale.

Progress counts distinct submitted attempts and acknowledged reviews. Active minutes are null. Writing/Speaking submittedAt may reflect grading completion rather than precise work-submission time. Untimed standalone Reading passages receive an explicit 20-minute allocation; whole timed tests retain their complete configured duration. Speaking duration is computed from published preparation/response seconds. Empty/missing/GT-incompatible banks are blocked, never fabricated.

Full Mock duration comes from the existing overview repository, not a shortened planner constant. Offers are optional (four full weeks plus a disabled final partial-week overview). Scheduling is accepted at the week's last selected study day, for a date in the next seven days within the cycle, with an explicit adequate window. Ordinary planned work is superseded without debt or user-skip penalties. No session is created or started by planning. Bank readiness and explicit start remain on the existing Full Mock page.

## Tests

No `.env`, application DATABASE_URL, production DB or external AI is read/contacted. Integration tests use only `internal/testdb.Open`, which creates/drops isolated schemas inside `TEST_DATABASE_URL`. There is no public clock override; tests replace the private service clock.

```sh
TEST_DATABASE_URL='postgres://iac_test@127.0.0.1:55439/iac_adaptive_test?sslmode=disable' \
  go test -count=1 -race ./internal/studyplan ./internal/dashboard ./internal/attempts ./internal/fullmock ./internal/user
go vet ./internal/studyplan ./internal/app
git diff --check
```

The package tests cover planner budgets/rest days/whole materials, repeated-material spacing, deterministic choices, score gating, pending/stale evidence, real authenticated HTTP ownership, concurrent create/start/skip, failed dependency/foreign attempt/unique-claim rollback, actual reading submission and wrong-answer recommendations, review acknowledgement, settings/replanning/history, day reconciliation, pauses 6/7/14, New York DST, rest-day/empty-bank reentry, GT limitations, mock date/window/overview timing/decline/scheduling/idempotency/no sessions, and explicit next-cycle history retention. Without TEST_DATABASE_URL, integration tests explicitly skip via testdb.Open.


## Academic learning extension

The learning contract and UX brief in `docs/academic-readiness/` supersede the original section-only recommendations described above. The aggregate now stores concrete mistake sources, viewed feedback, immutable correction revisions, drill sessions and independently checked outcomes. These are separate events. The daily extension counts submitted drills, saved correction revisions and qualified independent passes by profile-local date; active time remains unknown.

The calendar reserves editor-admitted objective controls for open learning cycles. An assignment binds the cycle, correction generation, source, content hash and family. Start derives this context on the server; stale/early/foreign starts fail without exposing or consuming a control. Own correct unassisted corrections are required before a cycle-context start can use reserved controls. Remedial suggestions use non-eligible practice. Explicit free practice has a client confirmation warning that viewing consumes novelty. Unseen control tasks, stimulus and audio are withheld until start.

Independent checks require a reviewed objective key, a known new family and new origin, plus an unchanged correct unassisted correction. The two-family rule including one delayed check after three local calendar days is an internal practice hypothesis, not an IELTS score or a proven learning effect. A reviewed family cannot relabel the same source as new. Editorial withdrawal removes qualification without deleting original answers. All bundled content ships as draft; Writing/Speaking remain unverified and typed Speaking is not speech evidence.

Migration `000044` stores ADMIN-only exact-content editorial decisions and audit history. Local test approvals are technical fixtures, not human editorial certification. Course effectiveness, feedback quality, audio quality and scoring calibration still require independent human validation.
