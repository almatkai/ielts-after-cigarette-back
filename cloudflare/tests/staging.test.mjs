import { test, after } from "node:test";
import assert from "node:assert/strict";
import { createHash, randomBytes, randomUUID } from "node:crypto";
import { mkdir, writeFile, rm } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";
import { Miniflare, convertV4MiniflareOptions } from "miniflare";
import source from "../schema/postgres.json" with { type: "json" };
import { stagingDigest, STAGING_CHUNK_BYTES } from "../runtime/transfer.mjs";
import { seal } from "../tools/transfer.mjs";
const root = fileURLToPath(new URL("..", import.meta.url));
const entry = `
import { DurableObject } from 'cloudflare:workers';
import { importDatabase, stagingDigest } from './runtime/transfer.mjs';
import sqlite from './schema/sqlite.json';
export class Staging extends DurableObject {
  constructor(ctx,env) {
    super(ctx,env);
    ctx.blockConcurrencyWhile(async()=>ctx.storage.transactionSync(()=>{
      for(const s of sqlite.statements) ctx.storage.sql.exec(s);
      for(const seed of sqlite.seeds) for(const row of seed.rows) {
        const cols=Object.keys(row);
        ctx.storage.sql.exec('INSERT INTO '+seed.table+'('+cols.join(',')+') VALUES('+cols.map(()=>'?').join(',')+')',...Object.values(row));
      }
      ctx.storage.sql.exec('CREATE TABLE cf_coordination(key TEXT PRIMARY KEY,value TEXT NOT NULL,expires INTEGER)');
    }));
  }
  async fetch(request) {
    const fault=new URL(request.url).searchParams.get('fault');
    let writeStatements={},written=0,stagingWritten=0,publicationWritten=0, faulted=false,maxChunkBytes=0;
    const sql={exec:(statement,...args)=>{
      if(fault&&!faulted&&statement==='SELECT count(*) AS n FROM cf_import_chunks') {
        faulted=true;
        if(fault==='extra') this.ctx.storage.sql.exec("INSERT INTO cf_import_chunks VALUES(999999,'row','account_object_deletions',?)",new Uint8Array([123,125,10]));
        if(fault==='flip') {
          const row=this.ctx.storage.sql.exec('SELECT position,payload FROM cf_import_chunks ORDER BY position LIMIT 1').one(),bytes=new Uint8Array(row.payload);
          bytes[bytes.length-2]^=1;
          this.ctx.storage.sql.exec('UPDATE cf_import_chunks SET payload=? WHERE position=?',bytes,row.position);
        }
        if(fault==='move') this.ctx.storage.sql.exec('UPDATE cf_import_chunks SET position=-1 WHERE position=0');
      }
      const cursor=this.ctx.storage.sql.exec(statement,...args);
      written+=cursor.rowsWritten; if(cursor.rowsWritten) writeStatements[statement]=(writeStatements[statement]??0)+cursor.rowsWritten;
      if(statement.startsWith('INSERT INTO cf_import_chunks')) maxChunkBytes=Math.max(maxChunkBytes,args[3].byteLength);
      if(/^(INSERT INTO|DELETE FROM) cf_import_chunks/.test(statement)) stagingWritten+=cursor.rowsWritten;
      if(statement.startsWith('INSERT INTO "')) publicationWritten+=cursor.rowsWritten;
      return cursor;
    }};
    let report,error,publicationMs=0;
    const start=performance.now();
    try { report=await importDatabase({sql,transactionSync:f=>{const start=performance.now();try{return this.ctx.storage.transactionSync(f);}finally{publicationMs+=performance.now()-start;}}},request.body); }
    catch(e) { error=e.message; }
    const count=this.ctx.storage.sql.exec('SELECT count(*) n FROM account_object_deletions').one().n;
    const coordination=this.ctx.storage.sql.exec('SELECT count(*) n FROM cf_coordination').one().n;
    const chunks=this.ctx.storage.sql.exec('SELECT count(*) n FROM cf_import_chunks').one().n;
    const stageIndexes=this.ctx.storage.sql.exec('PRAGMA index_list(cf_import_chunks)').toArray();
    return Response.json({report,error,count,coordination,chunks,written,stagingWritten,publicationWritten,maxChunkBytes,stageIndexes,writeStatements,publicationMs,ms:performance.now()-start});
  }
}
export default {fetch(request,env){const key=new URL(request.url).pathname;return env.STATE.get(env.STATE.idFromName(key)).fetch(request);}};
`;
const bundled = await build({
  stdin: {
    contents: entry,
    resolveDir: root,
    sourcefile: "staging-test-entry.mjs",
  },
  bundle: true,
  format: "esm",
  platform: "browser",
  external: ["cloudflare:workers"],
  write: false,
});
const mf = new Miniflare(
  convertV4MiniflareOptions({
    workers: [
      {
        modules: [
          {
            type: "ESModule",
            path: "staging-test-entry.mjs",
            contents: bundled.outputFiles[0].text,
          },
        ],
        compatibilityDate: "2026-10-19",
        durableObjects: { STATE: { className: "Staging", useSQLite: true } },
      },
    ],
  }),
);
after(() => mf.dispose());
const hash = (b) => createHash("sha256").update(b).digest("hex"),
  zero = "0".repeat(64);
