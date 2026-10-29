# IELTS native Cloudflare DEV backend

This is the **deployed native DEV backend**, built from the selected DEV application. It uses ordinary Workers, SQLite Durable Objects, including private blob storage with no R2 subscription. There is no backend origin proxy, Container, PostgreSQL connection, Redis server or MinIO connection in the runtime. Production application source, Go modules, Dockerfile and deployment paths are unchanged. The root `.dockerignore` only excludes this new tooling directory, so private exports and local build dependencies cannot enter the production Docker context.

The real-data cutover completed on 2026-10-28: **https://ielts.woolet.cc/app/** now uses the native frontend, API, SQLite data/blob storage and audio Worker. All ten current and legacy IELTS DEV containers are stopped; their source volumes are retained. The existing `ielts-dev` Worker is now a tiny private service-binding frontdoor, with its VPC origin binding removed. The temporary migration helper and local OAuth bridge have been removed/stopped. See [native cutover evidence and rollback boundaries](NATIVE-CUTOVER.md).

## Selected source and reproducibility

- Source: `40ed0dc5fca0b9df989a29d421a81476e7df0f50`.
- Image: `iac-backend:guest-dev-adaptive-40ed0dc`.
- Immutable image ID: `sha256:3cc32d093c0898032ab50a8d62972f606f09f91e4e111661950630cee063edba`.
- Both original Linux executables reproduced byte-for-byte with Go 1.26.10, `CGO_ENABLED=0`, `-trimpath -buildvcs=false -ldflags='-s -w'`. See [provenance.json](schema/provenance.json) and `tools/provenance.py`.
- The builder exports that exact Git object to an ignored archive and verifies every archived file before and after compilation. It never checks out or edits another branch/worktree. Compilation uses Go `-overlay`; the archived application bytes remain unchanged. A fresh clone must contain this exact Git object before building.
- `dist/manifest.json` records source/archive/Wasm hashes, toolchain, adapter Git head and release-input hashes. Build outputs are ignored, not committed.

The selected frontend is `b05d0622b7bfcc44dcddad3544c6e6d565041019`, prepared independently in frontend commit `ef1eda4393079115b15e2395c09c1fea7c7aabd9`. Its `iac-web/cloudflare/README.md` and `worker.mjs` define the same-origin `/api/v1/*` proxy. No frontend files are modified here.

## Runtime mapping

| Component          | Native implementation                                                                                                                                                                              |
| ------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Public backend     | `ielts-api-dev`, thin forwarding into `IELTSDev`                                                                                                                                                   |
| Application/domain | Original Go router, services, validators, evaluators and repositories compiled to Wasm; lazy initialization inside DO                                                                              |
| PostgreSQL         | SQLite in singleton `IAC_STATE.idFromName('ielts-dev-v1')`; source-specific SQL adapter, transactions and constraint mapping                                                                       |
| Redis              | `cf_coordination` SQLite storage: counters, TTLs, rate/daily limits, hashes, sorted sets, HLL and queue hints                                                                                      |
| MinIO              | Private `ielts-blob-dev` catalog/chunk SQLite DOs; original object keys retained                                                                                                                   |
| Writing worker     | Original Go claim/evaluate/recover code driven by persistent DO alarms, backoff and lease recovery                                                                                                 |
| STT ffmpeg         | Private `ielts-audio-dev` Worker and singleton `IACAudio` DO, precompiled FFmpeg Wasm; continuous 16 kHz mono WAV capture through bounded SQLite scratch chunks, then multipart SQLite blob output |
| Real-data transfer | Temporary binding-only named HTTP helper, removed after verified activation; no API import routes                                                                                                  |

Private Prometheus output is retained through `IAC_STATE` binding RPC `getMetrics()`; it is never registered on the public API. Job counts query SQLite, Go/runtime counters describe the Wasm runtime, and the legacy connection gauges describe the single native SQL connection. The VPS TCP metrics listener is replaced by this binding; Cloudflare supplies isolate CPU/memory infrastructure metrics.

All 165 original HTTP registrations are retained, including adaptive study plans, learning corrections/sessions/editorial review, guest exams and claiming, full mocks, material imports/versioning, admin operations and media. [routes.json](schema/routes.json) is obtained from the actual Chi router. [coverage.json](schema/coverage.json) distinguishes executed routes/statuses; it is not a claim that every input combination has been tested.

