# Writing feedback deployment to the isolated development site

The development site at `https://ielts.woolet.cc` runs the Compose project
`ielts-guest-dev` from `/home/almat/ielts-guest-dev`. GitHub Actions on `dev`
run checks; they do not deploy this site. A successful push alone therefore
does not publish Writing feedback changes.

## Published artifacts

- Frontend revision: `4758e51ff4ab486e731e8955106fced234cb46e9`.
- Backend and worker revision: `351f70857768515bfb3a69e8eca7e77a312d6acf`.
- Web image: `iac-web:guest-dev-20261013-writing-feedback`.
- Backend/worker image: `iac-backend:guest-dev-20261013-writing-feedback-final`.
- Release files and private backups: `releases/20261013-writing-feedback/`.
- Frontend build used `VITE_API_BASE_URL=https://ielts.woolet.cc`.

Both application images were built from the integrated development branches.
Go binaries built on Windows require executable permissions before the Linux
Docker build. Recreate the isolated gateway after replacing backend or web
containers so its upstream addresses resolve to the new containers.

## Missing schema dependencies

The database reported migration version 39, but only the guest development
migrations at versions 35–39 had been applied. Three migrations added to main
later reuse development migration numbers:

- `000035_blog_and_writers.up.sql`
- `000036_user_management.up.sql`
- `000037_editor_permissions.up.sql`

The current backend uses their account-state fields during authentication;
the version number alone does not establish schema compatibility. A complete
private `pg_dump -Fc` backup was taken before applying the missing SQL in one
transaction with `ON_ERROR_STOP=1`. The blog migration's role constraint was
adapted to preserve existing `GUEST` accounts. The exact applied SQL is saved
as `missing-main-migrations.sql` in the private release directory. Existing
guest clocks, pause, and claim migrations remain applied; migration version
39 was preserved. Resolve the duplicate version numbers before relying on
the normal migration runner for a new development database.

## Historical Writing assessment

The reported historical attempt already contained both original essays, but
its stored assessment had no annotations. A scoped maintenance utility used
the current evaluator to generate annotations and model paragraphs. The
suggestions were reviewed against the original essays before saving. Only
these two optional feedback fields in each task were updated, with an
optimistic comparison against the original feedback. All criterion bands,
the overall band, timestamps, and original answers were preserved.

The before/after JSON is retained privately in
`attempt-feedback-reviewed-patch.json`. This was a single-attempt enrichment;
other historical assessments were not regenerated.

## Verification

- Public HTML and the new attempt-review asset return HTTP 200, with the
  development environment header.
- Authenticated public attempt and mistake-detail API requests both return
  the two essays, one exact-quote suggestion per task, and overall band 8.0.
- The web, backend, and worker run the image tags above; backend readiness
  reports PostgreSQL, Redis, and object storage healthy.
- Google login loads its configured client successfully. The private review
  screen still requires the owner's browser session for visual inspection.
- Frontend TypeScript, full ESLint, production build, and GitHub CI pass.
- Five Writing browser tests passed on the first run; the narrow-screen test
  exceeded its initial render wait and passed when rerun alone at all four
  viewport widths. Backend attempts tests and GitHub CI pass.

## Application rollback

`compose.before.yml` in the release directory retains the previous isolated
application image tags. Restoring that Compose file and recreating only
backend, worker, web, and gateway rolls back application binaries. Do not
restore the database dump merely to roll back an application image: that
would discard subsequent development activity. The dump and per-attempt
feedback backup support a separately scoped recovery if required.
