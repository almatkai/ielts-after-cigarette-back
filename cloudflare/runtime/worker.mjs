import { mediaEnvironment } from "./blob-store.mjs";
import { NativeFetch } from "./fetch.mjs";
import { storageError } from "./errors.mjs";
import { migrationStatus, importDatabase, activate } from "./transfer.mjs";
import { applySchemaUpgrade, applySchemaRevert } from "./schema-upgrade.mjs";
import { DurableObject, WorkerEntrypoint } from "cloudflare:workers";
import wasm from "../dist/api.wasm";
import { Go } from "../dist/wasm_exec.js";
import schema from "../schema/sqlite.json";
import baselineSchema from "../schema/history/sqlite-9f195b8e3a83f584953e70de4456a96ab6912a20e8f8c20481024e6e6b9e7ed6.json";
import { translate } from "./sql.mjs";
import { specialSQL } from "./special-sql.mjs";
import { Coordination } from "./redis.mjs";
import { SqlReadProfile } from "./sql-read-profile.mjs";

const ok = (data) => JSON.stringify({ data });
const error = storageError;
const encode = (bytes) => {
  let s = "";
  for (let i = 0; i < bytes.length; i += 8192)
    s += String.fromCharCode(...bytes.subarray(i, i + 8192));
  return btoa(s);
};
const decode = (s) => Uint8Array.from(atob(s), (c) => c.charCodeAt(0));
export default {
  async fetch(request, env) {
    // Public entry has no origin URL, sockets, VPC, container or external database.
    return env.IAC_STATE.get(env.IAC_STATE.idFromName("ielts-dev-v1")).fetch(
      request,
    );
  },
};
// Service-binding capability only: ordinary HTTP never exposes SQL diagnostics.
export class SqlProfileBridge extends WorkerEntrypoint {
  profile() {
    return this.env.IAC_STATE.get(
      this.env.IAC_STATE.idFromName("ielts-dev-v1"),
    ).getSqlReadProfile();
  }
  fetch() {
    return new Response(null, { status: 404 });
  }
}

