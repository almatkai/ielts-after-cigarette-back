# Adaptive study plan: implementation contract

Scope: persisted 30-calendar-day Academic preparation plan using actual published IAC materials and attempts. Settings, skips, versioned replanning, real attempt completion, mistake-review acknowledgement, evidence transparency, daily results and optional weekly Full Mock. Do not invent calibrated scores, content, time spent or human assessment services. Research: `/Users/almat/Desktop/code/ielts/research/2026-10-24-ielts-learning-methodology.md`.

Selected worktrees ONLY:
- Frontend `/Users/almat/Desktop/code/ielts/ielts-after-cigarette-adaptive-plan`.
- Backend `/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan`.
Both branch `feat/adaptive-study-plan` based on fetched `origin/main`. Do not edit original checkouts. This contract is coordinated shared interface; ask parent before changing its public shape. Backend agent owns backend implementation, frontend agent owns frontend implementation.

## API

Authenticated routes, prefixed `/api/v1`. Standard existing JSON errors. 401 unauthorized, 404 foreign/missing IDs, 409 stale revision or invalid state, 422 validation. UUID strings, date-only local calendar dates, timestamps RFC3339. Arrays always arrays, unavailable numbers null. Reads accept no client clock override.

```
GET  /study-plan
POST /study-plan                          {dailyMinutes,studyDays,weeklyMockEnabled}
PUT  /study-plan/settings                 {dailyMinutes,studyDays,weeklyMockEnabled,expectedRevision}
POST /study-plan/replan                   {expectedRevision}
POST /study-plan/assignments/{id}/start    {expectedRevision}
POST /study-plan/assignments/{id}/skip     {expectedRevision,reason}
POST /study-plan/assignments/{id}/review   {expectedRevision}
POST /study-plan/weeks/{week}/mock         {expectedRevision,action:"schedule"|"decline",scheduledDate?,windowMinutes?}
```

POST create is idempotent for existing active cycle (returns it); no automatic replacement destroying completed history. PUT settings changes future assignments only. Replan preserves completed/history and original skips, never carries missed minutes as debt. Backend may reconcile old unfinished assignments once on real local-day transition; repeated GET same day must not continually increase revision. Navigating frontend dates does not advance backend time or mutate plans.

All mutation routes except start return the complete same-shaped GET response. `start` returns `{attemptId:string|null,target:string,planRevision:number}`. Server validates ownership, material availability, time and status. First start creates/resumes the current user's existing material attempt through attempts.Service; persist link to assignment and reuse on retry. One attempt cannot complete multiple assignments. Do not count opening a material as completed. REVIEW starts return target attempt review, with attemptId referring to source; review acknowledgement explicitly records viewing only. Backend may return library target for blocked material but never complete it. No arbitrary client supplied attemptId.

Start practice navigation target example `/exam/reading/{materialId}` (existing exam UI resumes the attempt), review `/attempts/{sourceAttemptId}`. Clients use existing router/base URL correctly. Full mock scheduled target `/full-mocks`, existing generator still requires separate explicit start; scheduling does not silently create a session.

## Response types (TypeScript notation)

```
type Skill = 'listening'|'reading'|'writing'|'speaking'
type StudyResponse = {
  today: string
  timezone: string
  settings: {dailyMinutes:number;studyDays:number[];weeklyMockEnabled:boolean}
  goal: {targetBand:number|null;examDate:string|null;examType:string|null}
  plan: null | {
    id:string;revision:number;startDate:string;endDate:string
    days: StudyDay[]
    weeks: StudyWeek[]
    changes: {type:string;description:string;date:string|null}[]
    returnAfterPause:boolean
  }
  recommendations: {id:string;skill:Skill;title:string;reason:string;attemptId:string|null;target:string}[]
  evidence: {skill:Skill;band:number|null;practiceBand:number|null;status:string;
    attemptId:string|null;assessedAt:string|null;sampleCount:number;
    score:number|null;maxScore:number|null;source:string;limitations:string[]}[]
  overallBand:number|null
  progress: {date:string;completedAttempts:number;reviewedAssignments:number;skippedAssignments:number;
    plannedMinutes:number;activeMinutes:number|null}[]
  limitations:string[]
}
type StudyDay = {
  date:string;dayIndex:number;week:number;budgetMinutes:number;plannedMinutes:number;
  isRest:boolean;preliminary:boolean;assignments:StudyAssignment[]
}
type StudyAssignment = {
  id:string;kind:'PRACTICE'|'REVIEW'|'CHECKPOINT'|'REENTRY';skill:Skill;
  title:string;reason:string;durationMinutes:number;
  status:'PLANNED'|'IN_PROGRESS'|'AWAITING_RESULT'|'COMPLETED'|'SKIPPED'|'MISSED'|'BLOCKED';
  materialId:string|null;attemptId:string|null;sourceAttemptId:string|null;target:string;
  skipReason:string|null;completedAt:string|null
}
type StudyWeek = {
  week:number;startDate:string;endDate:string;goal:string;preliminary:boolean;
  mockOffer:{status:'AVAILABLE'|'SCHEDULED'|'DECLINED'|'DISABLED';durationMinutes:number;
    scheduledDate:string|null;windowMinutes:number|null;target:string}
}
```

