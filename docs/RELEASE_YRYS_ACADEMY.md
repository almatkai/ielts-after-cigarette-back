# Yrys Academy release candidate

Branch: `release/yrys-academy`, based on `origin/main` at `4e9df61`.
The user authorized assembling and pushing this release branch. Merging into
`main` and deploying remain separate actions.

## Included changes

- `fix/question-translation-backfill` at `33dcd2e`: question-review language
  support, migrations 43/44, corrected hint backfill and PostgreSQL regression.
  `e9bc0ba` selectively imports the translation feature from `9be2ee0`.
- `feat/yrys-academy-rebrand` at `946c2b9`: Yrys identity in the assistant and
  AI-provider requests, with the existing branding tests.
- `fix/expired-guest-entry` at `b663730`: recognized expired-trial response,
  paired with the frontend's localized sign-in state.
- `feat/yrys-domain-migration` at `b3f76bb`: production domain overlay and
  Google-client/upload configuration propagation through CI and Compose.

The release is built from production main and these feature/fix branches.
Google Drive backups, CI/database regression jobs, production deployment
scripts and the ADMIN media-access override are retained from main. Dev-only
asset retention, AI-provider timeout changes and adaptive-plan work are excluded.

## Migration and rollout dependencies

The frontend release includes the matching review-language requests and UI
localization. A production database that ends at migration 42 can apply the
corrected 43/44 normally. Confirm the actual migration state before deployment;
these notes are not evidence that a live database was inspected.

If 44 has already run, editing its file will not rerun it and a corrected rerun
does not safely identify contaminated explanation text. Follow the separate
reviewed repair process in
[`scripts/QUESTION_TRANSLATION_BACKFILL.md`](../scripts/QUESTION_TRANSLATION_BACKFILL.md).
No live data repair is performed by creating this release branch.

DNS, origin HTTPS and Cloudflare Full (strict) for `ielts.yrys.academy` were
prepared separately. Existing production images still use the old domain/brand.
Before the coordinated application release, review production CORS and
Turnstile hostname allowlists, Google authorized origins/callback and consent
links, and the image build/runtime variables. Preserve the old host while
clients drain; cookies and localStorage do not transfer between the domains.
See the landing repository's `docs/YRYS_DOMAIN_MIGRATION.md` for cutover/rollback.

## Combined validation

Executed on the combined branch with a disposable local PostgreSQL 17 cluster:

```sh
go test ./internal/attempts -run '^TestQuestionTranslationBackfillSQL$' -count=1 -v
go test ./... -count=1
go vet ./...
go build ./...
git diff --check origin/main..HEAD
```

All passed. The SQL regression executed the checked-in backfill and generator
output for both skills/languages, independent field updates and repeated runs.
The PostgreSQL fixture was stopped and removed after checking. Live databases,
Redis integration and external identity-provider behavior were not validated.

Workflow and Compose YAML parsed successfully. Direct comparisons against
main confirmed no changes to Listening/Writing media-access handlers, backup
operations, production deployment script or `Dockerfile.release`.
