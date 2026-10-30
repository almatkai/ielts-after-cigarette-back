# Academic learning additive API — 2026-10-24

Implementation contract for FE. Authenticated under the same API prefix as `/study-plan`.
Existing `/study-plan` DTO is preserved. Its assignments may additionally contain
`drillId`, `sessionId` (nullable strings), kind `DRILL`, and day `readOnlyReason`
(nullable string). DRILL target is `/study-plan/learning/drills/{drillId}`;
use the drill start API, not the ordinary attempt start API.

All learning responses use camelCase. Arrays are always arrays. Scores/bands are
not accepted from clients. Numeric readiness bands remain null. Times are RFC3339;
dates YYYY-MM-DD. Persisted state shares the study-plan user transaction/advisory lock.

## Endpoints

* `GET /study-plan/learning` -> Learning
* `GET /study-plan/learning/inventory` -> Inventory
* `GET /study-plan/learning/cycles/{id}` -> Cycle (also reconciles late feedback)
* `POST /study-plan/learning/cycles/{id}/view` body `{expectedRevision}` -> Cycle
* `POST /study-plan/learning/cycles/{id}/correction` body
  `{expectedRevision,idempotencyKey,text,explanation,assisted}` -> Cycle
* `POST /study-plan/learning/cycles/{id}/skip` body `{expectedRevision,idempotencyKey}` -> Cycle
* `POST /study-plan/learning/drills/{id}/start` body
  `{expectedRevision,idempotencyKey,cycleId?,assignmentId?}` -> Session
* `GET /study-plan/learning/sessions/{id}` -> Session
* `POST /study-plan/learning/sessions/{id}/submit` body
  `{expectedRevision,idempotencyKey,text,explanation,assisted,audioAttemptId?}` -> Session

`expectedRevision` on cycle mutations is **Cycle.revision**, on start is
**Learning.revision**, on submit is **Session.revision**. Start retries return the
same session. Identical idempotency key + payload retries return the saved result;
key reuse with a different payload is 409. Keys: 1–100 chars. Text: 1–12000 chars;
explanation: correction requires 1–4000 chars, submit optional <=4000.
`assisted` is an explicit boolean. Unknown/foreign IDs are 404; stale revisions or
unavailable transitions 409; invalid payloads 422. Refresh Learning after mutations
for its current revision and updated profiles/plan. No public clock parameter.
A skipped cycle remains saved; a later distinct mistake creates its own cycle.

## DTOs (TypeScript notation)

```ts
type Section = 'reading'|'listening'|'writing'|'speaking';
type WeaknessStatus = 'insufficient'|'needs-practice'|'correction-saved'|
  'awaiting-transfer'|'demonstrated';
type Learning = {
  revision:number; today:string; examDate:string|null; readOnly:boolean;
  skills:MicroSkill[]; profiles:WeakProfile[]; cycles:Cycle[];
  drills:Drill[]; inventory:Inventory; limitations:string[]; overallBand:null;
  taskPerformance:TaskPerformance[];
};
type TaskPerformance = {
  section:'reading'|'listening'; questionType:string; title:string;
  answered:number; correct:number; incorrect:number; sourceCount:number;
  practiceAnswered:number; sourceRefs:TaskPerformanceSource[]; limitations:string[];
};
type TaskPerformanceSource = {
  sourceId:string; attemptId:string; title:string; occurredAt:string;
  answered:number; correct:number; incorrect:number;
};
type MicroSkill = {id:string; section:Section; title:string};
type WeakProfile = {
  skillId:string; section:Section; status:WeaknessStatus;
  sourceIds:string[]; rationale:string; updatedAt:string|null;
  independentFamilies:number; delayedCheckPassed:boolean; remainingChecks:number;
};
type SourceEvidence = {
  attemptId:string; questionId:string|null; materialId:string;
  versionId:string; familyId:string|null; familyKnown:boolean;
  questionType:string; prompt:string; stimulus:string;
  originalAnswer:string; feedback:string; occurredAt:string;
  modality:'objective'|'typed'|'audio'|'unknown';
  provenance:{model:string|null; promptVersion:string|null; rubricVersion:string|null};
};
type Correction = {
  text:string; explanation:string; assisted:boolean; savedAt:string;
  verdict:'incorrect'|'correct'|'unverified'; feedback:string;
};
type TransferCheck = {
  sessionId:string; familyId:string|null; occurredAt:string;
  independent:boolean; delayed:boolean; passed:boolean; reason:string;
};
type Cycle = {
  id:string; revision:number; skillId:string; section:Section;
  status:WeaknessStatus; source:SourceEvidence; viewedAt:string|null;
  correction:Correction|null; checks:TransferCheck[]; skippedAt:string|null;
  nextDrillId:string|null; remainingChecks:number; limitations:string[];
};
type Drill = {
  id:string; skillId:string; section:Section; title:string; durationMinutes:number;
  modality:'objective'|'typed'|'audio'; familyId:string|null; version:string;
  introduction:string; stimulus:string; task:string;
  rightsProvenance:string; editorialStatus:'draft'|'reviewed';
  masteryEligible:boolean;
};
type Session = {
  id:string; revision:number; drill:Drill; cycleId:string|null;
  assignmentId:string|null; status:'in-progress'|'submitted'; startedAt:string;
  submittedAt:string|null; text:string|null; explanation:string|null;
  assisted:boolean; audioAttemptId:string|null;
  verdict:'correct'|'incorrect'|'unverified'|null;
  feedback:string|null; independent:boolean; independenceReason:string;
};
type Inventory = {
  dailyMinutes:number; sections:SectionInventory[]; limitations:string[];
};
type SectionInventory = {
  section:Section; fullMaterials:number; fittingMaterials:number;
  shortDrills:number; reviewedDrills:number; independentFamilies:number;
  available:boolean; limitations:string[];
};
```

