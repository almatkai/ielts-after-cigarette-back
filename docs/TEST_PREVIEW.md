# Admin test preview

Reading and Listening previews are read-only projections of saved versions. They do not create attempts, save answers, grade submissions, or publish material.

## Access

Both endpoints require authentication and the `ADMIN` role. `STUDENT` and `EDITOR` receive `403`; unauthenticated requests receive `401`. Existing editor access to authoring endpoints is unchanged.

- `GET /api/v1/admin/reading/materials/{materialID}/preview?version=draft`
- `GET /api/v1/admin/listening/tests/{testID}/preview?version=draft`

`version` may be `draft` (default) or `published`. Other values return `400`. A draft without a published version returns `404` for `version=published`. Archived materials can be previewed using `draft`, but not as an active public test.

Response:

```json
{
  "material": {},
  "answerKeys": {
    "question-uuid": {
      "answer": { "optionId": "A" },
      "explanation": "Why A is correct.",
      "quote": "Exact evidence from the passage or transcript.",
      "hint": "First paragraph."
    }
  },
  "versionNumber": 2,
  "revision": 4,
  "status": "PUBLISHED",
  "hasUnpublishedChanges": true
}
```

`material` uses the same public DTO as student tests. Reading includes the passages pinned to the selected test version. Answer keys, explanations, quotes, and hints are excluded recursively from this public projection, including arbitrary content/config JSON. The ADMIN-only `answerKeys` sidecar is keyed by question UUID and uses exactly the same selected version, including pinned Reading passages; it is never returned by public/student or in-progress attempt endpoints. `versionNumber` identifies the selected content version; `revision` is the material's current optimistic-lock revision.

Responses use `Cache-Control: private, no-store`. Listening media retains its existing authorization: students may retrieve only assets referenced by published test versions; editors/admins may access draft assets for authoring. Media responses also use `private, no-store`.

Normal public lists and test routes remain published-only. Saving a new draft does not change the previously published snapshot or its media references.

## Frontend

Standalone routes (with the application's `/app` base):

- `/app/admin/preview/reading/{materialID}`
- `/app/admin/preview/listening/{testID}`

Admin libraries link directly to saved drafts. Editors save dirty forms before navigating; failed saves keep the user in the editor. Preview uses the student runner with a local-only answer adapter, no attempt ID, and an optional in-memory timer. Switching versions, resetting, or reloading discards test answers and revealed explanations. No grading is exposed. Every question has an administrator-only “Показать ответ с объяснением” toggle: correct answer (including accepted variants/multiple options), evidence quote, and explanation below the question. Reading highlights and scrolls to the stored quote in the current passage; Listening highlights the quote in the transcript when one is available. Quotes and explanations are rendered as text, never HTML. Missing author data or a quote that does not match the selected text is explicitly flagged rather than fabricated. Student runners have no answer-key toggle.

## Verification

Backend unit/route tests:

```sh
go test ./internal/reading ./internal/listening ./internal/app
```

Version and media integration tests require an explicit isolated test DB:

```sh
TEST_DATABASE_URL='postgres://.../test_database?sslmode=disable' go test ./internal/reading ./internal/listening
```

Frontend (in `iac-web`):

```sh
pnpm exec playwright install chromium
pnpm test:preview
pnpm test:regression
pnpm exec tsc --noEmit
```

Playwright starts its own server on port 3037 and intercepts all API calls. It does not contact a real account or database.