export class IELTSDev extends DurableObject {
  constructor(ctx, env) {
    super(ctx, env);
    this.sqlProfile = new SqlReadProfile(env.IAC_SQL_PROFILE === "true");
    this.db = this.sqlProfile.wrapStorage(ctx.storage);
    this.env = mediaEnvironment(env);
    env = this.env;
    this.tail = Promise.resolve();
    this.transports = new Map();
    this.transaction = null;
    this.ready = null;
    this.activeJobs = new Set();
    this.nativeFetch = new NativeFetch(env.MEDIA, env.IAC_AUDIO);
    ctx.blockConcurrencyWhile(async () => {
      const sql = this.db.sql;
      sql.exec(
        "CREATE TABLE IF NOT EXISTS cf_schema(version TEXT PRIMARY KEY)",
      );
      const storedSchema = sql
        .exec("SELECT version FROM cf_schema")
        .toArray()[0];
      if (storedSchema && storedSchema.version !== schema.version)
        if (this.env.SCHEMA_REVERT === "true")
          this.revertedSchema = applySchemaRevert(this.db, baselineSchema);
        else this.upgradedSchema = applySchemaUpgrade(this.db, schema);
      if (!storedSchema) {
        this.db.transactionSync(() => {
          for (const statement of schema.statements) sql.exec(statement);
          for (const seed of schema.seeds)
            for (const row of seed.rows) {
              const columns = Object.keys(row);
              sql.exec(
                `INSERT INTO ${seed.table}(${columns.join(",")}) VALUES(${columns.map(() => "?").join(",")})`,
                ...Object.values(row),
              );
            }
          sql.exec("INSERT INTO cf_schema VALUES(?)", schema.version);
        });
      }
      this.coordination = new Coordination(this.db);
    });
  }
  serial(fn) {
    const task = this.tail.then(fn);
    this.tail = task.catch(() => {});
    return task;
  }
  async initialize() {
    if (this.ready) return this.ready;
    this.ready = (async () => {
      const existing = globalThis.iacRuntime;
      if (existing && existing.objectID !== this.ctx.id.toString())
        throw Error(
          "Only the selected singleton application object may own this runtime",
        );
      if (existing) existing.owner.detachRuntime();
      globalThis.iacBridge = (op, input, bytes, id) =>
        this.bridge(op, JSON.parse(input), bytes, id);
      if (!existing) {
        const go = new Go();
        go.env = { GOMEMLIMIT: "64MiB", GOGC: "50" };
        const instance = await WebAssembly.instantiate(wasm, go.importObject);
        this.wasmMemory = instance.exports.mem ?? instance.exports.memory;
        go.run(instance).catch(() => {});
        if (!globalThis.iacInit) throw Error("Go runtime did not initialize");
        globalThis.iacRuntime = {
          objectID: this.ctx.id.toString(),
          owner: this,
          memory: this.wasmMemory,
        };
      } else {
        this.wasmMemory = existing.memory;
        existing.owner = this;
      }
      const settings = {
        APP_ENV: "development",
        DATABASE_URL: "cloudflare:sqlite",
        REDIS_URL: "cloudflare:coordination",
        WRITING_WORKER_EXTERNAL: "true",
        AI_PROVIDER_TELEMETRY_OPTIONAL: "true",
      };
      // Only string application settings enter os.Environ; binding objects never do.
      for (const [k, v] of Object.entries(this.env))
        if (typeof v === "string" && !k.startsWith("CF_")) settings[k] = v;
      Object.assign(settings, {
        APP_ENV: "development",
        DATABASE_URL: "cloudflare:sqlite",
        REDIS_URL: "cloudflare:coordination",
        OBJECT_STORAGE_BACKEND: "filesystem",
        WRITING_WORKER_EXTERNAL: "true",
      });
      this.settings = settings;
      await globalThis.iacInit(JSON.stringify(settings));
    })();
    return this.ready;
  }
  detachRuntime() {
    for (const state of this.nativeFetch.requests.values())
      state.controller.abort();
    for (const transport of this.transports.values())
      transport.writer.abort(Error("Object reset")).catch(() => {});
    this.transports.clear();
    this.activeJobs.clear();
  }
  bridge(op, input, bytes, id = "") {
    if (id && !this.transports.has(id) && !this.activeJobs.has(id))
      return error(Error("Request no longer belongs to the active object"));
    if (
      ["sql", "redis", "begin", "ping", "cursor.start", "cursor.next"].includes(
        op,
      ) &&
      this.transaction &&
      this.transaction.owner !== id
    )
      return this.transaction.done
        .catch(() => {})
        .then(() => this.bridge(op, input, bytes, id));
    const transport = this.transports.get(id);
    try {
      switch (op) {
        case "ping":
          this.db.sql.exec("SELECT 1");
          return ok(null);
        case "cursor.start": {
          const translated = translate(input, []),
            cursor = this.db.sql.exec(translated.sql, ...translated.args);
          this.cursors ??= new Map();
          const key = crypto.randomUUID();
          this.cursors.set(key, {
            owner: id,
            iterator: cursor.raw(),
            profileCursor: cursor,
          });
          return ok({ id: key, columns: cursor.columnNames });
        }
        case "cursor.next": {
          const cursor = this.cursors?.get(input);
          if (!cursor || cursor.owner !== id)
            throw Error("Invalid export cursor");
          const rows = [];
          for (let i = 0; i < 100; i++) {
            const item = cursor.iterator.next();
            if (item.done) {
              this.cursors.delete(input);
              break;
            }
            rows.push(item.value);
          }
          this.sqlProfile.sample(cursor.profileCursor);
          return ok(rows);
        }
        case "cursor.close":
          this.sqlProfile.sample(this.cursors?.get(input)?.profileCursor);
          this.cursors?.delete(input);
          return ok(null);
        case "sql":
          return ok(this.query(input.sql, input.args));
        case "begin": {
          if (this.transaction)
            throw Error("overlapping application transactions");
          const transactionID = crypto.randomUUID();
          let release, reject, entered;
          const gate = new Promise((r) => (entered = r));
          const done = this.db
            .transaction(async () => {
              entered();
              return new Promise((r, j) => {
                release = r;
                reject = j;
              });
            })
            .finally(() => {
              if (this.transaction?.id === transactionID)
                this.transaction = null;
            });
          done.catch(() => {});
          this.transaction = {
            id: transactionID,
            owner: id,
            done,
            release: () => release(),
            reject: () => reject(new Error("rollback")),
          };
          return gate.then(() => ok(transactionID));
        }
        case "commit":
        case "rollback": {
          const t = this.transaction;
          if (!t || t.id !== input) {
            if (op === "rollback") return ok(null);
            throw Error("invalid transaction");
          }
          if (op === "commit") t.release();
          else t.reject();
          return t.done.then(
            () => ok(null),
            (e) => (op === "rollback" ? ok(null) : error(e)),
          );
        }
        case "redis": {
          const value = this.coordination.run(input.op, input.args ?? []);
          if (input.op === "LPush") {
            this.db.sql.exec(
              "DELETE FROM cf_coordination WHERE key=?",
              input.args[0],
            );
            this.ctx.waitUntil(this.db.setAlarm(Date.now() + 100));
          }
          return ok(value);
        }
        case "r2check":
          return this.env.MEDIA.list({ limit: 1 }).then(() => ok(null));
        case "http.read":
          return this.readRequest(input, id).then(ok);
        case "http.headers": {
          // Read-only list handlers and validation failures intentionally defer a
          // rollback until after writing. Release that rollback before crossing the
          // response stream's input gate; never commit an uncommitted transaction.
          if (this.transaction?.owner === id) {
            const transaction = this.transaction;
            transaction.reject();
            return transaction.done
              .catch(() => {})
              .then(() => this.bridge(op, input, bytes, id));
          }
          const headers = new Headers();
          for (const [key, values] of Object.entries(input.headers))
            for (const v of values) headers.append(key, v);
          headers.set("cache-control", "no-store");
          const noBody =
            transport.method === "HEAD" ||
            [101, 204, 205, 304].includes(input.status);
          transport.noBody = noBody;
          transport.respond(
            new Response(noBody ? null : transport.stream.readable, {
              status: input.status,
              headers,
            }),
          );
          return ok(null);
        }
        case "http.r2stream":
          if (transport.noBody) return ok(input.length);
          return this.env.MEDIA.get(input.key, {
            range: { offset: input.offset, length: input.length },
          }).then(async (object) => {
            if (!object) throw Error("Object disappeared");
            let written = 0;
            const reader = object.body.getReader();
            try {
              for (;;) {
                const { done, value } = await reader.read();
                if (done) break;
                await transport.writer.write(value);
                written += value.byteLength;
              }
            } finally {
              reader.releaseLock();
            }
            return ok(written);
          });
        case "http.write":
          if (transport.noBody) return ok(null);
          return transport.writer.write(bytes).then(() => ok(null));
        case "fetch.start":
          return this.nativeFetch.start(input, bytes).then(ok);
        case "fetch.read":
          return this.nativeFetch.read(input).then(ok);
        case "fetch.close":
          return this.nativeFetch.close(input).then(() => ok(null));
        case "r2head":
          return this.env.MEDIA.head(input).then((o) => ok(o?.size ?? null));
        case "r2range":
          return this.env.MEDIA.get(input.key, {
            range: { offset: input.offset, length: input.length },
          }).then(async (o) => {
            if (!o) throw Error("Object disappeared");
            return ok(encode(new Uint8Array(await o.arrayBuffer())));
          });
        case "r2copy":
          return this.env.MEDIA.get(input.source).then(async (object) => {
            if (!object || object.size !== input.size)
              throw Error("Temporary object size mismatch");
            await this.env.MEDIA.put(input.key, object.body, {
              httpMetadata: { contentType: input.contentType },
            });
            return ok(null);
          });
        case "r2start":
          return this.env.MEDIA.createMultipartUpload(input.key, {
            httpMetadata: { contentType: input.contentType },
          }).then((upload) => {
            this.uploads ??= new Map();
            const id = crypto.randomUUID();
            this.uploads.set(id, { upload, parts: [] });
            return ok(id);
          });
        case "r2part": {
          const u = this.uploads.get(input.upload);
          return u.upload.uploadPart(input.part, bytes).then((part) => {
            u.parts.push(part);
            return ok(null);
          });
        }
        case "r2complete": {
          const u = this.uploads.get(input);
          return u.upload.complete(u.parts).then(() => {
            this.uploads.delete(input);
            return ok(null);
          });
        }
        case "r2abort": {
          const u = this.uploads.get(input);
          this.uploads.delete(input);
          return u ? u.upload.abort().then(() => ok(null)) : ok(null);
        }
        case "r2delete":
          return this.env.MEDIA.delete(input).then(() => ok(null));
        default:
          throw Error("Unsupported bridge operation");
      }
    } catch (e) {
      if (this.env.CF_TEST_MODE === "true")
        console.error(op, op === "sql" ? input.sql : "", e.message);
      return error(e);
    }
  }
  query(original, args) {
    return this.sqlProfile.observe(() => this.queryObserved(original, args));
  }
  queryObserved(original, args) {
    if (
      /^\s*LOCK TABLE ai_providers IN SHARE ROW EXCLUSIVE MODE\s*$/i.test(
        original,
      )
    ) {
      if (!this.transaction)
        throw Error("Table lock requires an atomic transaction");
      return { rows: [], columns: [], changes: 0 };
    }
    if (/pg_advisory_xact_lock\(/i.test(original))
      return { rows: [[null]], columns: ["lock"], changes: 0 };
    const special = specialSQL(this.db, original, args, (q, a) =>
      this.query(q, a),
    );
    if (special !== undefined) return special;
    const q = translate(original, args);
    const cursor = this.db.sql.exec(q.sql, ...q.args);
    const columns = cursor.columnNames;
    const rows = Array.from(cursor.raw(), (row) =>
      row.map((v) =>
        v instanceof ArrayBuffer ? { $bytes: encode(new Uint8Array(v)) } : v,
      ),
    );
    const changes = this.db.sql.exec("SELECT changes() AS n").one().n;
    return { rows, columns, changes };
  }
  async schedule() {
    while (this.transaction) await this.transaction.done.catch(() => {});
    const writingJobs = this.db.sql
      .exec(
        "SELECT MIN(CASE WHEN status='PROCESSING' THEN strftime('%s',updated_at)*1000+121000 ELSE strftime('%s',next_attempt_at)*1000 END) AS next FROM writing_assessment_jobs WHERE (status='QUEUED' AND attempts<3) OR status='PROCESSING'",
      )
      .one().next;
    const speakingJobs = this.db.sql
      .exec(
        "SELECT MIN(CASE WHEN status='PROCESSING' THEN strftime('%s',updated_at)*1000+121000 ELSE strftime('%s',next_attempt_at)*1000 END) AS next FROM speaking_assessment_jobs WHERE (status='QUEUED' AND attempts<3) OR status='PROCESSING'",
      )
      .one().next;
    const jobs =
      writingJobs === null && speakingJobs === null
        ? null
        : Math.min(writingJobs ?? Infinity, speakingJobs ?? Infinity);
    let cleanup = this.db.sql
      .exec(
        "SELECT MIN(expires) next FROM cf_coordination WHERE expires IS NOT NULL",
      )
      .one().next;
    if (
      this.db.sql
        .exec("SELECT 1 FROM account_object_deletions LIMIT 1")
        .toArray().length
    )
      cleanup = Math.min(cleanup ?? Infinity, Date.now() + 60000);
    if (jobs !== null || cleanup !== null)
      await this.db.setAlarm(
        Math.max(
          Date.now() + 100,
          Math.min(jobs ?? Infinity, cleanup ?? Infinity),
        ),
      );
  }
  async alarm() {
    if (this.transaction) await this.transaction.done.catch(() => {});
    await this.initialize();
    // Persist the recovery wake before provider I/O; an eviction cannot orphan a lease.
    await this.db.setAlarm(Date.now() + 125000);
    const pendingWriting = this.db.sql
      .exec(
        "SELECT attempt_id FROM writing_assessment_jobs WHERE status='QUEUED' AND attempts<3 AND next_attempt_at<=? ORDER BY next_attempt_at,created_at LIMIT 1",
        new Date().toISOString(),
      )
      .toArray()[0];
    const pendingSpeaking = this.db.sql
      .exec(
        "SELECT attempt_id FROM speaking_assessment_jobs WHERE status='QUEUED' AND attempts<3 AND next_attempt_at<=? ORDER BY next_attempt_at,created_at LIMIT 1",
        new Date().toISOString(),
      )
      .toArray()[0];
    const wJobID = "alarm:" + crypto.randomUUID();
    this.activeJobs.add(wJobID);
    try {
      if (pendingWriting) {
        await globalThis.iacJob(pendingWriting.attempt_id, wJobID, "writing");
      } else {
        await globalThis.iacJob("", wJobID, "writing");
      }
    } finally {
      this.activeJobs.delete(wJobID);
    }
    const sJobID = "alarm:" + crypto.randomUUID();
    this.activeJobs.add(sJobID);
    try {
      if (pendingSpeaking) {
        await globalThis.iacJob(pendingSpeaking.attempt_id, sJobID, "speaking");
      } else {
        await globalThis.iacJob("", sJobID, "speaking");
      }
    } finally {
      this.activeJobs.delete(sJobID);
    }
    this.db.sql.exec(
      "DELETE FROM cf_coordination WHERE key IN(SELECT key FROM cf_coordination WHERE expires<=? LIMIT 1000)",
      Date.now(),
    );
    const objects = this.db.sql
      .exec("SELECT store,storage_key FROM account_object_deletions LIMIT 100")
      .toArray();
    for (const object of objects) {
      await this.env.MEDIA.delete(object.storage_key);
      this.db.sql.exec(
        "DELETE FROM account_object_deletions WHERE store=? AND storage_key=?",
        object.store,
        object.storage_key,
      );
    }
    await this.db.deleteAlarm();
    await this.schedule();
  }
  importData(request) {
    return this.serial(() => importDatabase(this.db, request.body));
  }
  activateData(id) {
    return this.serial(async () => {
      const report = await activate(this.db, this.env.MEDIA, id);
      await this.schedule();
      return report;
    });
  }
  importReport() {
    return this.serial(() => migrationStatus(this.db));
  }
  async readRequest(length, id) {
    const t = this.transports.get(id);
    while (!t.pending?.length) {
      if (!t.reader) return null;
      const chunk = await t.reader.read();
      if (chunk.done) return null;
      t.pending = chunk.value;
    }
    const bytes = t.pending.subarray(0, length);
    t.pending = t.pending.subarray(bytes.length);
    return encode(bytes);
  }
  // Binding-only RPC: no request route exposes diagnostics or query text.
  getSqlReadProfile() {
    return this.sqlProfile.snapshot();
  }
  async getMetrics() {
    const response = await this.handle(
      new Request("https://private.invalid/metrics"),
      true,
    );
    if (!this.sqlProfile.enabled || !response.ok) return response;
    const headers = new Headers(response.headers);
    headers.delete("content-length");
    return new Response(
      (await response.text()) + "\n" + this.sqlProfile.prometheus(),
      {
        status: response.status,
        headers,
      },
    );
  }
  fetch(request) {
    return this.handle(request, false);
  }
  async handle(request, privateMetrics) {
    let respond,
      responded = false;
    const response = new Promise((resolve) => {
      respond = (value) => {
        if (!responded) {
          responded = true;
          resolve(value);
        }
      };
    });
    const id = crypto.randomUUID();
    const task = (async () => {
      let writer, transport;
      try {
        if (
          this.env.IAC_REQUIRE_IMPORT !== "false" &&
          migrationStatus(this.db)?.status !== "active"
        ) {
          respond(
            Response.json(
              {
                code: "DEV_DATA_NOT_ACTIVATED",
                message: "DEV data migration is not activated",
              },
              { status: 503 },
            ),
          );
          return;
        }
        await this.initialize();
        const stream = new TransformStream();
        writer = stream.writable.getWriter();
        transport = {
          stream,
          writer,
          respond,
          method: request.method,
          reader: request.body?.getReader(),
        };
        this.transports.set(id, transport);
        const headers = {};
        for (const [key, value] of request.headers) headers[key] = [value];
        delete headers["accept-encoding"];
        delete headers.forwarded;
        delete headers["x-real-ip"];
        const ip = request.headers.get("cf-connecting-ip") ?? "127.0.0.1";
        headers["x-forwarded-for"] = [ip];
        const input = {
          id,
          privateMetrics,
          method: request.method,
          url: request.url,
          headers,
          contentLength: request.body
            ? Number(request.headers.get("content-length") ?? -1)
            : 0,
          remoteAddr: (ip.includes(":") ? "[" + ip + "]" : ip) + ":0",
        };
        await globalThis.iacHandle(JSON.stringify(input));
        if (this.transaction?.owner === id) {
          const transaction = this.transaction;
          transaction.reject();
          await transaction.done.catch(() => {});
          throw Error("unclosed application transaction");
        }
        if (transport.reader) while (!(await transport.reader.read()).done) {}
        await writer.close();
        await this.schedule();
      } catch (e) {
        if (this.env.CF_TEST_MODE === "true") console.error(e.stack);
        if (responded) {
          await writer?.abort(e).catch(() => {});
        } else {
          respond(
            Response.json(
              {
                code: "DEV_RUNTIME_UNAVAILABLE",
                message: "DEV runtime unavailable",
              },
              { status: 503 },
            ),
          );
        }
      } finally {
        if (this.transaction?.owner === id) {
          const transaction = this.transaction;
          transaction.reject();
          await transaction.done.catch(() => {});
        }
        this.transports.delete(id);
        for (const [key, cursor] of this.cursors ?? [])
          if (cursor.owner === id) this.cursors.delete(key);
      }
    })();
    this.ctx.waitUntil(task);
    return response;
  }
}
