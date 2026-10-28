// RPC-only migration methods. The public API Worker exposes none of these routes.
import schema from "../schema/postgres.json" with { type: "json" };
import sqlite from "../schema/sqlite.json" with { type: "json" };
import { importHLL, countHLL } from "./hll.mjs";
const encoder = new TextEncoder(),
  zero = "0".repeat(64);
const hex = (b) =>
  Array.from(new Uint8Array(b), (x) => x.toString(16).padStart(2, "0")).join(
    "",
  );
export const digest = async (bytes) =>
  hex(
    await crypto.subtle.digest(
      "SHA-256",
      typeof bytes === "string" ? encoder.encode(bytes) : bytes,
    ),
  );
// One durable staging row per bounded byte chunk, never per application row.
// SQLite's INTEGER PRIMARY KEY aliases rowid, avoiding a second index write.
export const STAGING_CHUNK_BYTES = 128 * 1024;
export const MAX_TRANSFER_RECORD_BYTES = 4 * 1024 * 1024;
// SHA-256's fixed compression function is used only to recheck verified chunks
// inside transactionSync, where WebCrypto's asynchronous digest cannot be awaited.
// Input-chain verification continues to use native WebCrypto.
const SHA256_K = Uint32Array.from([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1,
  0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
  0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786,
  0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147,
  0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
  0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b,
  0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a,
  0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
  0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
]);
const rotate = (n, bits) => (n >>> bits) | (n << (32 - bits));
export function stagingDigest(input) {
  const bytes =
      typeof input === "string" ? encoder.encode(input) : new Uint8Array(input),
    data = new Uint8Array(Math.ceil((bytes.length + 9) / 64) * 64),
    view = new DataView(data.buffer),
    words = new Uint32Array(64),
    state = Uint32Array.from([
      0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c,
      0x1f83d9ab, 0x5be0cd19,
    ]);
  data.set(bytes);
  data[bytes.length] = 0x80;
  view.setUint32(data.length - 8, Math.floor(bytes.length / 0x20000000));
  view.setUint32(data.length - 4, bytes.length * 8);
  for (let offset = 0; offset < data.length; offset += 64) {
    for (let i = 0; i < 16; i++) words[i] = view.getUint32(offset + i * 4);
    for (let i = 16; i < 64; i++) {
      const a = words[i - 15],
        b = words[i - 2];
      words[i] =
        words[i - 16] +
        (rotate(a, 7) ^ rotate(a, 18) ^ (a >>> 3)) +
        words[i - 7] +
        (rotate(b, 17) ^ rotate(b, 19) ^ (b >>> 10));
    }
    let [a, b, c, d, e, f, g, h] = state;
    for (let i = 0; i < 64; i++) {
      const t1 =
          (h +
            (rotate(e, 6) ^ rotate(e, 11) ^ rotate(e, 25)) +
            ((e & f) ^ (~e & g)) +
            SHA256_K[i] +
            words[i]) |
          0,
        t2 =
          ((rotate(a, 2) ^ rotate(a, 13) ^ rotate(a, 22)) +
            ((a & b) ^ (a & c) ^ (b & c))) |
          0;
      h = g;
      g = f;
      f = e;
      e = (d + t1) | 0;
      d = c;
      c = b;
      b = a;
      a = (t1 + t2) | 0;
    }
    [a, b, c, d, e, f, g, h].forEach((n, i) => {
      state[i] += n;
    });
  }
  return Array.from(state, (n) => n.toString(16).padStart(8, "0")).join("");
}
const unhex = (s) =>
  Uint8Array.from(s.match(/../g) ?? [], (x) => parseInt(x, 16));