`SourceEvidence` never includes an answer key. Correction text is checked against
the immutable source question key for objective tasks; a wrong correction is saved
with `incorrect`, never marked successful merely because it is nonempty. Original
attempt answers remain untouched. W/S corrections are `unverified`, with no automatic
mastery. Viewing only records viewing. Typed Speaking is not speech evidence.
Audio sessions require an owned submitted speaking `audioAttemptId` with a stored
recording (use existing speaking upload/attempt flow); no arbitrary audio URL accepted.

`demonstrated` is an explicit internal practice rule: correct unassisted correction,
two successful independent reviewed new-family same-skill checks, including one >=3
calendar days after correction. Distinct IDs/versions alone do not establish families.
Unknown catalog families and editorial drafts cannot satisfy independent checks.
Remaining checks is a count of this rule's outstanding checks, never tests until a
band improves. W/S awaits qualified/independent checking which is currently unavailable.

Original built-in drills are usable draft practice with rights provenance. Their
keys and explanations provide practice feedback but draft status excludes mastery.
The generated 24-unit draft is not automatically promoted or loaded as validated
control content. Content inventory exposes those limitations per section/budget.

30-day calendar structure stays intact. Dates on/after exam day are read-only rest,
with explicit reason; new drills/mocks/assignments cannot start then. Existing results
and corrections remain readable; mutations are blocked. Full Mock stays optional,
never starts automatically; feedback review is placed on a next available study day
within daily budget. New cycles and sessions survive plan renewal.

## Addendum — audio and broad criterion tags

`Drill.modality` additionally permits `script` for read-only-to-assessment Listening
script practice. Scripts may be practised and checked against draft keys but never
count as listening evidence or independent checks. The planner excludes scripts.
`Drill` may additionally include `audioUrl`, `sourceMaterialId`, `sourceVersionId`,
`sourceQuestionId` (optional strings). Audio URL is relative to the existing API
prefix and uses the existing authenticated `/listening/media/{id}` endpoint.
Published audio focused-question drills are distinct saved exercises, not shortened
full attempts; 12 minutes is a suggested practice window, not measured audio length.

`Cycle.nextCheckDate: string|null` is the server's delayed-check date (3 local
calendar days after latest correction), not a promise that validated content exists.
Correction edits append private history, reset checks and restart the delayed anchor.
An incorrect correction may be revised with a new key and current cycle revision.

The bank uses the 19 IDs in ACADEMIC_MICROSKILLS.md plus broad evidence tags
`ms-w-grammar`, `ms-w-lexis`, `ms-w-coherence`, `ms-w-task-response`,
`ms-s-response-development`. Explicit AI error annotations/improvements may create
unverified suggestion cycles. Positive criterion feedback alone creates no weakness.
Broad grammar or coherence comments never imply numerical Task 1 deficiencies.

