# IELTS operational deployment — 2026-10-04

Target: `ssh -p 9865 almat@78.40.109.172`, production directories
`/home/almat/ielts-api` and `/home/almat/ielts-web`.
Only IELTS production was changed; blog/dev worktrees and other applications
were not modified. No Git push was performed. GlitchTip/Sentry remain excluded.

## Running

- Backend `iac-backend:reliability-20261004` and web `iac-web:reliability-20261004`.
  Linux amd64 binaries were cross-compiled from the current main backend
  checkout (including its uncommitted reliability changes); web was built with
  `VITE_API_BASE_URL=https://ielts.academy-ai.kz` and `/app/` base.
- External Writing worker, database pool 5. API pool 20. Speaking keeps the
  existing external STT/synchronous path; SPEECH_ENABLED remains false.
  A separate local Whisper service was not silently enabled.
- PostgreSQL 17: migration 29, pg_stat_statements preload and extension enabled.
- Prometheus, Grafana/IAC Operations dashboard, Loki, Alloy, read-only Docker
  socket proxy, backup textfile exporter.
- Dedicated private backup MinIO, scheduled daily PostgreSQL + media snapshots.
  Backup service account can upload only postgres/media prefixes; read/list and
  admin access are denied. Bucket is private, quota 5 GiB, PostgreSQL retention
  30 days and media retention 7 days; local retention defaults to 7 days.
- Monitoring services have memory limits; no public listener was created for
  Grafana, MinIO, metrics, Loki or the Docker socket proxy.

## Access and credentials

Credentials are **not stored in this repository**. On the server:

- `~/ielts-api/admin-credentials-20261004.json`, mode 0600: Grafana, MinIO,
  PostgreSQL, Redis, write-only backup account and existing application admins.
- `~/ielts-api/operations.env`, mode 0600: protected operational configuration.
- Application admins sign in with Google. Their passwords were not reset;
  password hashes/Google passwords were not exported.

For UI access:

```sh
ssh -p 9865 -N \
  -L 3300:127.0.0.1:3300 \
  -L 3320:127.0.0.1:3320 \
  -L 3321:127.0.0.1:3321 almat@78.40.109.172
```

Grafana: http://127.0.0.1:3300; MinIO console: http://127.0.0.1:3321;
MinIO API: http://127.0.0.1:3320. These addresses require the SSH tunnel.
PostgreSQL/Redis have no published production TCP ports.

Operational Compose wrapper on the server:

```sh
cd ~/ielts-api
scripts/operations-compose.sh ps
scripts/operations-compose.sh logs --tail=100 backend worker backup
```

Always include the operational overlays. The old single-file deployment
command does not preserve worker/metrics/pgstats configuration. Backend is
pinned in operations.env to the manual release; a later CI/main deployment
must first integrate this source/configuration and update the image reference.
The web image must likewise not be overwritten by an older main artifact.

## Backup and rollback artifacts

Pre-deployment environment/Compose files, PostgreSQL dump and all media volumes:
`~/ielts-api/backups/pre-observability-20261004/` (private directory).
Previous API/web images are retained as `iac-backend:rollback-20261004` and
`iac-web:rollback-20261004`.

Release bundles/build logs and non-secret restore report:
`~/ielts-api/releases/20261004-observability/` and corresponding web directory.

For ordinary application rollback, stop the external worker, select the retained
rollback image references and recreate only the application containers. Keep
production volumes and the compatible extra SQL index. Do not restore the old
DB dump over production unless a separately planned data-recovery operation
requires it: doing so would discard newer student data.

## Verified on the actual server