async function chain(previous, line) {
  const bytes = new Uint8Array(64);
  bytes.set(unhex(previous));
  bytes.set(unhex(await digest(line)), 32);
  return digest(bytes);
}
export async function* lines(body) {
  let pending = "";
  const reader = body.pipeThrough(new TextDecoderStream()).getReader();
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    pending += value;
    let end;
    while ((end = pending.indexOf("\n")) >= 0) {
      const line = pending.slice(0, end);
      pending = pending.slice(end + 1);
      if (line.length > 4 << 20) throw Error("Transfer record exceeds limit");
      if (line) yield line;
    }
    if (pending.length > 4 << 20) throw Error("Transfer record exceeds limit");
  }
  if (pending) yield pending;
}
export function importValue(column, input) {
  if (input === null) return null;
  if (column.type === "bytea") {
    if (typeof input?.$bytes !== "string") throw Error("Invalid bytea");
    return Uint8Array.from(atob(input.$bytes), (c) => c.charCodeAt(0)).buffer;
  }
  if (column.type === "boolean") {
    if (typeof input !== "boolean") throw Error("Invalid boolean");
    return Number(input);
  }
  if (
    column.type === "uuid" &&
    !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(
      input,
    )
  )
    throw Error("Invalid UUID");
  if (column.type === "bigint") {
    if (!/^-?\d+$/.test(input) || !Number.isSafeInteger(Number(input)))
      throw Error("Bigint exceeds lossless runtime range");
    return Number(input);
  }
  if (column.type === "double precision") {
    const bytes = unhex(input);
    if (bytes.length !== 8) throw Error("Invalid float64");
    const number = new DataView(bytes.buffer).getFloat64(0);
    if (!Number.isFinite(number))
      throw Error("Non-finite float is outside the native data contract");
    return number;
  }
  if (column.type === "jsonb" || column.type.endsWith("[]")) JSON.parse(input);
  return input;
}
function redisValue(record) {
  if (record.key.startsWith("iac:grading:v1:"))
    return { disposition: "derived-cache-archived" };
  if (
    record.type === "list" &&
    /^jobs:(writing:assessment|speaking:transcription|speaking:assessment)$/.test(
      record.key,
    )
  )
    return { disposition: "durable-sql-jobs-recovered" };
  if (record.type === "string" && record.value?.hll_raw) {
    const value = importHLL(record.value.hll_raw);
    if (countHLL([value]) !== record.value.count)
      throw Error("HLL cardinality mismatch");
    return { disposition: "restored", value };
  }
  if (
    !["string", "hash", "zset"].includes(record.type) ||
    !/^(?:iac:analytics:|iac:daily-limits:|daily-limit:|rate-limit:|rate:|guest:|rl:)/.test(
      record.key,
    )
  )
    throw Error("Unmapped coordination key; explicit coverage required");
  return { disposition: "restored", value: record.value };
}
export function coordinationRecord(record) {
  return { key: record.key, expires: record.expires, ...redisValue(record) };
}
function setup(storage) {
  storage.sql.exec(
    "CREATE TABLE IF NOT EXISTS cf_imports(id TEXT PRIMARY KEY,status TEXT NOT NULL,manifest TEXT NOT NULL,report TEXT NOT NULL)",
  );
  storage.sql.exec(
    "CREATE TABLE IF NOT EXISTS cf_source_ledger(kind TEXT NOT NULL,value TEXT NOT NULL)",
  );
}
export function migrationStatus(storage) {
  setup(storage);
  return (
    storage.sql
      .exec(
        "SELECT id,status,report FROM cf_imports ORDER BY rowid DESC LIMIT 1",
      )
      .toArray()[0] ?? null
  );
}
export async function importDatabase(storage, body) {
  setup(storage);
  storage.sql.exec(
    "CREATE TABLE IF NOT EXISTS cf_import_chunks(position INTEGER PRIMARY KEY,kind TEXT NOT NULL,table_name TEXT NOT NULL,payload BLOB NOT NULL)",
  );
  const iterator = lines(body)[Symbol.asyncIterator](),
    first = await iterator.next();
  if (first.done) throw Error("Missing manifest");
  const manifest = JSON.parse(first.value),
    id = await digest(first.value),
    existing = migrationStatus(storage);
  if (existing) {
    if (existing.id !== id)
      throw Error("Target already contains a different import");
    if (["complete", "active"].includes(existing.status)) {
      while (!(await iterator.next()).done) {}
      return JSON.parse(existing.report);
    }
  }
  if (
    manifest.format !== "iac-cloudflare-dev-v1" ||
    manifest.revision !== schema.revision
  )
    throw Error("Wrong selected DEV snapshot");
  if (
    !manifest.tables ||
    JSON.stringify(Object.keys(manifest.tables).sort()) !==
      JSON.stringify(schema.tables.map((t) => t.name).sort())
  )
    throw Error("Incomplete table manifest");
  if (
    !manifest.coordination ||
    !Number.isSafeInteger(manifest.coordination.count) ||
    manifest.coordination.count < 0 ||
    !/^[0-9a-f]{64}$/.test(manifest.coordination.chain)
  )
    throw Error("Invalid coordination manifest");
  const map = new Map(schema.tables.map((t) => [t.name, t]));
  function pristine() {
    for (const table of schema.tables) {
      const seed = sqlite.seeds.find((s) => s.table === table.name),
        count = storage.sql
          .exec(`SELECT count(*) AS n FROM "${table.name}"`)
          .one().n;
      if (count !== (seed?.rows.length ?? 0))
        throw Error("Target is not a pristine migration target");
      if (
        count &&
        storage.sql
          .exec(`SELECT * FROM "${table.name}"`)
          .toArray()
          .some(
            (row) =>
              !seed.rows.some((expected) =>
                Object.entries(expected).every(([k, v]) => row[k] === v),
              ),
          )
      )
        throw Error("Target is not a pristine migration target");
    }
    for (const name of ["cf_coordination", "cf_source_ledger"])
      if (storage.sql.exec(`SELECT count(*) AS n FROM ${name}`).one().n)
        throw Error("Target is not a pristine migration target");
  }
  for (const table of schema.tables) {
    const meta = manifest.tables[table.name];
    if (
      !meta ||
      JSON.stringify(meta.columns) !==
        JSON.stringify(table.columns.map((c) => c.name)) ||
      !Number.isSafeInteger(meta.count) ||
      meta.count < 0 ||
      !/^[0-9a-f]{64}$/.test(meta.chain)
    )
      throw Error("Invalid table manifest");
  }
  pristine();
  const report = {
    id,
    tables: {},
    coordination: {
      restored: 0,
      "derived-cache-archived": 0,
      "durable-sql-jobs-recovered": 0,
    },
    objects: manifest.objects?.length ?? 0,
    mediaCoverage: manifest.mediaCoverage ?? null,
  };
  for (const table of schema.tables)
    report.tables[table.name] = { count: 0, chain: zero };
  // Retried failed streams start afresh; no unverified previous bytes participate.
  storage.sql.exec("DELETE FROM cf_import_chunks");
  storage.sql.exec(
    "INSERT INTO cf_imports VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status",
    id,
    "receiving",
    first.value,
    JSON.stringify(report),
  );
  let redisCount = 0,
    redisChain = zero,
    buffer = new Uint8Array(STAGING_CHUNK_BYTES),
    used = 0,
    group = null;
  // Only chunk metadata/hashes are retained; application data remains bounded.
  const chunks = [];
  async function flush() {
    if (!used) return;
    const payload = buffer.slice(0, used),
      position = chunks.length,
      checksum = await digest(payload);
    storage.sql.exec(
      "INSERT INTO cf_import_chunks VALUES(?,?,?,?)",
      position,
      group.kind,
      group.table,
      payload,
    );
    chunks.push({ position, ...group, checksum });
    used = 0;
  }
  async function stage(kind, table, text) {
    if (group && (group.kind !== kind || group.table !== table)) await flush();
    group = { kind, table };
    const bytes = encoder.encode(text + "\n");
    if (bytes.length > MAX_TRANSFER_RECORD_BYTES)
      throw Error("Transfer record exceeds limit");
    for (let offset = 0; offset < bytes.length;) {
      const count = Math.min(buffer.length - used, bytes.length - offset);
      buffer.set(bytes.subarray(offset, offset + count), used);
      used += count;
      offset += count;
      if (used === buffer.length) await flush();
    }
  }
  function* staged(kind, table) {
    const decoder = new TextDecoder("utf-8", { fatal: true });
    let pending = "",
      count = 0;
    const expected = chunks.filter((c) => c.kind === kind && c.table === table);
    for (const row of storage.sql.exec(
      "SELECT position,kind,table_name,payload FROM cf_import_chunks WHERE kind=? AND table_name=? ORDER BY position",
      kind,
      table,
    )) {
      const chunk = expected[count++];
      if (
        !chunk ||
        row.payload.byteLength > STAGING_CHUNK_BYTES ||
        row.position !== chunk.position ||
        row.kind !== chunk.kind ||
        row.table_name !== chunk.table ||
        stagingDigest(row.payload) !== chunk.checksum
      )
        throw Error("Staged chunk checksum mismatch");
      pending += decoder.decode(row.payload, { stream: true });
      let end;
      while ((end = pending.indexOf("\n")) >= 0) {
        const line = pending.slice(0, end);
        pending = pending.slice(end + 1);
        if (!line) throw Error("Invalid empty staged record");
        yield JSON.parse(line);
      }
      if (pending.length > MAX_TRANSFER_RECORD_BYTES)
        throw Error("Staged record exceeds limit");
    }
    pending += decoder.decode();
    if (count !== expected.length || pending)
      throw Error("Incomplete staged chunks");
  }
  try {
    // Never await input inside transactionSync: the input gate would deadlock.
    // Validate the manifest chains while receiving, stage raw bounded chunks,
    // then recheck every chunk against its native digest while publishing.
    for (;;) {
      const item = await iterator.next();
      if (item.done) break;
      const record = JSON.parse(item.value);
      if (record.kind === "redis") {
        redisChain = await chain(redisChain, item.value);
        const native = coordinationRecord(record);
        report.coordination[native.disposition]++;
        if (++redisCount > manifest.coordination.count)
          throw Error("Coordination count mismatch");
        await stage("redis", "", JSON.stringify(native));
        continue;
      }
      const table = map.get(record.table);
      if (
        (record.kind !== undefined && record.kind !== "row") ||
        !table ||
        !Array.isArray(record.values) ||
        record.values.length !== table.columns.length
      )
        throw Error("Unknown table or column");
      record.values.forEach((v, i) => importValue(table.columns[i], v));
      const state = report.tables[table.name];
      state.chain = await chain(state.chain, item.value);
      if (++state.count > manifest.tables[table.name].count)
        throw Error("Table count mismatch");
      await stage("row", table.name, item.value);
    }
    await flush();
    buffer = null;
    for (const [table, actual] of Object.entries(report.tables)) {
      const expected = manifest.tables[table];
      if (actual.count !== expected.count || actual.chain !== expected.chain)
        throw Error("Table count or checksum mismatch");
    }
    if (
      redisCount !== manifest.coordination.count ||
      redisChain !== manifest.coordination.chain
    )
      throw Error("Coordination count or checksum mismatch");
    storage.transactionSync(() => {
      pristine();
      if (
        storage.sql.exec("SELECT count(*) AS n FROM cf_import_chunks").one()
          .n !== chunks.length
      )
        throw Error("Staged chunk count mismatch");
      storage.sql.exec("PRAGMA defer_foreign_keys=ON");
      for (const table of schema.tables)
        storage.sql.exec(`DELETE FROM "${table.name}"`);
      for (const table of schema.tables) {
        let count = 0;
        for (const record of staged("row", table.name)) {
          if (
            (record.kind !== undefined && record.kind !== "row") ||
            record.table !== table.name ||
            !Array.isArray(record.values) ||
            record.values.length !== table.columns.length
          )
            throw Error("Invalid staged row");
          const values = record.values.map((v, i) =>
            importValue(table.columns[i], v),
          );
          storage.sql.exec(
            `INSERT INTO "${table.name}"(${table.columns.map((c) => '"' + c.name + '"').join(",")}) VALUES(${values.map(() => "?").join(",")})`,
            ...values,
          );
          count++;
        }
        if (count !== report.tables[table.name].count)
          throw Error("Staged row count mismatch");
      }
      let coordinationCount = 0;
      for (const record of staged("redis", "")) {
        coordinationCount++;
        if (
          record.disposition === "restored" &&
          (record.expires === null || record.expires > Date.now())
        )
          storage.sql.exec(
            "INSERT INTO cf_coordination(key,value,expires) VALUES(?,?,?)",
            record.key,
            JSON.stringify(record.value),
            record.expires,
          );
      }
      if (coordinationCount !== redisCount)
        throw Error("Staged coordination count mismatch");
      if (storage.sql.exec("PRAGMA foreign_key_check").toArray().length)
        throw Error("Foreign-key verification failed");
      for (const row of manifest.ledger ?? [])
        storage.sql.exec(
          "INSERT INTO cf_source_ledger VALUES(?,?)",
          "postgres_migration",
          JSON.stringify(row),
        );
      for (const row of manifest.sequences ?? []) {
        storage.sql.exec(
          "INSERT INTO cf_source_ledger VALUES(?,?)",
          "postgres_sequence",
          JSON.stringify(row),
        );
        if (
          row.name === "study_learning_content_reviews_id_seq" &&
          row.value !== null
        ) {
          const n = Number(row.value);
          if (!Number.isSafeInteger(n))
            throw Error("Sequence exceeds lossless range");
          storage.sql.exec(
            "INSERT INTO sqlite_sequence(name,seq) SELECT 'study_learning_content_reviews',? WHERE NOT EXISTS(SELECT 1 FROM sqlite_sequence WHERE name='study_learning_content_reviews')",
            n,
          );
          storage.sql.exec(
            "UPDATE sqlite_sequence SET seq=MAX(seq,?) WHERE name='study_learning_content_reviews'",
            n,
          );
        }
      }
      storage.sql.exec("DELETE FROM cf_import_chunks");
      storage.sql.exec(
        "UPDATE cf_imports SET status=?,report=? WHERE id=?",
        "complete",
        JSON.stringify(report),
        id,
      );
    });
  } catch (e) {
    // Do not retain private duplicate data or charge future reads for failed chunks.
    storage.sql.exec("DELETE FROM cf_import_chunks");
    storage.sql.exec("UPDATE cf_imports SET status='failed' WHERE id=?", id);
    throw e;
  }
  return report;
}
export async function activate(storage, media, id) {
  const state = migrationStatus(storage);
  if (
    !state ||
    state.id !== id ||
    !["complete", "active"].includes(state.status)
  )
    throw Error("Import is not complete");
  if (state.status === "active") return JSON.parse(state.report);
  const manifest = JSON.parse(
    storage.sql.exec("SELECT manifest FROM cf_imports WHERE id=?", id).one()
      .manifest,
  );
  // Enumerate exact target keys too: a checksum match cannot conceal missing or extra objects.
  const actual = new Map();
  let cursor;
  do {
    const page = await media.list({ cursor, limit: 1000 });
    for (const object of page.objects) actual.set(object.key, object);
    cursor = page.truncated ? page.cursor : undefined;
  } while (cursor);
  if (actual.size !== manifest.objects.length)
    throw Error("Object key count mismatch");
  for (const expected of manifest.objects) {
    const object = actual.get(expected.key);
    if (!object || object.size !== expected.size)
      throw Error("Object key or size mismatch");
    const head = await media.head(expected.key);
    if (
      !head?.checksums.sha256 ||
      hex(head.checksums.sha256) !== expected.sha256
    )
      throw Error("Object checksum mismatch");
    if (head.httpMetadata?.contentType !== expected.contentType)
      throw Error("Object content type mismatch");
  }
  storage.sql.exec("UPDATE cf_imports SET status='active' WHERE id=?", id);
  return JSON.parse(state.report);
}