Editorial approval is an explicit authenticated ADMIN action, never automatic.
Registry decisions bind a content hash, reviewer, date, version and family. Withdrawn
content cannot start new sessions. A content change or withdrawal invalidates
independence for existing checks. Full Mock API `overallBand` remains null even if
all four legacy section bands are populated.

Source link addendum: `SourceEvidence` optionally contains `sessionId` and `drillId`.
For drill-origin mistakes `attemptId` is the empty string: use
`GET /study-plan/learning/sessions/{sessionId}`, never `/attempts/{sessionId}`.
The original response is retained. `familyKnown` is true only when the source's
exact content/version has current editorial confirmation, including drill origins.
The delayed date is derived in the user's profile timezone, including UTC+14/-10.
Viewing or another check does not invalidate an unchanged correction generation.

Objective input addendum: published option questions display letters A, B, C with
human-readable option text. Plain correction/submission text accepts one letter or
comma-separated letters for an exact multiple-select set (e.g. `A, C`). Letters map
to the original immutable option IDs on the server; learners need not enter opaque
IDs or JSON. Accepted free-text aliases and `NOT GIVEN`/`NOT_GIVEN` are normalized.
Empty answer keys never pass. Tables retain all headers/units/dates/cells as readable
text, cue cards retain their text; arbitrary editor JSON/HTML is not displayed.

Five original 10-minute draft production exercises also cover broad Writing grammar,
lexis, coherence, task response and Speaking response development. They provide a
same-tag next practice step after a saved unverified correction, without converting
it into an automatically verified criterion or independent mastery.

## Calendar-to-cycle binding — acceptance repair

`Assignment.cycleId?: string` is now persisted on a real cycle-bound `DRILL`
assignment. Send `assignmentId` to the existing drill start endpoint. The server
**derives** its cycle from the owned assignment; the client may omit `cycleId`.
If supplied, it must match the persisted binding (409 otherwise). A private
aggregate binding checks the correction generation, immutable source identity,
exercise hash and reviewed family before starting. The returned Session contains
both `assignmentId` and `cycleId`; submission records that cycle's check.

An awaiting-transfer cycle with current eligible unseen same-skill content gets at
most one pending check, on a selected future study date at/after `nextCheckDate`
(three local calendar days after the current correction), within budget and before
the exam date. The check replaces generic practice of the same section where
possible, or uses free budget; completed/in-progress work and history are retained.
There is at most one bound check per day and at most three planned task rows on a
new check day. After submission, another unseen eligible family can be scheduled.
Skipping does not count as a check. Editing the correction invalidates an unstarted
old binding and restarts its delayed anchor. Availability without a bound
assignment does not mean a check is scheduled.

Reviewed `masteryEligible` drills are excluded from generic plan practice. Explicit
free practice can still start them, consuming their novelty; it cannot silently
become independent evidence for a later cycle. Before a reserved eligible origin
has been started/encountered, `GET /study-plan/learning` returns its Drill metadata
and introduction with `stimulus: ""`, `task: ""`, and no `audioUrl`. Start returns
the complete saved exercise. This is a presentation/reservation rule, not an
anti-cheating guarantee. Internal units, hashes, session snapshots and ADMIN
editorial candidates are not redacted or mutated.

`WeakProfile` now reports **one cycle coherently**: the most recent source mistake
(ties by cycle ID), with `sourceIds: [cycle.id]`, its status, independent-family
count, delay flag and remaining checks. `updatedAt` is the latest source,
correction or check time for that cycle. Other cycles remain inspectable in
`Learning.cycles`. The compatibility ID `ms-s-response-development` now has the
broad title «Рекомендации Speaking (непроверенные)»; general AI improvements,
including pronunciation comments, do not establish a precise development deficit.

`Response.progress[]` gains integer fields `drillsCompleted`, `correctionsSaved`,
`independentChecksPassed`. They count unique persisted submitted drill sessions,
all saved correction revisions, and successful currently qualified independent
sessions, assigned by the profile's local calendar date. Idempotent retries and
views do not add events. An actual new correction revision is a new saved event;
its source answer stays unchanged. Editorial withdrawal can remove a session's
independent qualification from this projection. Legacy attempts/reviews keep their
existing meanings; `activeMinutes` stays null and no band growth is inferred.


