# Independent readiness audit — IELTS Academic study plan (devil's advocate)

Scope: independent audit only. No code changes, no child agents. Worktrees used: backend `/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan`, frontend `/Users/almat/Desktop/code/ielts/ielts-after-cigarette-adaptive-plan/iac-web`, branch `feat/adaptive-study-plan`. Sources: research methodology doc, BE API/test-report/README, planner code, FE live API fixtures. Synthetic materials are fixtures, not proof of a production content bank.

## Axis scores (0 absent, 1 partial, 2 functional, 3 pilot-validated)

| Axis | Score | Basis |
|---|---:|---|
| 1. Academic alignment | 1 | Planner filters materials by exam type and four sections; methodology doc defines Academic scope, but no verified Academic content inventory/provenance yet. |
| 2. Four-section balance / load | 2 | `planner.go:183-201` prioritizes sections without evidence and with stored wrong answers; one primary material/day; review capped to about a quarter of budget (`planner.go:277-281`). Functional scheduling, not pedagogically validated. |
| 3. Diagnosis / adaptation | 2 | Real attempt projection, raw n/N evidence, null bands, stale-after-28-days, pause reentry at 7 days, replan preserving history. Micro-skill tagging is not implemented (test report §boundaries). |
| 4. Feedback → correction → transfer | 1 | Review assignment exists and explicitly records viewing only (`README.md` "Reviews can refer... acknowledgement records VIEWED, never mastery"). No verified independent transfer check; review reason says viewing is not mastery. |
| 5. Content readiness | 0 | Only synthetic fixtures; no confirmed published bank, rights, conversion provenance, or editor validation. Code truthfully blocks empty/insufficient banks (`planner.go:429-461`). |
| 6. Assessment / evidence | 2 | `band`, `practiceBand`, `overallBand` stay null until validated provenance; raw scores and limitations exposed. No qualified L/R conversion or human W/S assessment yet. |
| 7. Product resilience | 2 | Versioned revisions, advisory locks, idempotency, timezone/DST, pause handling, optional mock, tests reported 30/30 backend studyplan and 54/54 frontend target. Archive UI is absent. |

## Verdict

- **Can launch as a planner/scheduler:** yes, functionally. It schedules real materials, preserves history, handles skips/pauses/revisions, and avoids fake bands.
- **Can launch as a full IELTS Academic course:** no. Three blockers remain: unverified Academic content bank; no qualified assessment path for overall; unvalidated feedback and no proven transfer.

## Top 3 blockers

1. **Content is not proven.** Synthetic fixtures and blocked-state handling are not a bank. Need 3 full new control forms per section, 8 short units per micro-skill, rights, answer keys, and editor validation (`research` §6.3).
2. **Overall cannot be honestly computed.** L/R conversion provenance and human W/S assessment are absent; null bands are correct. Do not add numeric estimates or confidence intervals.
3. **Feedback is viewing, not mastery.** Review acknowledgement must not be treated as evidence; delayed independent checks on unfamiliar material are still proposed, not implemented.

## Acceptance gates

- Content: published Academic inventory with provenance, rights, and editor sign-off; empty-bank blocking retained.
- Assessment: human W/S capacity, blinded agreement within ±0.5 at ≥80% lower bound, L/R conversion provenance; otherwise remain in study mode without overall.
- Feedback QA: ≥95% correct remarks, no harmful recommendations, ≥80% detection of priority problems on blinded new work before enabling feedback types.
- Transfer: independent success on new material before marking a skill improved; repeated old items remain practice only.
- Pilot metrics: diagnostic completion, completed correction cycles, one-week transfer, budget overruns, independent control-work change, dropout handling.

## Realistic 4-week structure at 45 min/day

Assumption: 6 study days/week, 1 rest day; 45 min/day = 270 min/week, about 18 hours in 4 weeks. At 45 minutes, a full 60-minute Reading test cannot fit in one day; use timed passages or schedule full tests only on longer days. Full Mock remains a separate explicit long window.

- **Week 1 — baseline and diagnosis:** Day 1 Listening full or passage set + error classification; Day 2 Reading passage/timed set; Day 3 Writing Task 1+2 untimed draft; Day 4 Speaking Parts 1–3 with audio; Day 5 review of dominant errors; Day 6 weekly review and next-week goals. Output: raw scores, missing components, two priority errors. No band claims.
- **Week 2 — correction:** alternate sections, one primary task/day plus one 10-minute review. Focus TFNG evidence, paraphrase, Writing overview/argument development, Speaking answer development. Correct, then retry on a new item.
- **Week 3 — transfer:** delayed checks on unfamiliar materials; second L/R sets; Writing revision on a new topic; Speaking Part 3. Count only independent new-material success.
- **Week 4 — consolidation and second checks:** repeat full/partial L/R forms, second Writing pair, Speaking check, final review of unresolved gaps. If Speaking grading is pending, show waiting status; do not replace prior result.

## Implemented vs proposed

- Implemented: scheduling, evidence gating, null bands, review acknowledgement, pause/replan, tests.
- Proposed only: micro-skill tagging, validated feedback quality, transfer verification, human assessment, content bank, archive UI, cancellation of a saved mock window.

## Sources

- `/Users/almat/Desktop/code/ielts/research/2026-10-24-ielts-learning-methodology.md` §§4, 6, 7, 13
- `/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/docs/ADAPTIVE_STUDY_PLAN_API.md`
- `/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/docs/ADAPTIVE_STUDY_PLAN_TEST_REPORT.md`
- `/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/README.md`
- `/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner.go:183-281,429-461`
- `/Users/almat/Desktop/code/ielts/ielts-after-cigarette-adaptive-plan/iac-web/tests/fixtures/study-plan-live-response.json`
- IELTS official scoring/conversion and descriptors: https://ielts.org/take-a-test/your-results/ielts-scoring-in-detail
