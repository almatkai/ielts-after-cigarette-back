import { mediaEnvironment } from "../runtime/blob-store.mjs";
import {
  IACBlobIndex as Index,
  IACBlobChunks as Chunks,
} from "../runtime/blob-worker.mjs";
export class IACBlobIndex extends Index {
  async legacyProbe() {
    const id = crypto.randomUUID().replaceAll("-", ""),
      key = "legacy-layout",
      bytes = new Uint8Array(2 * 256 * 1024 + 17);
    for (let n = 0; n < bytes.length; n++) bytes[n] = n % 251;
    const fields = {
      httpMetadata: { contentType: "application/octet-stream" },
      customMetadata: {},
    };
    this.begin({ id, key, parent: null, fields });
    let count = 0;
    for (let at = 0; at < bytes.length; at += 256 * 1024) {
      const index = count++;
      const n = (parseInt(id.slice(0, 8), 16) + index) % 64;
      await this.env.IAC_BLOB_CHUNKS.get(
        this.env.IAC_BLOB_CHUNKS.idFromName(String(n)),
      ).write(id, index, bytes.slice(at, at + 256 * 1024));
    }
    const sha256 = Array.from(
      new Uint8Array(await crypto.subtle.digest("SHA-256", bytes)),
      (x) => x.toString(16).padStart(2, "0"),
    ).join("");
    const part = { id, size: bytes.length, count, sha256 };
    this.staged(id, part);
    this.publish(
      key,
      {
        ...fields,
        parts: [part],
        size: bytes.length,
        sha256,
        etag: sha256,
        uploaded: Date.now(),
        version: id,
      },
      null,
    );
    return { key, size: bytes.length, sha256 };
  }
  async sweep() {
    this.sql.exec("UPDATE uploads SET expires=0 WHERE expires IS NOT NULL");
    this.sql.exec("UPDATE multipart SET expires=0");
    for (let n = 0; n < 8; n++) await this.alarm();
  }
  async stats() {
    return {
      uploads: this.sql.exec("SELECT count(*) AS n FROM uploads").one().n,
      objects: this.sql.exec("SELECT count(*) AS n FROM objects").one().n,
      alarm: await this.ctx.storage.getAlarm(),
      expiring: this.sql
        .exec("SELECT count(*) AS n FROM uploads WHERE expires IS NOT NULL")
        .one().n,
    };
  }
}
export class IACBlobChunks extends Chunks {
  stats() {
    return this.sql.exec("SELECT bytes FROM usage WHERE id=1").one().bytes;
  }
  async orphanProbe() {
    const before = this.stats();
    this.write("0".repeat(32), 0, new Uint8Array(1024));
    this.sql.exec("UPDATE leases SET expires=0 WHERE upload=?", "0".repeat(32));
    await this.alarm();
    return { before, after: this.stats() };
  }
}

export default {
  async fetch(request, env) {
    const media = mediaEnvironment(env).MEDIA,
      url = new URL(request.url),
      key = url.searchParams.get("key");
    try {
      if (url.pathname === "/legacy")
        return Response.json(
          await env.IAC_BLOB_INDEX.get(
            env.IAC_BLOB_INDEX.idFromName("ielts-media-v1"),
          ).legacyProbe(),
        );
      if (url.pathname === "/orphan")
        return Response.json(
          await env.IAC_BLOB_CHUNKS.get(
            env.IAC_BLOB_CHUNKS.idFromName("0"),
          ).orphanProbe(),
        );
      if (url.pathname === "/debug") {
        const index = env.IAC_BLOB_INDEX.get(
          env.IAC_BLOB_INDEX.idFromName("ielts-media-v1"),
        );
        if (url.searchParams.has("sweep")) await index.sweep();
        let bytes = 0;
        for (let n = 0; n < 64; n++)
          bytes += await env.IAC_BLOB_CHUNKS.get(
            env.IAC_BLOB_CHUNKS.idFromName(String(n)),
          ).stats();
        return Response.json({ ...(await index.stats()), bytes });
      }
      if (url.pathname === "/failed-stream") {
        let delivered = false;
        const body = new ReadableStream({
          pull(controller) {
            if (delivered) controller.error(Error("upstream interrupted"));
            else {
              delivered = true;
              controller.enqueue(new Uint8Array(300000));
            }
          },
        });
        return Response.json(await media.put(key, body));
      }
      if (url.pathname === "/put")
        return Response.json(
          await media.put(key, request.body, {
            httpMetadata: {
              contentType:
                request.headers.get("content-type") ??
                "application/octet-stream",
            },
            sha256: url.searchParams.get("sha256") ?? undefined,
            onlyIf: url.searchParams.has("absent")
              ? { etagDoesNotMatch: "*" }
              : undefined,
          }),
        );
      if (url.pathname === "/get") {
        const object = await media.get(key, {
          range: url.searchParams.has("offset")
            ? {
                offset: Number(url.searchParams.get("offset")),
                length: Number(url.searchParams.get("length")),
              }
            : undefined,
        });
        return object
          ? new Response(object.body, {
              headers: {
                "x-checksum": Array.from(
                  new Uint8Array(object.checksums.sha256),
                  (x) => x.toString(16).padStart(2, "0"),
                ).join(""),
              },
            })
          : new Response(null, { status: 404 });
      }
      if (url.pathname === "/head") return Response.json(await media.head(key));
      if (url.pathname === "/list")
        return Response.json(
          await media.list(
            Object.fromEntries(
              [...url.searchParams].map(([k, v]) => [
                k,
                k === "limit" ? Number(v) : v,
              ]),
            ),
          ),
        );
      if (url.pathname === "/delete") {
        await media.delete(key);
        return new Response(null, { status: 204 });
      }
      const data = await request.json();
      if (url.pathname === "/start") {
        const upload = await media.createMultipartUpload(key, data);
        return Response.json({ uploadId: upload.uploadId });
      }
      const upload = media.resumeMultipartUpload(key, data.uploadId);
      if (url.pathname === "/part")
        return Response.json(
          await upload.uploadPart(
            data.partNumber,
            new TextEncoder().encode(data.body),
          ),
        );
      if (url.pathname === "/complete")
        return Response.json(await upload.complete(data.parts));
      if (url.pathname === "/abort") {
        await upload.abort();
        return new Response(null, { status: 204 });
      }
      return new Response(null, { status: 404 });
    } catch (error) {
      return new Response(error.message, { status: 409 });
    }
  },
};