- `promtool check config`: valid Prometheus config and 9 alert rules.
- Loki verify-config and Alloy validate succeeded.
- Canary new API readiness and job metrics succeeded before application switch.
- Production API/Postgres/Redis healthy; worker started with Writing enabled.
- Prometheus API and backup scrape targets UP; Grafana datasource health OK.
- Loki has backend/worker/backup streams; provisioned dashboard exists.
- Grafana admin authentication and MinIO console login succeeded.
- POST through Docker log proxy denied (403); backup writer read/list denied.
- New application HTML/assets return 200 through production nginx; assets have
  immutable cache headers. Public /metrics is 404; unauthenticated attempts 401.
- Uploaded dump and media archive were downloaded from the real MinIO bucket;
  both SHA256 files verified. Dump restored to a separate temporary database,
  never production: 42 users, 3 attempts, 40 answers.
- Media archive: 57 files, every one of the 51 DB-linked objects present with the
  correct byte size. Temporary restore database/artifact copies were removed.
- Result: `releases/20261004-observability/restore-verification.json` on the server.

## GlitchTip (self-hosted) — 2026-10-04

Self-hosted GlitchTip is deployed in a separate Docker Compose project
`glitchtip` at `~/glitchtip` (postgres 16, redis 7, one all-in-one web image).
It reuses an immutable copy of the existing Woolet GlitchTip database volume
(legacy users/organizations preserved; Woolet project data untouched). The
separate production Nginx site `glitchtip.woolet.cc` terminates TLS with the
existing Let's Encrypt certificate and proxies to 127.0.0.1:8002.

- UI: https://glitchtip.woolet.cc (only admins already in the database:
  kairatovalmat@gmail.com, kairatovalmat2003@gmail.com; registration closed).
- Organization `ielts-after-cigarette` with two projects `iac-web` and `iac-api`
  was created through the server-internal API token.
- Backend (`iac-api` project): `SENTRY_DSN` is wired via operations.env, and the
  Go backend uses `github.com/getsentry/sentry-go` with the
  `httpx.InternalError` and panic-recovery seam. Background workers report
  writing/speaking failures with `attempt_id`/`recording_id` tags.
- Frontend (`iac-web` project): `VITE_SENTRY_DSN` was built into
  `iac-web:glitchtip-20261004`; Sentry is loaded lazily only after the first
  reported error (no initial bundle cost) and breadcrumbs are stripped so
  tokens/drafts never leave the browser.
- Both projects received smoke-test events; both are visible as issues.

Pending: source map upload for readable frontend stack traces, release tagging
in CI, and optional per-channel alert delivery inside GlitchTip.

## Explicitly still pending

1. **Off-host backups:** MinIO is on this same server. This protects from some
   application mistakes but not loss of the host/disk. Metric
   `iac_backup_offhost=0` and BackupNotOffHost warning make this visible.
   Configure an external private S3/R2 destination with its own credentials;
   then test download/restore again. No external account credentials were guessed
   or copied from other projects.
2. **Alert notification delivery:** rules are active, but no Telegram/email/
   external contact point was configured because no destination/channel secrets
   were supplied. Test actual delivery before relying on notifications.
3. Error tracker remains deferred. Public/dashboard caches, SSE and presigned
   media URLs still require the separate application work listed in RELIABILITY.md.
4. MinIO public community images could not be pulled. This deployment uses the
   exact existing official image already present on the server:
   `minio/minio@sha256:14cea493d9a34af32f524e538b8346cf79f3321eff8e708c1e2960462bd8936e`,
   version RELEASE.2025-09-07T16-13-09Z, tagged locally `iac-minio:2025-09-07`.
   No unrelated MinIO instance/bucket was modified and no third-party mirror
   was used. A fresh host needs a verified source-built image or a retained
   image export; review security upgrades before exposing it beyond private access.
5. Media snapshots run while the application is online. DB and filesystem are
   not a single atomic snapshot; concurrent replacement/deletion of a recording
   can require consistency coordination. The completed snapshot was verified
   against all database-linked files. For a guaranteed recovery point under busy
   uploads, implement snapshot/retention coordination or a brief maintenance
   window rather than claim cross-storage atomicity.