Transaction ownership is propagated through Go contexts. Requests cannot execute SQL/coordination operations inside another request's transaction. The source's deferred rollback before writing a large read-only response is released as a rollback before crossing the response-stream input gate. It is never implicitly committed. Atomic answer saves, refresh rotation, exam clocks and duplicate mutations have real SQLite tests. A schema fingerprint rejects incompatible future schema changes instead of silently reusing an incompatible database.

Multipart files spill to private temporary SQLite blob objects. Authorized full/single-range downloads stream directly from bounded SQLite blob chunks while Go still decides headers, access and range semantics. Multiple ranges use bounded reader caching. Speaking audio is encoded into the original provider JSON format from blob storage without constructing large base64 payloads in Go memory. Original provider routing, credentials, prompts and response parsing remain in Go. Outbound I/O uses Workers Fetch, not Go TCP/DNS sockets; persisted public providers retain endpoint validation and redirects remain disabled.

One small isolated compatibility repair resolves writer certificates from the existing application/storage-key relationship: the selected source DTO does not populate `CertificateMediaID`. The source archive and production implementation are unchanged; this repair and ownership/byte-preservation checks are in the overlay and tests.

The exact live configuration has `SPEECH_ENABLED=false` and synchronous Speaking STT/assessment; the optional Python speech-metrics pipeline is not used. Enabling that separate pipeline is explicitly rejected by this adapter. There are zero live `speaking_assessment_jobs` rows in the observation. This is a documented configuration boundary, not a hidden stub route.

## Local build and checks

Requirements: Node 26 (offline schema validation uses `node:sqlite`), Python 3, Go with toolchain download/cache access, and enough local memory to compile Go. The tests additionally use local `ffmpeg`, `redis-server` and `redis-cli`; they start only their own isolated Unix-socket Redis oracle. Neither service is a runtime dependency.

Run inside this directory:

```sh
npm ci
npm run build
npm run schema
python3 tools/provenance.py
npm test
npm run check
```

`check` is Wrangler **dry-run only**, for the API, audio and private blob bundles. Tests build separate Miniflare/workerd workers with real SQLite application/blob DOs and synthetic identities/provider responses. Test entrypoints, SQL hooks and mocks are never the deployment entrypoints. `IAC_REQUIRE_IMPORT` defaults to `true`; a newly created API returns `DEV_DATA_NOT_ACTIVATED` until verified real data is activated.

Normal production validation remains `go test ./...` from the repository root. The nested `cloudflare/go/go.mod` keeps js/wasm adapter packages outside that normal Go module.

## Data accounting and private transfer

