# IELTS DEV native cutover — 2026-10-28

**IELTS DEV runtime migration and source retirement are complete; current API availability is quota limited.** https://ielts.woolet.cc/app/ serves the selected DEV application entirely through Cloudflare. The existing public hostname, Google/Turnstile origin authority and host-only secure cookie policy are retained.

As checked at 2026-10-28 09:46 UTC, the guest API returns **503** because the
shared Cloudflare Free SQLite read allowance is exhausted. The frontdoor sends
`Cache-Control: no-store`, `Retry-After` and a Russian message pointing to the
reset at **2026-10-29 00:00 UTC (05:00 Almaty)**. The earlier HTML request returned
200; healthy paths are not forcibly disabled. Recovery after reset remains
unverified. No paid upgrade or VPS fallback was introduced. The
[quota boundary receipt](../../ielts-after-cigarette/iac-web/cloudflare/quota-boundary-acceptance.json)
records the narrow adapter update, current versions and preserved bindings/vars/secrets.

The chain is `ielts-dev` service-binding frontdoor → `ielts-web-dev` frontend/SSR DO → private `ielts-api-dev` service → application SQLite DO, private SQLite blob DOs and private audio DO. There is no VPC/VPS/database/Redis/MinIO fallback in this chain. Production application/deployment paths were unchanged.

## Data and source boundary

- Backend source `40ed0dc5fca0b9df989a29d421a81476e7df0f50`; frontend source `b05d0622b7bfcc44dcddad3544c6e6d565041019`.
- All source DEV ingress, web, API, worker and tunnel containers were stopped before the final repeatable-read export.
- Verified import `b3ad7c1d96f0e2d14aa89638228a5a3681ca59f09caea1aabf5e0952e2ae9d33`: **50 tables, 15,540 rows, 52 objects / 278,008,907 bytes, 68 coordination keys**.
- Every table count/hash chain and complete object checksum inventory matched before activation. The final active import report exactly matches the retained original receipt. The conservative database publication/staging/coordination bound is 57,496 writes, excluding private blob writes and other account use.
- Existing selected DEV secrets were reused through private files. VPS connection/storage credentials and telemetry endpoints were not copied into the runtime. No provider key or migration token was created.
- Source PostgreSQL, Redis and MinIO plus two legacy DEV databases are now stopped: **thirteen stopped IELTS DEV/legacy containers total**, with Docker restart policy **`no` on all thirteen**. This includes eight `ielts-guest-dev` containers, legacy `ielts-api-development` PostgreSQL/Redis/backend containers, `ielts-landing-dev-landing-1` and `ielts-web-dev-web-1`. The three additional inactive legacy backend/web/landing artifacts were retired in place; they were not separately migrated as applications. Their volumes, private export/receipts and original restart policies are retained. Existing CI deploys production only; its DEV branch runs tests.

The final read-only server inventory at **2026-10-28 09:51:06 UTC** verified all
thirteen IELTS containers stopped with restart disabled. The original private
ten-container policy receipt remains unchanged (SHA-256
`ede65d31fdfe4569708009509ea518e083781b81b4d6a39c6cd4658bb1914101`);
separate private receipts retain the three additional legacy policies. Their
checksums are in the machine-readable retirement record. Source volumes remain
retained for a reconciled rollback.

The temporary private migration Worker has been deleted and the loopback OAuth bridge stopped. The public API has no migration endpoint. Private exports live under ignored mode-700 `.sensitive/` directories with mode-600 files; they are never committed.

## Acceptance and cost boundaries

The [frontend receipt](../../ielts-after-cigarette/iac-web/cloudflare/public-acceptance.json) records 82 native requests: SSR, exact asset/font bytes, catalog APIs, guest configuration and negative authentication/cookie checks. Its immutable source artifact has 201 verified SSR references; module startup was 11 ms. Canonical aliases preserve path/query.

The [audio receipt](AUDIO-PLATFORM-ACCEPTANCE.md) verifies an imported educational file and a full 180-second browser Opus recording on actual Cloudflare: continuous PCM16 mono 16 kHz output, exact byte count/duration and complete SHA-256. Audio CPU was 40 ms / 1,149 ms and Wasm linear memory 32 MiB; native outcomes were `ok`. Derived test objects were removed and originals preserved. Exact total isolate peak memory was not measured.

No paid plan, R2 subscription, Container, external database or storage service was enabled. The design uses Free-eligible SQLite Durable Objects and Workers; shared Free request, read/write, storage, compute and Images quotas remain finite. Billing/subscription APIs returned insufficient permissions, so the account's pre-existing subscription is not independently certified by this migration. External AI, STT, Google and SMS provider transactions were not invoked for acceptance. Existing provider settings remain in force; Cloudflare's free allowances do not change those providers' terms.

Google interactive login, Turnstile browser completion and browser hydration were not manually exercised. Local workerd contract checks cover all 165 API registrations; native acceptance covers the listed cases and does not claim exhaustive live-user coverage.

Machine-readable deployment/source/import metadata: [schema/native-cutover.json](schema/native-cutover.json).

## Rollback

The old frontdoor Worker version is `6a79b7bd-8c0a-4964-872c-3857337ef784`; its source/settings and the source container boundary are retained privately. The frontdoor contains no DO migration, so its code can be restored independently of application storage.

**The native dataset is active and may contain new writes.** Before rollback, fence target writers and reconcile/export those changes back into the retained source. Restore the retained original restart policies only as part of that reconciled rollback. Merely restarting old containers and routing traffic to their stale data would discard new activity. Preserve both datasets and receipts until reconciliation is complete. Do not delete source volumes or activate another old snapshot over the live object.
