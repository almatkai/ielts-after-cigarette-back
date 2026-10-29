import { DurableObject } from "cloudflare:workers";
import {
  CHUNK,
  MAX,
  BLOB_SHARDS,
  precondition,
  SQLiteBucket,
} from "./blob-store.mjs";
const RETAIN = 24 * 60 * 60 * 1000;
const SHARD_CAP = 64 * 1024 * 1024;
export default {
  fetch() {
    return new Response(null, { status: 404 });
  },
};
export class IACBlobChunks extends DurableObject {
  constructor(ctx, env) {
    super(ctx, env);
    this.sql = ctx.storage.sql;
    this.sql.exec(
      "CREATE TABLE IF NOT EXISTS chunks(upload TEXT NOT NULL,position INTEGER NOT NULL,bytes BLOB NOT NULL,PRIMARY KEY(upload,position)); CREATE TABLE IF NOT EXISTS usage(id INTEGER PRIMARY KEY CHECK(id=1),bytes INTEGER NOT NULL); INSERT OR IGNORE INTO usage VALUES(1,0); CREATE TABLE IF NOT EXISTS leases(upload TEXT PRIMARY KEY,expires INTEGER)",
    );
    this.sql.exec(
      "CREATE INDEX IF NOT EXISTS leases_expires ON leases(expires)",
    );
  }
  write(id, position, bytes) {
    if (
      !/^[0-9a-f]{32}$/.test(id) ||
      !Number.isInteger(position) ||
      position < 0 ||
      position >= MAX / (256 * 1024) ||
      !(bytes instanceof Uint8Array) ||
      !bytes.length ||
      bytes.length > CHUNK
    )
      throw Error("Invalid storage chunk");
    this.ctx.storage.transactionSync(() => {
      const previous =
        this.sql
          .exec(
            "SELECT length(bytes) AS size FROM chunks WHERE upload=? AND position=?",
            id,
            position,
          )
          .toArray()[0]?.size ?? 0;
      const used = this.sql
        .exec("SELECT bytes FROM usage WHERE id=1")
        .one().bytes;
      if (used - previous + bytes.length > SHARD_CAP)
        throw Error("Free blob shard capacity exceeded");
      this.sql.exec(
        "INSERT OR REPLACE INTO chunks VALUES(?,?,?)",
        id,
        position,
        bytes,
      );
      this.sql.exec(
        "UPDATE usage SET bytes=? WHERE id=1",
        used - previous + bytes.length,
      );
      if (
        this.sql.exec(
          "INSERT OR IGNORE INTO leases VALUES(?,?)",
          id,
          Date.now() + RETAIN,
        ).rowsWritten
      )
        this.schedule();
    });
  }
  schedule() {
    const next = this.sql
      .exec("SELECT min(expires) AS next FROM leases")
      .one().next;
    if (next === null) return;
    const desired = Math.max(Date.now() + 100, next);
    this.ctx.waitUntil(
      this.ctx.storage.getAlarm().then((current) => {
        if (current === null || current > desired)
          return this.ctx.storage.setAlarm(desired);
      }),
    );
  }
  async alarm() {
    const index = this.env.IAC_BLOB_INDEX.get(
      this.env.IAC_BLOB_INDEX.idFromName("ielts-media-v1"),
    );
    for (const { upload } of this.sql
      .exec("SELECT upload FROM leases WHERE expires<=? LIMIT 20", Date.now())
      .toArray()) {
      const lease = await index.lease(upload);
      if (!lease || (lease.expires !== null && lease.expires <= Date.now()))
        this.remove(upload);
      else
        this.sql.exec(
          "UPDATE leases SET expires=? WHERE upload=?",
          lease.expires,
          upload,
        );
    }
    this.schedule();
  }
  read(id, position) {
    const row = this.sql
      .exec(
        "SELECT bytes FROM chunks WHERE upload=? AND position=?",
        id,
        position,
      )
      .toArray()[0];
    if (!row) throw Error("Stored chunk is missing");
    return row.bytes;
  }
  remove(id) {
    this.ctx.storage.transactionSync(() => {
      const total = this.sql
        .exec(
          "SELECT coalesce(sum(length(bytes)),0) AS size FROM chunks WHERE upload=?",
          id,
        )
        .one().size;
      this.sql.exec("DELETE FROM chunks WHERE upload=?", id);
      this.sql.exec("DELETE FROM leases WHERE upload=?", id);
      this.sql.exec("UPDATE usage SET bytes=bytes-? WHERE id=1", total);
    });
  }
}
export class IACBlobIndex extends DurableObject {
  constructor(ctx, env) {
    super(ctx, env);
    this.sql = ctx.storage.sql;
    this.sql.exec(
      "CREATE TABLE IF NOT EXISTS objects(key TEXT PRIMARY KEY,meta TEXT NOT NULL); CREATE TABLE IF NOT EXISTS uploads(id TEXT PRIMARY KEY,key TEXT NOT NULL,parent TEXT,fields TEXT NOT NULL,part TEXT,expires INTEGER); CREATE INDEX IF NOT EXISTS expiry ON uploads(expires); CREATE TABLE IF NOT EXISTS multipart(id TEXT PRIMARY KEY,key TEXT NOT NULL,fields TEXT NOT NULL,parts TEXT NOT NULL,expires INTEGER NOT NULL);",
    );
    // Temporary cleanup must not scan permanent media on every staged upload.
    this.sql.exec(
      "CREATE INDEX IF NOT EXISTS temporary_objects_expiry ON objects(json_extract(meta,'$.uploaded')) WHERE substr(key,1,9)='_cf-temp/'",
    );
  }
  capacity() {
    if (this.sql.databaseSize >= 64 * 1024 * 1024)
      throw Error("Free blob catalog capacity exceeded");
  }
  async ingest(key, stream, options) {
    const bucket = new SQLiteBucket(null, this.env.IAC_BLOB_CHUNKS, this);
    const meta = await bucket.stage(key, stream, options);
    return this.publish(key, meta, options.onlyIf ?? null);
  }
  schedule() {
    const rows = this.sql
      .exec(
        "SELECT min(expires) AS next FROM (SELECT expires FROM uploads WHERE expires IS NOT NULL UNION ALL SELECT expires FROM multipart UNION ALL SELECT json_extract(meta,'$.uploaded')+? AS expires FROM objects WHERE substr(key,1,9)='_cf-temp/')",
        RETAIN,
      )
      .one();
    if (rows.next === null) return;
    const desired = Math.max(Date.now() + 100, rows.next);
    this.ctx.waitUntil(
      this.ctx.storage.getAlarm().then((current) => {
        if (current === null || current > desired)
          return this.ctx.storage.setAlarm(desired);
      }),
    );
  }
  begin({ id, key, parent, fields }) {
    this.capacity();
    if (this.sql.exec("SELECT count(*) AS n FROM uploads").one().n >= 10000)
      throw Error("Free upload-session capacity exceeded");
    if (
      parent &&
      !this.sql.exec("SELECT id FROM multipart WHERE id=?", parent).toArray()
        .length
    )
      throw Error("Multipart upload unavailable");
    this.sql.exec(
      "INSERT INTO uploads VALUES(?,?,?,?,NULL,?)",
      id,
      key,
      parent,
      JSON.stringify(fields),
      Date.now() + RETAIN,
    );
    this.schedule();
  }
  staged(id, part) {
    if (
      !this.sql
        .exec("SELECT id FROM uploads WHERE id=? AND expires>?", id, Date.now())
        .toArray().length
    )
      throw Error("Upload expired");
    this.sql.exec(
      "UPDATE uploads SET part=? WHERE id=?",
      JSON.stringify(part),
      id,
    );
  }
  lease(id) {
    return (
      this.sql
        .exec("SELECT expires FROM uploads WHERE id=?", id)
        .toArray()[0] ?? null
    );
  }
  head(key) {
    const row = this.sql
      .exec("SELECT meta FROM objects WHERE key=?", key)
      .toArray()[0];
    return row ? JSON.parse(row.meta) : null;
  }
  retire(meta) {
    for (const part of meta?.parts ?? [])
      this.sql.exec(
        "UPDATE uploads SET expires=? WHERE id=?",
        Date.now() + RETAIN,
        part.id,
      );
  }
  publish(key, meta, onlyIf) {
    return this.ctx.storage.transactionSync(() => {
      this.capacity();
      const old = this.head(key);
      if (!precondition(old, onlyIf)) {
        this.sql.exec("UPDATE uploads SET expires=0 WHERE id=?", meta.version);
        this.schedule();
        return null;
      }
      if (
        !old &&
        this.sql.exec("SELECT count(*) AS n FROM objects").one().n >= 10000
      )
        throw Error("Free blob object count exceeded");
      for (const part of meta.parts)
        if (
          !this.sql
            .exec(
              "SELECT id FROM uploads WHERE id=? AND part IS NOT NULL AND expires>?",
              part.id,
              Date.now(),
            )
            .toArray().length
        )
          throw Error("Upload expired");
      meta.key = key;
      this.retire(old);
      this.sql.exec(
        "INSERT OR REPLACE INTO objects VALUES(?,?)",
        key,
        JSON.stringify(meta),
      );
      for (const part of meta.parts)
        this.sql.exec("UPDATE uploads SET expires=NULL WHERE id=?", part.id);
      this.schedule();
      return meta;
    });
  }
  remove(key) {
    this.ctx.storage.transactionSync(() => {
      this.retire(this.head(key));
      this.sql.exec("DELETE FROM objects WHERE key=?", key);
    });
    this.schedule();
  }
  list({ prefix = "", cursor, limit = 1000 }) {
    if (
      !Number.isInteger(limit) ||
      limit < 1 ||
      limit > 1000 ||
      typeof prefix !== "string"
    )
      throw Error("Invalid list request");
    let after = "";
    if (cursor) {
      const decoded = JSON.parse(
        new TextDecoder().decode(
          Uint8Array.from(atob(cursor), (c) => c.charCodeAt(0)),
        ),
      );
      if (decoded.prefix !== prefix || typeof decoded.after !== "string")
        throw Error("Invalid list cursor");
      after = decoded.after;
    }
    const rows = this.sql
      .exec(
        "SELECT key,meta FROM objects WHERE key>? AND substr(key,1,length(?))=? ORDER BY key LIMIT ?",
        after,
        prefix,
        prefix,
        limit + 1,
      )
      .toArray();
    const truncated = rows.length > limit;
    rows.length = Math.min(rows.length, limit);
    return {
      objects: rows.map((row) => JSON.parse(row.meta)),
      truncated,
      cursor: truncated
        ? btoa(
            String.fromCharCode(
              ...new TextEncoder().encode(
                JSON.stringify({ prefix, after: rows.at(-1).key }),
              ),
            ),
          )
        : undefined,
    };
  }
  multipart({ id, key, fields }) {
    this.capacity();
    this.sql.exec(
      "INSERT INTO multipart VALUES(?,?,?,?,?)",
      id,
      key,
      JSON.stringify(fields),
      "{}",
      Date.now() + RETAIN,
    );
    this.schedule();
  }
  upload(key, id) {
    const row = this.sql
      .exec(
        "SELECT * FROM multipart WHERE id=? AND key=? AND expires>?",
        id,
        key,
        Date.now(),
      )
      .toArray()[0];
    if (!row) throw Error("Multipart upload unavailable");
    return {
      ...row,
      fields: JSON.parse(row.fields),
      parts: JSON.parse(row.parts),
    };
  }
  part(id, number, meta) {
    this.ctx.storage.transactionSync(() => {
      this.capacity();
      const row = this.sql
        .exec("SELECT key FROM multipart WHERE id=?", id)
        .toArray()[0];
      if (!row) throw Error("Multipart upload unavailable");
      const upload = this.upload(row.key, id);
      if (upload.parts[number]) this.retire(upload.parts[number]);
      upload.parts[number] = meta;
      if (
        Object.keys(upload.parts).length > 2048 ||
        JSON.stringify(upload.parts).length > 512 * 1024
      )
        throw Error("Multipart descriptor limit exceeded");
      this.sql.exec(
        "UPDATE multipart SET parts=? WHERE id=?",
        JSON.stringify(upload.parts),
        id,
      );
    });
  }
  completion(key, id, parts) {
    const upload = this.upload(key, id);
    if (!Array.isArray(parts) || !parts.length)
      throw Error("Empty multipart completion");
    let previous = 0;
    const selected = parts.map((part) => {
      const meta = upload.parts[part.partNumber];
      if (!meta || part.partNumber <= previous || meta.etag !== part.etag)
        throw Error("Multipart part mismatch");
      previous = part.partNumber;
      return meta;
    });
    if (selected.reduce((n, x) => n + x.size, 0) > MAX)
      throw Error("Multipart object too large");
    return { fields: upload.fields, parts: selected };
  }
  finishMultipart(key, id, parts, meta) {
    return this.ctx.storage.transactionSync(() => {
      const upload = this.completion(key, id, parts);
      if (
        JSON.stringify(upload.parts.flatMap((x) => x.parts)) !==
        JSON.stringify(meta.parts)
      )
        throw Error("Multipart changed during completion");
      const result = this.publish(key, meta, null);
      this.sql.exec("DELETE FROM multipart WHERE id=?", id);
      return result;
    });
  }
  discard(id) {
    this.sql.exec(
      "UPDATE uploads SET expires=0 WHERE (id=? OR parent=?) AND expires IS NOT NULL",
      id,
      id,
    );
    this.sql.exec("DELETE FROM multipart WHERE id=?", id);
    this.schedule();
  }
  async alarm() {
    for (const { key } of this.sql
      .exec(
        "SELECT key FROM objects WHERE substr(key,1,9)='_cf-temp/' AND json_extract(meta,'$.uploaded')<=? LIMIT 20",
        Date.now() - RETAIN,
      )
      .toArray())
      this.remove(key);
    const expiredMultipart = this.sql
      .exec("SELECT id FROM multipart WHERE expires<=? LIMIT 10", Date.now())
      .toArray();
    for (const { id } of expiredMultipart) this.discard(id);
    const expired = this.sql
      .exec("SELECT id,part FROM uploads WHERE expires<=? LIMIT 4", Date.now())
      .toArray();
    for (const { id, part } of expired) {
      this.sql.exec(
        "UPDATE uploads SET expires=-1 WHERE id=? AND expires<=?",
        id,
        Date.now(),
      );
      const count = part
        ? Math.min(BLOB_SHARDS, JSON.parse(part).count)
        : BLOB_SHARDS;
      for (let offset = 0; offset < count; offset++) {
        const n = part
          ? (parseInt(id.slice(0, 8), 16) + offset) % BLOB_SHARDS
          : offset;
        await this.env.IAC_BLOB_CHUNKS.get(
          this.env.IAC_BLOB_CHUNKS.idFromName(String(n)),
        ).remove(id);
      }
      this.sql.exec(
        "DELETE FROM uploads WHERE id=? AND expires<=?",
        id,
        Date.now(),
      );
    }
    this.schedule();
  }
}
