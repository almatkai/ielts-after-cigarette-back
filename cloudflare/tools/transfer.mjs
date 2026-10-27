#!/usr/bin/env node
// Seal a private export, validate every table/object/key, then transfer only on explicit execution.
import { createHash } from "node:crypto";
import { createReadStream } from "node:fs";
import { readFile, writeFile, stat } from "node:fs/promises";
import { createInterface } from "node:readline";
import { Readable } from "node:stream";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { importValue } from "../runtime/transfer.mjs";
import pgSchema from "../schema/postgres.json" with { type: "json" };
const sha = (bytes) => createHash("sha256").update(bytes).digest("hex"),
  zero = "0".repeat(64);
const next = (previous, line) =>
  sha(
    Buffer.concat([
      Buffer.from(previous, "hex"),
      Buffer.from(sha(line), "hex"),
    ]),
  );
async function privateFile(path) {
  const info = await stat(path);
  if (info.mode & 0o077) throw Error("Private export files must have mode 600");
  return readFile(path, "utf8");
}
export async function seal(directory) {
  const manifest = JSON.parse(
      await privateFile(resolve(directory, "database.json")),
    ),
    objects = JSON.parse(await privateFile(resolve(directory, "objects.json"))),
    redis = JSON.parse(await privateFile(resolve(directory, "redis.json")));
  if (
    manifest.format !== "iac-cloudflare-dev-v1" ||
    manifest.revision !== pgSchema.revision
  )
    throw Error("Wrong DEV revision");
  const expected = new Set(pgSchema.tables.map((t) => t.name));
  if (
    expected.size !== Object.keys(manifest.tables).length ||
    Object.keys(manifest.tables).some((t) => !expected.has(t))
  )
    throw Error("Incomplete schema coverage");
  if ((await stat(resolve(directory, "rows.jsonl"))).mode & 0o077)
    throw Error("Private row export must have mode 600");
  const observed = Object.fromEntries(
    [...expected].map((name) => [name, { count: 0, chain: zero }]),
  );
  let maxNativeRowBytes = 0;
  const references = [];
  const tables = new Map(pgSchema.tables.map((t) => [t.name, t]));
  for await (const line of createInterface({
    input: createReadStream(resolve(directory, "rows.jsonl")),
    crlfDelay: Infinity,
  })) {
    const record = JSON.parse(line),
      state = observed[record.table];
    if (!state) throw Error("Unknown table");
    const table = tables.get(record.table);
    if (
      !Array.isArray(record.values) ||
      record.values.length !== table.columns.length
    )
      throw Error("Invalid row shape");
    const bytes = record.values.reduce((n, v, i) => {
      const value = importValue(table.columns[i], v);
      return (
        n +
        (value instanceof ArrayBuffer
          ? value.byteLength
          : typeof value === "string"
            ? Buffer.byteLength(value)
            : 8) +
        16
      );
    }, 0);
    if (bytes > 2 * 1024 * 1024)
      throw Error("Row exceeds native SQLite limit: " + record.table);
    maxNativeRowBytes = Math.max(maxNativeRowBytes, bytes);

    if (
      [
        "listening_media",
        "writing_media",
        "blog_media",
        "speaking_recordings",
        "writer_applications",
      ].includes(record.table)
    ) {
      const keyColumn = table.columns.findIndex(
          (c) =>
            c.name ===
            (record.table === "writer_applications"
              ? "certificate_storage_key"
              : "storage_key"),
        ),
        sizeColumn = table.columns.findIndex((c) => c.name === "byte_size");
      if (keyColumn >= 0 && record.values[keyColumn])
        references.push({
          table: record.table,
          key: record.values[keyColumn],
          size: sizeColumn >= 0 ? Number(record.values[sizeColumn]) : null,
        });
    }
    state.count++;
    state.chain = next(state.chain, line);
  }
  for (const [name, state] of Object.entries(observed)) {
    const meta = manifest.tables[name];
    if (meta.count !== state.count || meta.chain !== state.chain)
      throw Error("Table count/checksum mismatch");
  }
  const keys = new Set();
  for (const object of objects) {
    if (
      typeof object.key !== "string" ||
      !object.key ||
      keys.has(object.key) ||
      object.key.startsWith("_cf-temp/") ||
      typeof object.contentType !== "string" ||
      !object.contentType ||
      /[\r\n]/.test(object.contentType) ||
      !/^[0-9a-f]{64}$/.test(object.file)
    )
      throw Error("Invalid object key manifest");
    keys.add(object.key);
    const path = resolve(directory, "objects", object.file),
      info = await stat(path);
    if (info.mode & 0o077 || info.size !== object.size)
      throw Error("Object mode/size mismatch");
    const h = createHash("sha256");
    for await (const chunk of createReadStream(path)) h.update(chunk);
    if (h.digest("hex") !== object.sha256)
      throw Error("Object checksum mismatch");
  }
  const objectByKey = new Map(objects.map((o) => [o.key, o]));
  for (const reference of references) {
    const object = objectByKey.get(reference.key);
    if (!object || (reference.size !== null && object.size !== reference.size))
      throw Error(
        "Referenced media is missing or has a different byte size: " +
          reference.table,
      );
  }
  const referencedKeys = new Set(references.map((r) => r.key));
  manifest.mediaCoverage = {
    referencedRows: references.length,
    uniqueReferencedKeys: referencedKeys.size,
    unreferencedCurrentKeys: objects.length - referencedKeys.size,
    rowsByTable: Object.fromEntries(
      [
        "listening_media",
        "writing_media",
        "blog_media",
        "speaking_recordings",
        "writer_applications",
      ].map((table) => [
        table,
        references.filter((r) => r.table === table).length,
      ]),
    ),
  };
  manifest.objects = objects.map(({ key, size, sha256, contentType }) => ({
    key,
    size,
    sha256,
    contentType,
  }));
  manifest.coordination = { count: redis.length, chain: zero };
  // Preserve exact raw Redis dump bytes in the private export. Runtime restoration uses typed values.
  const records = redis.map((record) => JSON.stringify(record));
  for (const line of records)
    manifest.coordination.chain = next(manifest.coordination.chain, line);
  const header = JSON.stringify(manifest);
  if (Buffer.byteLength(header) > 2 << 20)
    throw Error("Transfer manifest exceeds native record limit");
  const rows = Object.values(manifest.tables).reduce((n, t) => n + t.count, 0),
    budget = {
      maxNativeRowBytes,
      publicationRowsWrittenUpperBound: pgSchema.tables.reduce(
        (n, t) => n + manifest.tables[t.name].count * (1 + t.indexes.length),
        0,
      ),
      stagingRowsWrittenUpperBound: rows * 2,
    };
  return { manifest, objects, records, header, id: sha(header), budget };
}
export async function* body(directory, sealed) {
  yield sealed.header + "\n";
  for await (const chunk of createReadStream(resolve(directory, "rows.jsonl")))
    yield chunk;
  for (const line of sealed.records) yield line + "\n";
}
async function main() {
  const args = process.argv.slice(2),
    get = (flag) => {
      const i = args.indexOf(flag);
      return i < 0 ? undefined : args[i + 1];
    },
    directory = resolve(get("--directory") ?? ".");
  const root = resolve(
    dirname(fileURLToPath(import.meta.url)),
    "..",
    ".sensitive",
  );
  if (!directory.startsWith(root + "/"))
    throw Error("Select a private export under cloudflare/.sensitive/");
  const sealed = await seal(directory);
  try {
    await writeFile(
      resolve(directory, "sealed-manifest.json"),
      sealed.header + "\n",
      { mode: 0o600, flag: "wx" },
    );
  } catch (e) {
    if (
      e.code !== "EEXIST" ||
      (await privateFile(resolve(directory, "sealed-manifest.json"))).trim() !==
        sealed.header
    )
      throw e;
  }
  console.log(
    JSON.stringify({
      id: sealed.id,
      tables: Object.keys(sealed.manifest.tables).length,
      rows: Object.values(sealed.manifest.tables).reduce(
        (n, t) => n + t.count,
        0,
      ),
      objects: sealed.objects.length,
      coordination: sealed.records.length,
      budget: sealed.budget,
      mediaCoverage: sealed.manifest.mediaCoverage,
    }),
  );
  if (!args.includes("--execute")) return;
  const endpoint = get("--endpoint"),
    tokenFile = get("--token-file");
  if (!endpoint || !tokenFile)
    throw Error("--execute requires an endpoint and a private token file");
  const origin = new URL(endpoint);
  if (
    origin.protocol !== "https:" ||
    !origin.hostname.startsWith("ielts-migrate-dev.")
  )
    throw Error("Only the isolated DEV migration helper is accepted");
  const token = (await privateFile(resolve(tokenFile))).trim();
  if (token.length < 32) throw Error("Invalid migration token file");
  const authorization = "Bearer " + token;
  async function send(path, init) {
    const response = await fetch(new URL(path, origin), {
      ...init,
      headers: { authorization, ...init?.headers },
    });
    if (!response.ok)
      throw Error("DEV transfer rejected; source data unchanged");
    return response.json();
  }
  if (args.includes("--activate-only")) {
    await send("/activate", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ id: sealed.id }),
    });
    console.log("Verified DEV import activated");
    return;
  }
  for (const object of sealed.objects) {
    const status = await send("/object-status", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        key: object.key,
        size: object.size,
        sha256: object.sha256,
        contentType: object.contentType,
      }),
    });
    if (status.exists) continue;
    await send("/object", {
      method: "PUT",
      duplex: "half",
      headers: {
        "x-iac-key": Buffer.from(object.key).toString("base64"),
        "x-iac-sha256": object.sha256,
        "content-type": object.contentType,
        "content-length": String(object.size),
      },
      body: createReadStream(resolve(directory, "objects", object.file)),
    });
  }
  const report = await send("/database", {
    method: "POST",
    duplex: "half",
    headers: { "content-type": "application/x-ndjson" },
    body: Readable.from(body(directory, sealed)),
  });
  if (report.id !== sealed.id) throw Error("Import receipt mismatch");
  try {
    await writeFile(
      resolve(directory, "import-receipt.json"),
      JSON.stringify(report, null, 2) + "\n",
      { mode: 0o600, flag: "wx" },
    );
  } catch (e) {
    if (
      e.code !== "EEXIST" ||
      JSON.stringify(
        JSON.parse(
          await privateFile(resolve(directory, "import-receipt.json")),
        ),
      ) !== JSON.stringify(report)
    )
      throw e;
  }
  if (args.includes("--activate")) {
    await send("/activate", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ id: sealed.id }),
    });
    console.log("Verified DEV import activated");
  } else
    console.log(
      "Import verified and staged. API remains gated until separate activation.",
    );
}
if (process.argv[1] === fileURLToPath(import.meta.url))
  main().catch(() => {
    console.error(
      "DEV transfer stopped. Review private manifests and coverage; no credentials or records are logged.",
    );
    process.exitCode = 1;
  });