studyDays use ISO weekdays 1=Monday … 7=Sunday; default [1,2,3,4,5,6]. dailyMinutes one of 30/45/60/90; total ordinary mandatory work must fit each selected day. duration is actual material duration, never truncate whole Writing/Reading to pretend it is a short exercise. If no suitable material, a truthful BLOCKED state with library/profile action; never fake content. Prefer available smaller published exercises where genuine (Reading passage) and meaningful review blocks. Avoid assigning the same material repeatedly as a new checkpoint. Future weeks preliminary. Four full weekly offers plus final partial-week overview as appropriate; do not promise a fifth full week.

## Completion, evidence and progress

Linked attempt IN_PROGRESS stays in progress, PROCESSING is awaiting result, SUBMITTED is completed. Authoritative completion is joined/reconciled from real attempt state (SQL projection is acceptable, outbox not necessary for this slice), not a browser completion checkbox. Review explicitly marks "viewed", never mastery or independent evidence. No user-supplied success scores.

Prioritise missing component practice, actual recent mistakes/recommendations, delayed review and skill maintenance. Reasons use evidence actually present; do not claim repeated TFNG in two new tests without backing rows. Existing AI feedback is labelled as such. No new AI calls needed to build plan.

New evidence model bypasses legacy user_skill_progress's unqualified estimated_band. Expose raw n/N for Listening/Reading with source, date and limitations. The current bank has no confirmed conversion provenance: practiceBand, band and overallBand remain null. In particular, partial scores are never scaled to 40 and converted to a displayed band; automatic numeric Writing/Speaking results are also withheld. A future qualified conversion/assessment path must provide provenance before these fields can contain numbers. Pending/failed AI is not zero. Partial/repeated/assisted attempts never become calibrated IELTS score. Stale after 28 days flagged; historic last value preserved. Latest eligible result, not maximum. Show dates/sample counts, never invented precision or confidence intervals. Existing attempt result/review pages remain intact.

Daily progress counts distinct real submitted work/reviews, not planned tasks or page views. activeMinutes=null without real instrumentation. Use user's IANA zone and calendar-day arithmetic, preserve DST. In absence of work_submitted_at document W/S completion-day limitation; don't call it precise active time.

## Skips, pause and weekly mock

Skip reason allowed `no_time|unsuitable|outside_app|other`. Outside-app is still skipped, not assessed. Idempotent repeated skip, stale revision returns 409. Past completed history cannot be edited. Start/skip future or past tasks? Current day actions only; future navigation read-only. Explicit resume previously started attempt may remain possible; never silently complete historic missed work from future unrelated attempt.

Replan recomputes remaining assignments without doubling any day. Pause >=7 calendar days prompts routing reentry using a genuinely suitable small exercise/review, does not alter bands. Six days not threshold. Stable versions and descriptions of changed assignments; don't erase completed/skipped history.

Weekly Full Mock offer always optional. Use actual existing overview duration (currently 165 min), don't hardcode a faster duration. At weekly overview (last selected study day) propose scheduling a date on upcoming seven days and explicit windowMinutes>=duration. Scheduling permits this explicit one-day exception, removes/supersedes ordinary planned tasks for that date without silently adding debt, and displays long-window warning/change. Decline persists for that week and does not penalize score/progress. Do not automatically start/pay/send anything. If bank incomplete, existing full-mocks page handles readiness; show that scheduling is a plan, not guaranteed available exam.

## Tests / deliverables

Unit tests for planner/calendar/score gating; real Postgres integration tests through handler and authenticated ownership, concurrency/idempotency, revision and rollback, 30-day budgets, no content, goals/settings, skipped days, pause 6/7/14, timezone/DST, pending grading, repeated materials, mock accept/decline/long-window. TEST_DATABASE_URL only with isolated testdb schemas. Browser tests for real user navigation/interactions and fake-API states; separately real API browser smoke for actual attempts/reviews. Unknown API routes in mocks must not silently succeed. No public production test-clock.

Frontend design brief is in parent delegated result, supplied in implementation prompt. Existing style, day/week/month URL navigation, no decorative card grid/gradients/side stripes, mobile >=360px, visible reasons and errors. Include summary in overview and progress using new API without breaking guest/practice.
