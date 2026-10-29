# Billing-only production release

Based on freshly fetched `origin/main` (`git merge-base origin/main HEAD` at
preparation: see PR base), with exactly these imported source commits:
`813b40c`, `81f4789`, `6d9eb9a`, `2826d92`.
No adaptive study plan, asynchronous assessment, Cloudflare/dev integration or
`778adc2` is included. Existing main features remain unchanged except billing
hooks and the documented production environment correction.

## Release behavior and configuration

Paywall is enabled now; access is granted manually by ADMIN until BCC exists.
GitHub **production** variables set during preparation:

- `PAYWALL_ENABLED=true`
- `PAYMENT_PROVIDER=none`
- `PAYMENT_RETURN_URL=https://ielts.yrys.academy/app/billing/return`

The deploy job exports those settings into generated `production.env`; Compose
passes them into the API (and the worker inheriting backend configuration).
Checkout returns 503 without creating orders. No fake production payment path
is enabled. `APP_ENV=production` is fixed in production Compose, so config and
provider guards both reject fake payments. Production secret checks, guest
Turnstile/secure cookies and AI telemetry checks also apply. GitHub production
already has secure cookies, Turnstile settings and SENTRY_DSN configured; secret
values and live application startup were not inspected during this preparation.
Server-local `operations.env` is loaded after `production.env` by the existing
deploy flow: before merging, verify it does not override these three billing
settings or contain stale backend image settings. Never print its secrets.

Migration `000045_billing` follows main's `000044`; no old migration was edited.
It adds billing tables and seeds three plans plus default quotas (three lifetime
practice starts per skill, five free assistant calls daily). Earlier practice is
not backfilled. The existing deploy script runs migrations before replacing the
API/worker; failure aborts replacement. Preserve a verified DB backup. Roll back
access enforcement by setting PAYWALL_ENABLED=false and redeploying; preserve
billing tables/history instead of applying the destructive down migration.

## Review scope

Billing plans/settings, orders, entitlements, administrator APIs, provider
boundary, free quotas and their atomic integration into practice/full mocks,
guest claim and assistant. Nested payment-required response details retain
compatibility with existing string validation details. Owner order responses
include the documented provider and providerOrderId fields.

The only release-specific wiring is PR-to-main tests, PostgreSQL plus Redis CI
services, production billing variables/env generation, APP_ENV correction and
release documentation. Test has no GitHub environment, so PR checks do not need
protected deployment-environment access. Build and deploy remain main-only;
main deploy still gates on both tests and build.

## Validation

Passed on the final code using a disposable native PostgreSQL 17 cluster and
Redis bound to localhost; TEST_DATABASE_URL and TEST_REDIS_URL were supplied:

- `go test -race ./... -count=1`
- `go test -race ./internal/app ./internal/billing -count=1 -v`
- `go vet ./...` and `go build ./...`
- Workflow/production Compose YAML parse, deploy script `bash -n`, diff whitespace
- Mocked deploy success/failure verifies migration-before-API and no API
  replacement after migration failure

Billing integration includes concurrent quotas, rollback/idempotency,
entitlement windows/revocation, assistant Redis allowance and paid transition,
HTTP ownership/admin checks, guest cookie restrictions, production manual grants
and unavailable checkout creating no order. DB/Redis cases were enabled locally
and are enabled in CI. An initial run caught the owner-order response missing
provider metadata; the final full suite passed after that correction.

Docker daemon was unavailable: no container build/start or real Compose deploy
was executed. No production database/server changes, merge or deploy were made.
XERJ indexing was explicitly skipped due the known disk flood block; rg was used.
Backend must deploy before the separately reviewed frontend and landing PRs.