[schema/postgres.json](schema/postgres.json) is derived by applying all 47 actual selected-source migrations offline. It contains 55 application tables, 330 explicit constraints, indexes and triggers. `sqlite.json` adds exactly those objects to an already imported DEV store on startup (see [Schema upgrade](#schema-upgrade)) and additionally enforces native type/length/JSON constraints. `schema_migrations` is preserved as source metadata, not used to infer the live application's schema.

The read-only observation in [dev-observation.json](schema/dev-observation.json) records PostgreSQL 17.11, 29,808,307 database bytes, 15,540 application rows and ledger version 39/clean. All application column definitions matched the selected source. PostgreSQL and PGlite deparse some equivalent check-constraint casts differently; this observation does not assert textual equality of those expressions. The final quiesced export/import preserved 50 tables, 15,540 rows, 52 objects (278,008,907 bytes) and 68 coordination keys; see [NATIVE-CUTOVER.md](NATIVE-CUTOVER.md).

The approved cutover exporter:

1. Guards the exact DEV backend image and fixed DEV container names. It does not accept a production target.
2. Takes a read-only repeatable-read PostgreSQL snapshot and exports every column of all 50 tables in primary-key order. It preserves UUIDs, UTC timestamp precision, JSON/arrays, binary data, numeric strings, float64 wire values, migration ledger and sequence values.
3. Enumerates every current MinIO key, including unreferenced objects, and writes private bytes plus size/content type/SHA-256 manifests.
4. Archives Redis DUMP bytes and restores mapped typed state with absolute expirations. HLL registers/cardinality are retained, including future additions. Derived grading caches are archived and rebuilt; list queue hints are accounted for through durable SQL jobs. Unknown key/type mappings fail explicitly.
5. Writes only to a new ignored `.sensitive/` directory (directory 0700, files 0600). Logs contain counts and hashes, never credentials or row contents. MinIO/Redis credentials stay in remote process memory.

`tools/transfer.mjs` seals the private pack, verifies table hash chains/counts, native values/row bounds, file modes and every object checksum, cross-checks stored media references/byte sizes, and accounts for unreferenced current keys, then reports conservative write-budget estimates. Without `--execute`, it makes no remote calls. The real SQL exporter, canonicalization and sealer are exercised together against actual migrations in the workerd import test.

The importer durably stages bounded records, verifies completeness/checksums, then publishes the entire relational dataset in a synchronous transaction with deferred-FK verification. Awaiting an input stream inside that transaction is deliberately avoided. Invalid/incomplete imports keep the API gated. Activation requires exact private-blob key count, sizes, content types and streamed native SHA-256 checksums. The same completed import ID is idempotent and never overwrites subsequent application activity; a different import is rejected. Only a pristine target can receive data.

## Migration procedure for a future fresh target

The current DEV cutover is complete. This procedure describes the gates for a new target; do not rerun it against the active imported object or reactivate an old source snapshot.

1. Review the full branch diff/commit list against main and the test results. Confirm Free-plan capacity in the shared account. The current conservative database estimate is **approximately 57,000 published row/index writes**, plus bounded 128 KiB staging chunks, cleanup, metadata, coordination and private blobs; the sealed pack computes the complete bound. Reserve a fresh daily budget, accounting for Drone and other applications; schedule migrations on separate UTC days if needed. No paid-plan upgrade is required or configured.
2. Deploy `wrangler.blob.jsonc` first, then the new audio and API Worker/SQLite DO resources from the supplied configs, in account `ab39c7a0ec52f861379630cccb01061f`. Deploy the private audio Worker before the API binding. The public API must remain import-gated. Do not change the existing proxy, DNS or production deployment.
3. Supply the **existing selected DEV** application secrets/configuration securely: JWT and phone-verification secrets, Google client ID, Turnstile site/secret/hostnames, current AI/STT URLs/keys/models, existing provider encryption keys (including previous key when configured), current limits, admin allowlist and any configured Infobip values. Reuse secrets to preserve refresh sessions, guest proofs and encrypted provider credentials. Do not create an AI provider/key. `APP_ENV` is forced to development and native DB/storage settings are internal. Keep `SPEECH_ENABLED=false`, `WRITING_WORKER_EXTERNAL=true`, `GUEST_TRIAL_ENABLED=true`, and the selected telemetry-optional policy. Do not copy VPS database/Redis/MinIO credentials or VPS telemetry endpoints into this deployment.
4. Match CORS, frontend URL, secure cookie/domain settings, Google allowed origins and Turnstile hostnames to the separately prepared `ielts-web-dev` frontend. `IAC_ADAPTER_TEST` and `CF_TEST_MODE` must be absent for real data. Default logs are disabled.
5. The private blob catalog expires `_cf-temp/` objects and incomplete uploads after one day using persistent alarms. Original object keys never receive an automatic expiry. Retired versions retain a one-day read grace period; discarded staging chunks are collected independently. No R2 lifecycle API or subscription is involved. The exporter rejects a source pack using the reserved temporary prefix.
6. During the approved cutover, quiesce **all DEV writers**, including in-flight jobs and uploads, through root's cutover procedure. The exporter does not stop services or enforce quiescence. Keep the old DEV data intact for rollback.
7. Export to a fresh private directory and seal locally:

   ```sh
   python3 tools/export-dev.py --execute --quiesced --output .sensitive/approved-cutover
   node tools/transfer.mjs --directory .sensitive/approved-cutover
   ```

   Review all 50 table counts, coordination dispositions, object key/checksum totals and the exact quota estimate. The actual object/key/Redis inventory remains unverified until this approved export; unknown state blocks transfer rather than disappearing.

8. Deploy the binding-only helper (`wrangler.migration.jsonc`) and start the
   [local OAuth bridge](tools/migration-local/README.md). Public HTTP always returns
   404; no migration token is created. Only Wrangler's authenticated remote service
   binding grants access. Keep the bridge on `127.0.0.1:8794`.

   ```sh
   node node_modules/wrangler/bin/wrangler.js dev --config tools/migration-local/wrangler.jsonc
   node tools/transfer.mjs --directory .sensitive/approved-cutover --execute --binding-local --endpoint http://127.0.0.1:8794
   ```

   Object preflight avoids reuploading matching bytes; differing objects are never
   overwritten. Retrying the same pack is supported. Inspect the private import
   receipt and local bridge `/report` before activation.

9. Activate separately only after receipt/count/checksum review:

   ```sh
   node tools/transfer.mjs --directory .sensitive/approved-cutover --execute --activate-only --binding-local --endpoint http://127.0.0.1:8794
   ```

10. Verify actual Cloudflare startup, total memory, billed CPU, quotas and real provider interoperability before directing the DEV frontend to the native API. Exercise existing login/refresh, guest claim, audio STT, async Writing retry, media ranges, adaptive learning and admin operations with approved accounts. Root then deploys/connects the prepared frontend. This is the point at which a full migration can be declared; successful local bundles alone are not that claim.
11. Stop the local OAuth bridge and remove the migration helper after cutover. Keep private backups/receipts according to the owner's retention policy. Do not drop old DEV data or stop services as part of these local tools. Switching back to the intact old deployment is straightforward before native writes; after native writes, a reverse data reconciliation is required to avoid losing those new writes.

## Measured bounds and remaining release gates

- Local contract coverage: passing real workerd checks with all **165/165** registrations exercised, including success and negative cases. This covers SQL-backed mutations, ownership/revocation, refresh reuse, guest section clocks/claim, full mocks, learning/idempotency/editorial history, SSE concurrency, imports, uploads, admin hashing/deletion, durable retries, DO reset, native grading-cache rebuild and private metrics. PostgreSQL extrema preserve NULL semantics, including admin timestamps for accounts without profiles. It does not prove every historical-data combination or every provider's behavior.
- A 50 MiB multipart upload/full download preserves SHA-256 and uses one bounded SQLite blob response stream; Go Wasm linear memory stays well below 100 MiB. Three 12 MiB Speaking recordings reach the synthetic provider with exact decoded checksums at about 51.5 MiB Go linear memory.
- The separate audio core uses 32 MiB linear memory in workerd while decoding an 800-second/11 MB WebM into a continuous 25.6 MB WAV. WebM/Opus and Ogg tests preserve full reference PCM length; AAC padding differs by about one codec frame against the workstation's newer native decoder. The selected image's ffmpeg is 6.1.2; the pinned Wasm core is a separate codec build. No sample-window slicing is used.
- Linear-memory figures exclude V8, SQLite and other isolate overhead. Local elapsed timings are not billed Cloudflare CPU measurements. Native API, SSR and audio requests passed platform enforcement. The frontend module startup was 11 ms; the 180-second native audio conversion took 1,149 ms DO CPU with 32 MiB Wasm linear memory. Exact total isolate peak and exhaustive per-route CPU bounds were not exposed by these checks; no sub-10 ms bound is inferred from a successful temporary helper request. See the native receipts. Go and codecs initialize lazily in DO requests, not at Worker module evaluation.
- Native SQL limits still apply: 2 MB row/value, 100 bind parameters, 32 function arguments and 1 GB per Free object. Long admin searches and large JSON construction are adapted to those limits; packs outside native bounds are rejected. The current observed data fits; arbitrarily larger future datasets are not promised to fit one Free object.
- Singleton storage serializes application transactions and is intended for this DEV workload. It is not a production-scale horizontal database design.
- Real quiesced data/object import and activation are verified. Source MinIO version history/ACLs remain outside the application's current-key media contract; current keys, bytes and content types were transferred. Real AI/STT/Google/Turnstile/Infobip provider transactions were not made by acceptance checks; existing settings and public host authority were preserved.
- Native SQLite schema upgrades need an explicit data-preserving migration. The fingerprint guard will refuse an incompatible schema rather than replacing existing data.

Official constraints and APIs checked for this implementation: [Workers limits](https://developers.cloudflare.com/workers/platform/limits/), [Durable Object limits](https://developers.cloudflare.com/durable-objects/platform/limits/), [DO Free pricing](https://developers.cloudflare.com/durable-objects/platform/pricing/), [Workers Wasm](https://developers.cloudflare.com/workers/runtime-apis/webassembly/), and [Emscripten device streams](https://emscripten.org/docs/api_reference/Filesystem-API.html#devices).

## Third-party codec artifact

`@ffmpeg/core@0.12.10` is pinned in the lockfile and identifies its license as GPL-2.0-or-later. The generated audio manifest records the original glue and Wasm hashes. The only glue adjustment supplies an inert script-directory fallback; the binary is unmodified and is instantiated from a precompiled Workers module. Its upstream source/build and component notices are in [ffmpeg.wasm](https://github.com/ffmpegwasm/ffmpeg.wasm) and the [official project overview](https://ffmpegwasm.netlify.app/docs/overview/). Retain those notices when packaging the codec artifact.
