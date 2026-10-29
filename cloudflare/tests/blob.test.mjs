import { fileURLToPath } from "node:url";
process.chdir(fileURLToPath(new URL("..", import.meta.url)));
import { test, after } from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { Miniflare, convertV4MiniflareOptions } from "miniflare";
execFileSync(
  process.execPath,
  [
    "node_modules/wrangler/bin/wrangler.js",
    "deploy",
    "--dry-run",
    "--config",
    "wrangler.blob.jsonc",
    "--outdir",
    ".artifacts/blob-contract",
    "tests/blob-entry.mjs",
  ],
  { stdio: "pipe", env: { ...process.env, WRANGLER_SEND_METRICS: "false" } },
);
const mf = new Miniflare(
  convertV4MiniflareOptions({
    modules: [
      { type: "ESModule", path: ".artifacts/blob-contract/blob-entry.js" },
    ],
    modulesRoot: ".artifacts/blob-contract",
    compatibilityDate: "2026-10-19",
    durableObjects: {
      IAC_BLOB_INDEX: { className: "IACBlobIndex", useSQLite: true },
      IAC_BLOB_CHUNKS: { className: "IACBlobChunks", useSQLite: true },
    },
  }),
);
after(() => mf.dispose());
const call = (path, body) =>
  mf.dispatchFetch(
    "https://blob.test" + path,
    body === undefined
      ? {}
      : {
          method: "POST",
          body:
            typeof body === "string" || body instanceof Uint8Array
              ? body
              : JSON.stringify(body),
        },
  );
const hash = (b) => createHash("sha256").update(b).digest("hex");
test("SQLite blob streams bounded chunks, verifies checksum, ranges and immutable read versions", async () => {
  const bytes = Buffer.alloc(2 * 1024 * 1024 + 13);
  for (let i = 0; i < bytes.length; i++) bytes[i] = i % 251;
  let out = await call("/put?key=large&sha256=" + hash(bytes), bytes);
  const text = await out.text();
  assert.equal(out.status, 200, text);
  const uploaded = JSON.parse(text);
  assert.equal(uploaded.size, bytes.length);
  assert.equal(uploaded.key, "large");
  out = await call("/get?key=large");
  assert.deepEqual(Buffer.from(await out.arrayBuffer()), bytes);
  assert.equal(out.headers.get("x-checksum"), hash(bytes));
  out = await call("/get?key=large&offset=1048572&length=29");
  assert.deepEqual(
    Buffer.from(await out.arrayBuffer()),
    bytes.subarray(1048572, 1048601),
  );
  assert.equal(
    (await call("/put?key=large&sha256=" + "0".repeat(64), "corrupt")).status,
    409,
  );
  assert.equal(
    (await call("/head?key=large").then((r) => r.json())).size,
    bytes.length,
  );
  assert.equal(
    await call("/put?key=large&absent=1", "conflict").then((r) => r.json()),
    null,
  );
  const old = await call("/get?key=large");
  await call("/put?key=large", "replacement");
  assert.deepEqual(Buffer.from(await old.arrayBuffer()), bytes);
  await call("/delete?key=large");
  assert.equal((await call("/get?key=large")).status, 404);
});
test("SQLite multipart retries, order, abort and atomic replacement survive facade recreation", async () => {
  const { uploadId } = await call("/start?key=multipart", {}).then((r) =>
    r.json(),
  );
  const second = await call("/part?key=multipart", {
    uploadId,
    partNumber: 2,
    body: "world",
  }).then((r) => r.json());
  await call("/part?key=multipart", { uploadId, partNumber: 1, body: "bad" });
  const first = await call("/part?key=multipart", {
    uploadId,
    partNumber: 1,
    body: "hello ",
  }).then((r) => r.json());
  assert.equal((await call("/get?key=multipart")).status, 404);
  assert.equal(
    (
      await call("/complete?key=multipart", {
        uploadId,
        parts: [second, first],
      })
    ).status,
    409,
  );
  const done = await call("/complete?key=multipart", {
    uploadId,
    parts: [first, second],
  });
  assert.equal(done.status, 200, await done.clone().text());
  assert.equal(
    await call("/get?key=multipart").then((r) => r.text()),
    "hello world",
  );
  await call("/abort?key=multipart", { uploadId });
  assert.equal(
    await call("/get?key=multipart").then((r) => r.text()),
    "hello world",
  );
  assert.equal(
    (await call("/head?key=multipart").then((r) => r.json())).etag,
    hash("hello world"),
  );
  const aborted = await call("/start?key=multipart", {}).then((r) => r.json());
  await call("/part?key=multipart", {
    ...aborted,
    partNumber: 1,
    body: "other",
  });
  await call("/abort?key=multipart", aborted);
  assert.equal(
    (await call("/complete?key=multipart", { ...aborted, parts: [first] }))
      .status,
    409,
  );
  assert.equal(
    await call("/get?key=multipart").then((r) => r.text()),
    "hello world",
  );
});
test("SQLite listing has stable prefix pagination and rejects unsupported contracts", async () => {
  for (const key of ["prefix/a", "prefix/b", "prefix/c", "other"])
    await call("/put?key=" + key, key);
  const first = await call("/list?prefix=prefix/&limit=2").then((r) =>
    r.json(),
  );
  assert.deepEqual(
    first.objects.map((x) => x.key),
    ["prefix/a", "prefix/b"],
  );
  assert.equal(first.truncated, true);
  const next = await call(
    "/list?prefix=prefix/&limit=2&cursor=" + encodeURIComponent(first.cursor),
  ).then((r) => r.json());
  assert.deepEqual(
    next.objects.map((x) => x.key),
    ["prefix/c"],
  );
  assert.equal(next.truncated, false);
  assert.equal((await call("/list?delimiter=/")).status, 409);
});

