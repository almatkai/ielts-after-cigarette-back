# Adaptive study plan production release validation (2026-10-30)

Base: `origin/main` at `bb3dc6d085411f0c342e33dc7105da1829a2f0f2`.
Branch: `feat/release-adaptive-study-plan`.

## Scope and conflict decisions

Selected source commits: `edcd7e9`, `4953d79`, `09bb9af`, `778adc2`.
No dev merge. The app routing conflict retains both main's admin billing routes
and the new editorial routes. Billing integration from `e17853d` is already
present via production `58b3b85`; their added code matches, so no billing dev
commit or migration is imported. Existing production deployment, CI, domain,
backup, billing and config files remain unchanged. Static asset retention is
limited to `ops/cloudflare-dev` and its dev documentation.

## Migration and rollout order

1. Keep existing production `000045_billing` applied. All 90 migration files
   from main were compared byte for byte and are unchanged.
2. Apply `000046_adaptive_study_plan`, then
   `000047_learning_content_metadata` using the existing deployment migration
   step. Only the two new feature migration pairs were renumbered.
3. Deploy this backend before the frontend release. Read-only verification of
   the live server confirmed that production uses the operational Compose
   overlays with `WRITING_WORKER_EXTERNAL=true`, an external worker and shared
   Speaking storage. Retain that topology and use the same release revision
   for API and worker; the existing deploy script recreates both. The base
   Compose without overlays can use in-process workers, but is not the live
   production topology. Keep the current `PAYWALL_ENABLED=true`,
   `PAYMENT_PROVIDER=none` configuration; manual grants work without an online
   payment provider.
4. Verify `/health/ready`, authenticated study plan/learning routes and durable
   Writing/Speaking job processing, then release the frontend.

The new upgrade test starts at the actual main schema through billing 45,
seeds customized billing plans/settings, a paid order, an admin entitlement
with consumed meetings, and free usage. It checks all billing rows unchanged
after 46/47 up, 47/46 down, and up again. Rollback drops plan/editorial history;
prefer reverting application binaries while leaving additive tables in place
once students have used the feature. Never run down in production casually.

## Executed checks

Logs: `/tmp/ielts-release-validation/` on the preparation machine.
No application `.env`, live database or production credentials were used.
Docker was unavailable; dedicated PostgreSQL 17 and Redis processes were
started on loopback ports 55483 and 56483, with files under the log directory.

```sh
initdb -D /tmp/ielts-release-validation/pgdata -A trust -U release_test
pg_ctl -D /tmp/ielts-release-validation/pgdata \
  -l /tmp/ielts-release-validation/postgres.log \
  -o '-p 55483 -h 127.0.0.1 -k /tmp/ielts-release-validation' start
createdb -h 127.0.0.1 -p 55483 -U release_test release_test
redis-server --bind 127.0.0.1 --port 56483 --save '' --appendonly no \
  --daemonize yes --pidfile /tmp/ielts-release-validation/redis.pid \
  --logfile /tmp/ielts-release-validation/redis.log
TEST_DATABASE_URL='postgres://release_test@127.0.0.1:55483/release_test?sslmode=disable' \
TEST_REDIS_URL='redis://127.0.0.1:56483/0' \
  go test -race -count=1 -json ./...
go vet ./...
go build ./...
python3 -m unittest discover -s ops/backup -p 'test*.py'
git diff --check
```

- Final full race/integration suite: **703 passed**, **0 failed**, **0 skipped
  tests**, 31 passing packages (`go-race-final.json`). Packages without tests
  are reported separately by Go.
- Migration upgrade/rollback: passed (`migration-upgrade.log`, also in the final
  full suite).
- Added real-service plan-start billing regression: denied start returns 402,
  creates no attempt and does not advance the plan; an admin grant then permits
  start without consuming free quota. Passed in the final full suite. Its first
  run failed because the test grant omitted required `source: ADMIN`; fixed the
  fixture and reran all tests.
- `go vet ./...` and `go build ./...`: passed (`go-vet-final.log`,
  `go-build-final.log`).
- Backup unit suite: 3 tests, 2 passed and 1 Docker smoke test skipped because
  Docker is unavailable (`backup-tests.log`). Production backup code unchanged.
- `git diff --check`: passed.

XERJ per-worktree index `ax-2d841c8b54ad-rel1030` was attempted. The old prefix
required schema evolution; the fresh prefix was blocked by the local server's
96% disk flood-stage watermark (HTTP 429), so its index is incomplete. Used
`rg` for exact source verification. This is a local search-tool limitation.

No merge, deployment, production configuration change or live-data action was
performed. GitHub checks/review remain required on the release PR.


## CI follow-up: bounded-pool plan lock contention

CI run `41049963448` timed out in `TestConcurrentCreateHasOnePersistedCycle`.
The runner's default pool has four connections. The lock owner needed another
connection to read Full Mock availability while competing plan transactions
occupied every connection waiting on the same blocking advisory lock. The
same wait pattern can occur when starting an assignment.

Reproduced locally before the fix with `pool_max_conns=4`: the original HTTP
concurrency test timed out, and the added regression returned its context
deadline after five seconds. Plan transactions now try the same PostgreSQL
transaction-scoped advisory lock without blocking. An unsuccessful waiter
rolls back and releases its connection before a cancellable 10 ms retry delay.
The database lock continues to enforce serialization across service instances.
Neither pool sizes nor CI timeouts were increased.

The new real-database regression forces four connections and ten concurrent
callers, verifies all creates return one cycle, then verifies competing starts
produce exactly one persisted attempt claim and revision conflicts for losers.

Validation after the fix:

```sh
TEST_DATABASE_URL='postgres://release_test@127.0.0.1:55483/release_test?sslmode=disable&pool_max_conns=4' \
  go test -race ./internal/studyplan \
  -run 'TestConcurrent(CreateHasOnePersistedCycle|PlanMutationsWithSmallPool)$' \
  -count=5 -timeout=40s
TEST_DATABASE_URL='postgres://release_test@127.0.0.1:55483/release_test?sslmode=disable&pool_max_conns=4' \
TEST_REDIS_URL='redis://127.0.0.1:56483/0' \
  go test -race -count=1 -timeout=3m -json ./...
go vet ./...
go build ./...
```

All five focused repetitions passed. Full suite: **704 passed, zero failed or
skipped tests**, with PostgreSQL/Redis and the four-connection pool. Vet/build
passed. Evidence: `/tmp/ielts-release-validation/go-race-pool4.json`,
`go-vet-pool4.log`, `go-build-pool4.log`, `ci-repro-small-pool.log` and the
captured failing CI log. Production data and configuration were untouched.
