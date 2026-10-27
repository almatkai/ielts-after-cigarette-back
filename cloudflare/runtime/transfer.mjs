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
    "CREATE TABLE IF NOT EXISTS cf_import_staging(id TEXT NOT NULL,kind TEXT NOT NULL,table_name TEXT NOT NULL,position INTEGER NOT NULL,payload TEXT NOT NULL,PRIMARY KEY(id,kind,table_name,position))",
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
    JSON.stringify(Object.keys(manifest.tables).sort()) !==
    JSON.stringify(schema.tables.map((t) => t.name).sort())
  )
    throw Error("Incomplete table manifest");
  const map = new Map(schema.tables.map((t) => [t.name, t]));
  function pristine() {
    for (const table of schema.tables) {
      const seed = sqlite.seeds.find((s) => s.table === table.name),
        rows = storage.sql.exec(`SELECT * FROM "${table.name}"`).toArray();
      if (
        rows.length !== (seed?.rows.length ?? 0) ||
        rows.some(
          (row) =>
            !seed.rows.some((expected) =>
              Object.entries(expected).every(([k, v]) => row[k] === v),
            ),
        )
      )
        throw Error("Target is not a pristine migration target");
    }
  }
  for (const table of schema.tables) {
    const meta = manifest.tables[table.name];
    if (
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
  storage.sql.exec(
    "INSERT INTO cf_imports VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status",
    id,
    "receiving",
    first.value,
    JSON.stringify(report),
  );
  let redisCount = 0,
    redisChain = zero;
  try {
    // Never await streamed RPC input inside a SQLite transaction: input gates would
    // block delivery of that same stream. Stage bounded records durably, then
    // atomically publish the complete, verified dataset without any awaits.
    for (;;) {
      const item = await iterator.next();
      if (item.done) break;
      const record = JSON.parse(item.value);
      if (record.kind === "redis") {
        redisChain = await chain(redisChain, item.value);
        const native = redisValue(record);
        report.coordination[native.disposition]++;
        const payload = JSON.stringify({
          key: record.key,
          expires: record.expires,
          ...native,
        });
        storage.sql.exec(
          "INSERT INTO cf_import_staging VALUES(?,?,?,?,?) ON CONFLICT(id,kind,table_name,position) DO UPDATE SET payload=excluded.payload WHERE payload<>excluded.payload",
          id,
          "redis",
          "",
          redisCount++,
          payload,
        );
        continue;
      }
      const table = map.get(record.table);
      if (
        !table ||
        !Array.isArray(record.values) ||
        record.values.length !== table.columns.length
      )
        throw Error("Unknown table or column");
      record.values.forEach((v, i) => importValue(table.columns[i], v));
      const state = report.tables[table.name];
      state.chain = await chain(state.chain, item.value);
      storage.sql.exec(
        "INSERT INTO cf_import_staging VALUES(?,?,?,?,?) ON CONFLICT(id,kind,table_name,position) DO UPDATE SET payload=excluded.payload WHERE payload<>excluded.payload",
        id,
        "row",
        table.name,
        state.count++,
        item.value,
      );
    }
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
      storage.sql.exec("PRAGMA defer_foreign_keys=ON");
      for (const table of schema.tables)
        storage.sql.exec(`DELETE FROM "${table.name}"`);
      for (const table of schema.tables) {
        let count = 0;
        for (const { payload } of storage.sql.exec(
          "SELECT payload FROM cf_import_staging WHERE id=? AND kind='row' AND table_name=? AND position<? ORDER BY position",
          id,
          table.name,
          report.tables[table.name].count,
        )) {
          const record = JSON.parse(payload),
            values = record.values.map((v, i) =>
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
      for (const { payload } of storage.sql.exec(
        "SELECT payload FROM cf_import_staging WHERE id=? AND kind='redis' AND position<? ORDER BY position",
        id,
        redisCount,
      )) {
        const record = JSON.parse(payload);
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
      storage.sql.exec(
        "UPDATE cf_imports SET status=?,report=? WHERE id=?",
        "complete",
        JSON.stringify(report),
        id,
      );
    });
  } catch (e) {
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