test("aborted, replaced and checksum-rejected writes are collected without deleting live objects", async () => {
  const before = await call("/debug").then((r) => r.json());
  const live = await call("/get?key=multipart").then((r) => r.text());
  const after = await call("/debug?sweep=1").then((r) => r.json());
  assert.ok(after.bytes < before.bytes);
  assert.equal(after.objects, before.objects);
  assert.equal(await call("/get?key=multipart").then((r) => r.text()), live);
  const again = await call("/debug?sweep=1").then((r) => r.json());
  assert.equal(again.bytes, after.bytes);
  assert.equal(after.expiring, 0);
});
test("Unicode object names and prefix cursors roundtrip without Latin-1 truncation", async () => {
  for (const key of ["қазақ/а", "қазақ/ә"])
    await call("/put?key=" + encodeURIComponent(key), key);
  const first = await call(
    "/list?prefix=" + encodeURIComponent("қазақ/") + "&limit=1",
  ).then((r) => r.json());
  assert.equal(first.objects.length, 1);
  assert.equal(first.truncated, true);
  const second = await call(
    "/list?prefix=" +
      encodeURIComponent("қазақ/") +
      "&limit=1&cursor=" +
      encodeURIComponent(first.cursor),
  ).then((r) => r.json());
  assert.equal(second.objects.length, 1);
  assert.notEqual(second.objects[0].key, first.objects[0].key);
});

test("a chunk written after catalog cleanup is recovered by its shard alarm", async () => {
  const response = await call("/orphan");
  assert.equal(response.status, 200, await response.clone().text());
  const stats = await response.json();
  assert.equal(stats.before, stats.after);
});

test("interrupted input streams never publish partial bytes", async () => {
  await call("/put?key=interrupted", "original");
  const failed = await call("/failed-stream?key=interrupted");
  assert.equal(failed.status, 409);
  await failed.text();
  assert.equal(
    await call("/get?key=interrupted").then((r) => r.text()),
    "original",
  );
  await call("/debug?sweep=1");
  assert.equal(
    await call("/get?key=interrupted").then((r) => r.text()),
    "original",
  );
});

test("older 256 KiB chunk metadata remains readable after the 1 MiB upgrade", async () => {
  const response = await call("/legacy");
  assert.equal(response.status, 200, await response.clone().text());
  const meta = await response.json();
  const bytes = Buffer.from(
    await call("/get?key=" + meta.key).then((r) => r.arrayBuffer()),
  );
  assert.equal(bytes.length, meta.size);
  assert.equal(hash(bytes), meta.sha256);
  const range = await call("/get?key=" + meta.key + "&offset=262140&length=29");
  assert.deepEqual(
    Buffer.from(await range.arrayBuffer()),
    bytes.subarray(262140, 262169),
  );
});