const chain = (previous, line) =>
  hash(
    Buffer.concat([
      Buffer.from(previous, "hex"),
      Buffer.from(hash(line), "hex"),
    ]),
  );
function fixture(count = 1800, large = false, invalid = false) {
  const rows = Array.from({ length: count }, (_, i) =>
    JSON.stringify({
      table: "account_object_deletions",
      values: [
        invalid && i === count - 1 ? "bad" : "blog",
        String(i).padStart(6, "0") +
          "-Контракт-" +
          (large ? "café😀".repeat(20000) : "storage"),
      ],
    }),
  );
  const redis = Array.from({ length: 400 }, (_, i) =>
    JSON.stringify({
      kind: "redis",
      key: "rate-limit:fixture:" + i,
      type: "string",
      value: "7",
      expires: null,
      dump: "AA==",
    }),
  );
  const tables = Object.fromEntries(
    source.tables.map((t) => [
      t.name,
      { columns: t.columns.map((c) => c.name), count: 0, chain: zero },
    ]),
  );
  for (const line of rows) {
    const state = tables.account_object_deletions;
    state.count++;
    state.chain = chain(state.chain, line);
  }
  const coordination = { count: redis.length, chain: zero };
  for (const line of redis)
    coordination.chain = chain(coordination.chain, line);
  const manifest = {
    format: "iac-cloudflare-dev-v1",
    revision: source.revision,
    tables,
    coordination,
    objects: [],
    ledger: [],
    sequences: [],
  };
  const header = JSON.stringify(manifest),
    payload = header + "\n" + [...rows, ...redis].join("\n") + "\n";
  return { manifest, header, payload, rows, redis };
}
async function send(path, payload, fault) {
  const result = await mf.dispatchFetch(
    "https://staging.test/" + path + (fault ? "?fault=" + fault : ""),
    { method: "POST", body: payload },
  );
  assert.equal(result.status, 200);
  return result.json();
}
test("synchronous staging SHA-256 matches standard vectors and native crypto at chunk boundaries", () => {
  for (const [input, expected] of [
    ["", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"],
    ["abc", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"],
  ])
    assert.equal(stagingDigest(input), expected);
  for (const n of [
    1,
    7,
    55,
    56,
    63,
    64,
    65,
    127,
    128,
    1023,
    4096,
    STAGING_CHUNK_BYTES - 1,
    STAGING_CHUNK_BYTES,
  ]) {
    const b = randomBytes(n);
    assert.equal(stagingDigest(b), hash(b));
  }
  assert.equal(
    stagingDigest("😀é漢字".repeat(1000)),
    hash("😀é漢字".repeat(1000)),
  );
});
test("native SQLite chunks reduce actual index+cleanup rowsWritten, retain all tables and coordination", async () => {
  const f = fixture(),
    out = await send("budget", f.payload);
  assert.equal(out.error, undefined);
  assert.equal(Object.keys(out.report.tables).length, 50);
  assert.equal(out.count, f.rows.length);
  assert.equal(out.coordination, f.redis.length);
  assert.equal(out.chunks, 0);
  assert.deepEqual(out.stageIndexes, []);
  assert.ok(out.maxChunkBytes <= STAGING_CHUNK_BYTES);
  assert.ok(out.stagingWritten < 20, JSON.stringify(out));
  assert.equal(out.publicationWritten, f.rows.length * 2);
  assert.ok(
    out.written < f.rows.length * 2 + f.redis.length * 2 + 50,
    JSON.stringify(out),
  );
  const replay = await send("budget", f.payload);
  assert.equal(replay.error, undefined);
  assert.equal(replay.written, 0);
  console.log(
    JSON.stringify({
      fixtureRows: f.rows.length,
      coordination: f.redis.length,
      totalWrites: out.written,
      stagingWrites: out.stagingWritten,
      publicationWrites: out.publicationWritten,
      ms: out.ms,
    }),
  );
});
test("partial stream is cleaned, publication is pristine, and same-manifest retry succeeds", async () => {
  const f = fixture(),
    out = await send(
      "partial",
      f.header + "\n" + f.rows.slice(0, 1700).join("\n") + "\n",
    );
  assert.match(out.error, /count or checksum mismatch/);
  assert.equal(out.count, 0);
  assert.equal(out.coordination, 0);
  assert.equal(out.chunks, 0);
  const retry = await send("partial", f.payload);
  assert.equal(retry.error, undefined);
  assert.equal(retry.count, 1800);
});
test("tampered stage bytes, extra chunks and positions reject atomically and permit clean retry", async () => {
  const f = fixture();
  for (const fault of ["flip", "extra", "move"]) {
    const out = await send("tamper-" + fault, f.payload, fault);
    assert.match(out.error, /Staged chunk/);
    assert.equal(out.count, 0);
    assert.equal(out.coordination, 0);
    assert.equal(out.chunks, 0);
    const retry = await send("tamper-" + fault, f.payload);
    assert.equal(retry.error, undefined);
    assert.equal(retry.count, 1800);
  }
});
test("SQL constraint failure rolls back all rows and seed deletes, rather than publishing a prefix", async () => {
  const f = fixture(40, false, true),
    out = await send("constraint", f.payload);
  assert.match(out.error, /CHECK constraint failed/);
  assert.equal(out.count, 0);
  assert.equal(out.coordination, 0);
  assert.equal(out.chunks, 0);
});
test("UTF-8 records spanning multiple 128KiB chunks remain exact and bounded", async () => {
  const f = fixture(2, true),
    out = await send("unicode", f.payload);
  assert.equal(out.error, undefined);
  assert.equal(out.count, 2);
  assert.ok(out.maxChunkBytes <= STAGING_CHUNK_BYTES);
  assert.ok(out.stagingWritten >= 8);
});
test("sealed estimate includes publication indexes, staging deletion, coordination and metadata", async () => {
  const f = fixture(),
    directory = root + "/.sensitive/staging-budget-" + randomUUID();
  await mkdir(directory, { recursive: true, mode: 0o700 });
  try {
    const manifest = { ...f.manifest };
    delete manifest.coordination;
    for (const [name, data] of [
      ["database.json", JSON.stringify(manifest)],
      ["rows.jsonl", f.rows.join("\n") + "\n"],
      ["objects.json", "[]"],
      ["redis.json", "[" + f.redis.join(",") + "]"],
    ])
      await writeFile(directory + "/" + name, data, { mode: 0o600 });
    const sealed = await seal(directory),
      out = await send("estimate", f.payload);
    assert.equal(
      sealed.budget.stagingRowsWrittenUpperBound,
      out.stagingWritten,
    );
    assert.ok(
      sealed.budget.totalRowsWrittenUpperBound >= out.written + 1,
      JSON.stringify({
        budget: sealed.budget,
        actual: out.written,
        statements: out.writeStatements,
      }),
    );
    assert.ok(sealed.budget.totalRowsWrittenUpperBound < 4500);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("30MiB staged input publishes with bounded chunk hashing and no awaited transaction", async () => {
  const f = fixture(180, true),
    bytes = Buffer.byteLength(f.payload);
  assert.ok(bytes > 30 * 1024 * 1024);
  const out = await send("large-budget", f.payload);
  assert.equal(out.error, undefined);
  assert.equal(out.count, 180);
  assert.equal(out.chunks, 0);
  assert.ok(out.maxChunkBytes <= STAGING_CHUNK_BYTES);
  assert.ok(out.publicationMs < 5000, JSON.stringify(out));
  console.log(
    JSON.stringify({
      inputBytes: bytes,
      publicationMs: out.publicationMs,
      totalMs: out.ms,
      stagingWrites: out.stagingWritten,
    }),
  );
});
test("missing tables, changed columns and coordination chains are rejected before publication", async () => {
  const f = fixture(2);
  for (const variant of ["table", "columns", "coordination"]) {
    const manifest = structuredClone(f.manifest);
    if (variant === "table") delete manifest.tables.users;
    if (variant === "columns") manifest.tables.users.columns.reverse();
    if (variant === "coordination") manifest.coordination.chain = zero;
    const out = await send(
      "manifest-" + variant,
      JSON.stringify(manifest) +
        "\n" +
        [...f.rows, ...f.redis].join("\n") +
        "\n",
    );
    assert.match(out.error, /manifest|checksum mismatch/);
    assert.equal(out.count, 0);
    assert.equal(out.coordination, 0);
    assert.equal(out.chunks, 0);
  }
});
