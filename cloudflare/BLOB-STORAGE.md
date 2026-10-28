# Free private object storage

The default deployment uses **SQLite Durable Objects, without R2, a subscription checkout, paid Workers, Containers, external object storage or a VPS origin**. It remains subject to the account's shared Free allowances; reaching a Free quota fails an operation rather than enabling billing. Low traffic does not remove CPU, memory or quota limits.

`runtime/blob-store.mjs` exposes the application's `MEDIA` object interface. `runtime/blob-worker.mjs` owns private `IACBlobIndex` and `IACBlobChunks` classes. The deployment entrypoint always returns HTTP 404, and `workers_dev` is disabled. All reads/writes require explicit DO namespace bindings. Test fixture HTTP routes are separate files and are never deployment entrypoints.

## Durability and bounds

- A catalog row atomically switches the visible object version only after every chunk has been stored durably and the streamed SHA-256 has been verified. Failed checksums, interrupted streams and failed preconditions never replace the old visible object. Copying uses the original immutable stream and atomically publishes a new version.
- Each blob row contains at most **1 MiB**. One file is limited to **512 MiB**; multipart metadata is bounded to **2,048 parts / 512 KiB**. Per-file chunks distribute across 64 deterministic shard objects. Each shard rejects new bytes beyond **64 MiB** of live/retired chunk data, keeping its database far below the platform's 1 GiB Free per-object limit. Catalog creation/mutations reject a 64 MiB allocated catalog and have explicit 10,000-object/session caps. Application storage remains a separate DO.
- Shards have atomic byte accounting. Published versions are immutable. Replacing/deleting an object changes visibility immediately but retains its old chunks for **24 hours**, so an already-open stream can finish. Reads lasting longer than that grace period are unsupported. A very long or continuously replaced dataset can hit the capacity guard before its previous versions expire.
- Multipart parts are individually staged and checksummed. Retried part numbers replace only unpublished metadata. Completion verifies exact ordered part receipts, streams the complete checksum, rechecks the selected immutable part identities, then atomically publishes. An abort after successful completion cannot delete published parts.
- Catalog alarms collect expired staging and retired versions. Shard alarms independently recover a late orphan chunk left by cancellation, reset or a writer racing catalog cleanup. Alarms schedule the actual next deadline; they do not poll once a minute. A live shard performs one lease check after the initial one-day staging deadline, then sleeps until a later explicit operation.
- `_cf-temp/` published objects expire after one day. Their retired bytes receive the same one-day read grace period. Original imported objects do not expire automatically. Capacity accounting includes temporary, staged and retained bytes.
- Large `put()` calls transfer their readable stream into the catalog DO and execute hashing/chunk loops there. The temporary migration helper therefore does not perform a large-file JavaScript loop in the Free ingress Worker's 10 ms CPU budget. Application/audio multipart operations already execute inside their own DOs.

The implementation intentionally supports the actual contract rather than silently emulating all R2 behavior: `put` (SHA-256, HTTP/custom metadata and supported object conditions), `get` (offset/length or suffix ranges and supported conditions), `head`, `delete`, prefix/cursor `list`, multipart create/resume/upload/complete/abort. Unsupported options fail explicitly. ETags are the complete object's SHA-256 rather than S3 multipart MD5 ETags. The application consumes sizes, bytes and checksums, not R2-specific ETag construction. Existing explicit R2-bound environments can still use the original interface when blob bindings are absent; the supplied default configs contain no R2 binding.

## Deployment order

From `cloudflare/`, using the existing authorized account credentials:

```sh
npx wrangler deploy --config wrangler.blob.jsonc
npx wrangler deploy --config wrangler.audio.jsonc
npx wrangler deploy --config wrangler.jsonc
```

Both API and audio configs bind `IAC_BLOB_INDEX` / `IAC_BLOB_CHUNKS` to `ielts-blob-dev`; the private blob Worker binds both classes locally. The separately authenticated migration helper uses the same cross-script bindings. No API traffic should be connected until import/activation and actual platform limits pass. These commands have **not** been run by this implementation task.

A private API binding, rather than a public blob upload endpoint, carries application object traffic. The migration helper remains authenticated using the existing migration procedure, is removed after cutover, and validates checksums before activation. No real data has been copied by this task.

## Free allowance accounting

The account has a shared **5 GB** Free SQLite storage allowance, **100,000 daily rows written**, **5 million daily rows read**, and **100,000 daily DO requests** (plus compute allowance). Those limits are shared with Drone and other applications. The per-shard capacity guards are local bounds, not a reservation of account quota: root must total actual application databases, both projects' objects, staging/retired objects and other account usage before cutover.

Each part records its chunk size, preserving reads of older 256 KiB staging fixtures after the upgrade. The Free internal subrequest cap is 1,000 per invocation: a 512 MiB full object read uses at most 512 chunk calls with this layout. The account HTTP request body limit still applies to a single upload, so an object above that limit requires multipart transfer.

A blob chunk upload writes its blob row, a byte-account row, and (once per upload/shard) a lease row; indexes and catalog updates also consume writes. A 278 MB source inventory therefore needs roughly one thousand blob writes in addition to relational import, not zero writes. Reads and GC consume DO calls/rows too. Do not reuse the previous R2-based daily budget estimate without adding these costs and the actual relational staging format. Importing on separate UTC days is valid if the shared write budget requires it. Free quotas and hard per-request limits still require native measurement.

## Validation

`npm test` exercises the entire actual Go/Wasm API, private audio codec and transfer/activation suite with these native blob DOs and **zero R2 buckets**. This includes all 165 source routes, a 50 MiB multipart upload/range/download, full 800-second audio decoding to a 25.6 MB waveform, import checksum/metadata mismatch rejection and activation. Dedicated workerd tests cover chunk boundaries, SHA mismatch rollback, failed conditional replacement, immutable reads across replacement, multipart ordering/retries/abort-after-completion, GC idempotence, orphan recovery and Unicode pagination. Synthetic fixtures only; real data and deployed CPU/memory remain root-owned cutover gates.

Primary API/limit references: [SQLite storage API](https://developers.cloudflare.com/durable-objects/api/sqlite-storage-api/), [DO limits](https://developers.cloudflare.com/durable-objects/platform/limits/), [Free pricing/quotas](https://developers.cloudflare.com/durable-objects/platform/pricing/), [native DigestStream](https://developers.cloudflare.com/workers/runtime-apis/web-crypto/).
