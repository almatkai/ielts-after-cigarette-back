// Private, bounded object storage on SQLite Durable Objects. No R2 subscription.
// This implements the application-used object contract, not the entire R2 API.
const CHUNK = 1024 * 1024,
  MAX = 512 * 1024 * 1024;
export const BLOB_SHARDS = 64;
const hex = (bytes) =>
  [...new Uint8Array(bytes)]
    .map((x) => x.toString(16).padStart(2, "0"))
    .join("");
const unhex = (s) =>
  Uint8Array.from(s.match(/../g), (x) => parseInt(x, 16)).buffer;
const stub = (ns, name) => ns.get(ns.idFromName(name));
const shard = (id, index) =>
  (parseInt(id.slice(0, 8), 16) + index) % BLOB_SHARDS;
function keyCheck(key) {
  if (
    typeof key !== "string" ||
    !key ||
    new TextEncoder().encode(key).length > 1024
  )
    throw Error("Invalid object key");
}
function optionsCheck(options, allowed) {
  for (const key of Object.keys(options))
    if (!allowed.includes(key))
      throw Error("Unsupported object option: " + key);
}
function precondition(meta, onlyIf) {
  if (!onlyIf) return true;
  if (onlyIf instanceof Headers)
    throw Error("Header conditions are not supported");
  optionsCheck(onlyIf, [
    "etagMatches",
    "etagDoesNotMatch",
    "uploadedBefore",
    "uploadedAfter",
  ]);
  if (
    onlyIf.etagMatches !== undefined &&
    !(onlyIf.etagMatches === "*"
      ? meta
      : meta?.etag === onlyIf.etagMatches.replaceAll('"', ""))
  )
    return false;
  if (
    onlyIf.etagDoesNotMatch !== undefined &&
    (onlyIf.etagDoesNotMatch === "*"
      ? meta
      : meta?.etag === onlyIf.etagDoesNotMatch.replaceAll('"', ""))
  )
    return false;
  if (
    onlyIf.uploadedBefore &&
    (!meta || meta.uploaded >= +new Date(onlyIf.uploadedBefore))
  )
    return false;
  if (
    onlyIf.uploadedAfter &&
    (!meta || meta.uploaded <= +new Date(onlyIf.uploadedAfter))
  )
    return false;
  return true;
}
function metadata(options) {
  optionsCheck(options, ["httpMetadata", "customMetadata", "sha256", "onlyIf"]);
  if (options.httpMetadata instanceof Headers)
    throw Error("Header metadata is unsupported");
  const httpMetadata = { ...(options.httpMetadata ?? {}) },
    customMetadata = { ...(options.customMetadata ?? {}) };
  optionsCheck(httpMetadata, [
    "contentType",
    "contentLanguage",
    "contentDisposition",
    "contentEncoding",
    "cacheControl",
    "cacheExpiry",
  ]);
  for (const [key, value] of Object.entries(customMetadata))
    if (typeof value !== "string")
      throw Error("Custom metadata must be strings");
  if (JSON.stringify({ httpMetadata, customMetadata }).length > 8192)
    throw Error("Object metadata too large");
  return { httpMetadata, customMetadata };
}
export function mediaEnvironment(env) {
  return env.IAC_BLOB_INDEX
    ? {
        ...env,
        MEDIA: new SQLiteBucket(env.IAC_BLOB_INDEX, env.IAC_BLOB_CHUNKS),
      }
    : env;
}
export class SQLiteBucket {
  constructor(index, chunks, localIndex = null) {
    this.index = localIndex ?? stub(index, "ielts-media-v1");
    this.chunks = chunks;
    this.localIndex = localIndex;
  }
  async stage(key, value, options, parent = null) {
    keyCheck(key);
    const fields = metadata(options),
      id = crypto.randomUUID().replaceAll("-", "");
    await this.index.begin({ id, key, parent, fields });
    const digest = new crypto.DigestStream("SHA-256"),
      writer = digest.getWriter();
    // A failed upstream stream can abort before the digest is awaited.
    digest.digest.catch(() => {});
    let size = 0,
      count = 0,
      buffer = new Uint8Array(CHUNK),
      used = 0;
    const input =
      value instanceof ReadableStream ? value : new Response(value).body;
    const reader = input?.getReader();
    const flush = async () => {
      if (!used) return;
      const bytes = buffer.slice(0, used);
      await stub(this.chunks, String(shard(id, count))).write(id, count, bytes);
      count++;
      used = 0;
    };
    try {
      for (;;) {
        const item = reader ? await reader.read() : { done: true };
        if (item.done) break;
        const bytes = item.value;
        if (!(bytes instanceof Uint8Array)) throw Error("Expected byte stream");
        size += bytes.length;
        if (size > MAX) throw Error("Object exceeds 512 MiB limit");
        await writer.write(bytes);
        for (let offset = 0; offset < bytes.length;) {
          const n = Math.min(CHUNK - used, bytes.length - offset);
          buffer.set(bytes.subarray(offset, offset + n), used);
          used += n;
          offset += n;
          if (used === CHUNK) await flush();
        }
      }
      await flush();
      await writer.close();
      const sha256 = hex(await digest.digest);
      const expected =
        typeof options.sha256 === "string"
          ? options.sha256.toLowerCase()
          : options.sha256
            ? hex(options.sha256)
            : null;
      if (expected && sha256 !== expected)
        throw Error("Object SHA-256 mismatch");
      const part = { id, size, count, chunkSize: CHUNK, sha256 };
      await this.index.staged(id, part);
      return {
        ...fields,
        parts: [part],
        size,
        sha256,
        etag: sha256,
        uploaded: Date.now(),
        version: id,
      };
    } catch (error) {
      await writer.abort().catch(() => {});
      await this.index.discard(id).catch(() => {});
      throw error;
    } finally {
      await reader?.cancel().catch(() => {});
    }
  }
  async put(key, value, options = {}) {
    if (!this.localIndex) {
      keyCheck(key);
      metadata(options);
      const stream =
        value instanceof ReadableStream ? value : new Response(value).body;
      const meta = await this.index.ingest(key, stream, options);
      return meta ? this.object(meta, false) : null;
    }
    const meta = await this.stage(key, value, options);
    const committed = await this.index.publish(
      key,
      meta,
      options.onlyIf ?? null,
    );
    return committed ? this.object(committed, false) : null;
  }
  async head(key) {
    keyCheck(key);
    const meta = await this.index.head(key);
    return meta ? this.object(meta, false) : null;
  }
  async get(key, options = {}) {
    keyCheck(key);
    optionsCheck(options, ["range", "onlyIf"]);
    const meta = await this.index.head(key);
    if (!meta) return null;
    if (!precondition(meta, options.onlyIf)) return this.object(meta, false);
    return this.object(meta, true, options.range);
  }
  object(meta, body, range) {
    const object = {
      key: meta.key,
      size: meta.size,
      version: meta.version,
      etag: meta.etag,
      httpEtag: '"' + meta.etag + '"',
      uploaded: new Date(meta.uploaded),
      httpMetadata: { ...meta.httpMetadata },
      customMetadata: { ...meta.customMetadata },
      checksums: { sha256: unhex(meta.sha256) },
      writeHttpMetadata(headers) {
        for (const [k, v] of Object.entries(meta.httpMetadata)) {
          const names = {
            contentType: "content-type",
            contentLanguage: "content-language",
            contentDisposition: "content-disposition",
            contentEncoding: "content-encoding",
            cacheControl: "cache-control",
            cacheExpiry: "expires",
          };
          headers.set(
            names[k],
            k === "cacheExpiry" ? new Date(v).toUTCString() : v,
          );
        }
      },
    };
    if (!body) return object;
    let offset = 0,
      length = meta.size;
    if (range) {
      if (range instanceof Headers)
        throw Error("Header ranges are not supported");
      optionsCheck(range, ["offset", "length", "suffix"]);
      if (range.suffix !== undefined) {
        if (
          range.offset !== undefined ||
          range.length !== undefined ||
          !Number.isSafeInteger(range.suffix) ||
          range.suffix <= 0
        )
          throw Error("Invalid suffix range");
        offset = Math.max(0, meta.size - range.suffix);
      } else {
        offset = range.offset ?? 0;
        if (
          !Number.isSafeInteger(offset) ||
          offset < 0 ||
          (meta.size ? offset >= meta.size : offset !== 0)
        )
          throw Error("Invalid object range");
        if (
          range.length !== undefined &&
          (!Number.isSafeInteger(range.length) || range.length < 0)
        )
          throw Error("Invalid range length");
      }
      length = Math.min(range.length ?? meta.size - offset, meta.size - offset);
      object.range = { offset, length };
    }
    const chunks = this.chunks;
    let partIndex = 0,
      partStart = 0,
      position = offset,
      remaining = length;
    object.body = new ReadableStream({
      async pull(controller) {
        if (!remaining) {
          controller.close();
          return;
        }
        while (
          partIndex < meta.parts.length &&
          position >= partStart + meta.parts[partIndex].size
        ) {
          partStart += meta.parts[partIndex++].size;
        }
        const part = meta.parts[partIndex],
          relative = position - partStart,
          chunkSize = part.chunkSize ?? 256 * 1024,
          index = Math.floor(relative / chunkSize);
        const bytes = new Uint8Array(
          await stub(chunks, String(shard(part.id, index))).read(
            part.id,
            index,
          ),
        );
        const at = relative % chunkSize,
          count = Math.min(remaining, bytes.length - at);
        if (count <= 0) throw Error("Stored chunk length mismatch");
        controller.enqueue(bytes.slice(at, at + count));
        position += count;
        remaining -= count;
      },
    });
    object.arrayBuffer = () => new Response(object.body).arrayBuffer();
    object.text = () => new Response(object.body).text();
    object.json = async () => JSON.parse(await object.text());
    return object;
  }
  async delete(keys) {
    for (const key of Array.isArray(keys) ? keys : [keys]) {
      keyCheck(key);
      await this.index.remove(key);
    }
  }
  async list(options = {}) {
    optionsCheck(options, ["prefix", "cursor", "limit", "include"]);
    if (
      options.include?.some(
        (x) => !["httpMetadata", "customMetadata"].includes(x),
      )
    )
      throw Error("Unsupported list include");
    const page = await this.index.list(options);
    return {
      ...page,
      objects: page.objects.map((meta) => this.object(meta, false)),
      delimitedPrefixes: [],
    };
  }
  async createMultipartUpload(key, options = {}) {
    keyCheck(key);
    const fields = metadata(options);
    if (options.sha256 || options.onlyIf)
      throw Error("Multipart conditions/checksum are unsupported");
    const uploadId = crypto.randomUUID().replaceAll("-", "");
    await this.index.multipart({ id: uploadId, key, fields });
    return this.resumeMultipartUpload(key, uploadId);
  }
  resumeMultipartUpload(key, uploadId) {
    keyCheck(key);
    const bucket = this;
    return {
      key,
      uploadId,
      async uploadPart(partNumber, value) {
        if (
          !Number.isInteger(partNumber) ||
          partNumber < 1 ||
          partNumber > 10000
        )
          throw Error("Invalid multipart part number");
        const upload = await bucket.index.upload(key, uploadId);
        const part = await bucket.stage(key, value, upload.fields, uploadId);
        await bucket.index.part(uploadId, partNumber, part);
        return { partNumber, etag: part.etag };
      },
      async complete(parts) {
        const upload = await bucket.index.completion(key, uploadId, parts);
        const meta = {
          ...upload.fields,
          key,
          parts: upload.parts.flatMap((x) => x.parts),
          size: upload.parts.reduce((n, x) => n + x.size, 0),
          version: uploadId,
          uploaded: Date.now(),
        };
        if (meta.size > MAX) throw Error("Object exceeds 512 MiB limit");
        const digest = new crypto.DigestStream("SHA-256");
        await bucket
          .object({ ...meta, sha256: "0".repeat(64) }, true)
          .body.pipeTo(digest);
        meta.sha256 = meta.etag = hex(await digest.digest);
        return bucket.object(
          await bucket.index.finishMultipart(key, uploadId, parts, meta),
          false,
        );
      },
      abort: () => bucket.index.discard(uploadId),
    };
  }
}
export { CHUNK, MAX, precondition };