### Защита контрольных семейств до самостоятельной правки (R4)

До `awaiting-transfer` рекомендации цикла исключают `masteryEligible` задания. Старт такого задания с `cycleId` при неверной, непроверенной или выполненной с помощью правке возвращает 409 до создания сессии и фиксации просмотра семейства. Явная свободная практика остаётся без `cycleId` и требует предупреждения в интерфейсе. Будущее календарное назначение показывает дату; действие старта открывается в день назначения. Эти ограничения не являются оценкой учебного эффекта.

## Question-format observations — additive, read-only projection

`Learning.taskPerformance` describes exact persisted Reading/Listening question
formats, not content topics or cognitive diagnoses. `title` is a readable format
label (e.g. «Краткий ответ»); a short-answer error does not establish a spelling
deficit. There is no mastery status, percentage, band, readiness estimate or
threshold. Existing micro-skill profiles/cycles and their R4 rules remain separate.
These counters do not enter the planner fingerprint or change assignments.

Coverage is persisted attempt answers with a non-null stored grade in owned
`SUBMITTED` Reading/Listening attempts, with a deterministic join to the attempt's
immutable version. Correct answers are included. Graded blank answers count by
their stored grade; null/ungraded answers do not count. No regrading occurs here.
An attempt inside a Mock whose session is not `SUBMITTED` is excluded, including
abandoned Mocks. Completed visible Mock attempts use the same source rules as
ordinary attempts. Foreign attempts and unrelated/missing question IDs are excluded.

For each logical source, select its earliest eligible submitted graded attempt
(ordered by submission time, then attempt ID). `answered = correct + incorrect`
counts all eligible answers in that first attempt, separately per exact type.
Later attempts contribute only to `practiceAnswered`, including types first added
in a later version. A first attempt may be partial; these are checked-answer
counts, never a full IELTS section score or total expected-question denominator.
An earlier ungraded/in-progress/hidden attempt cannot establish the first eligible
work. Prior viewing, drafts and drill exposure are not measured by this projection;
«first» means first eligible graded catalog attempt, not guaranteed unseen content.

Reading `sourceCount` counts logical child passages, including within a TEST,
rather than treating the parent TEST as three new texts. Stable passage material
IDs collapse version changes. Equal normalized nonempty passage text also joins
copied materials across IDs; identity is transitive across versions and copies.
Listening counts whole test materials, not parts/questions. Stable material IDs
collapse versions; equal complete ordered audio-asset sets also join copied tests.
Missing audio metadata alone never establishes equality across materials.
These conservative repeat guards cannot detect every edited/reuploaded copy and
do not establish editorial review, calibrated independence or qualified IELTS
evidence. `sourceId` is opaque, section-prefixed and shared by references to the
same logical source; consumers must not derive a navigation URL from it.

`sourceRefs` groups the first work's answers by logical source and question type,
with readable passage/test `title`, RFC3339 `occurredAt`, and owned `attemptId`
for the existing `/attempts/{attemptId}` review. It contains at most five newest
source groups (ties by source ID), while `sourceCount` includes all source groups.
Several Reading passage references may correctly open the same parent TEST attempt.
The reference counts always describe that source's first work for this type;
they do not include later practice answers. There is no omitted-reference count
other than the explicit full `sourceCount`.

All 25 currently supported section/format pairs are emitted, including zero-data
rows with empty `sourceRefs`. FE may hide rows with no observed answers or repeats.
An empty format is returned as `questionType: 'unknown'`; future nonempty labels
retain their exact key and have a readable unknown-type title. Arrays stay arrays.

Drill sessions, saved correction revisions and transfer checks never enter these
counters. Their evidence remains in existing sessions/cycles/profiles. Writing and
Speaking have no task-format answer counters or fabricated answer percentages;
only their existing explicit AI criterion/error suggestions and current cycles
are available. UI should open the real review, then match an existing owned cycle
by section, exact `source.questionType` and nonempty attempt ID for correction;
drill-origin cycles are separate. Learner-facing limitations are plain language.
