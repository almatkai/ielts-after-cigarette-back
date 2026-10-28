import { mediaEnvironment } from "../runtime/blob-store.mjs";
import { WorkerEntrypoint } from "cloudflare:workers";
// No public migration HTTP endpoint. Authority is the account-owned service
// binding to this named HTTP entrypoint (locally reached via Wrangler OAuth).
export default {
  fetch() {
    return new Response("Not found", { status: 404 });
  },
};
export class MigrationBridge extends WorkerEntrypoint {
  fetch(request) {
    // Named fetch is reachable only through an explicit account service binding.
    // Use native HTTP streaming, avoiding remote RPC Request body translation.
    return transfer(request, this.env);
  }
}
async function transfer(request, env) {
  env = mediaEnvironment(env);
  const path = new URL(request.url).pathname,
    stub = env.IAC_STATE.get(env.IAC_STATE.idFromName("ielts-dev-v1"));
  try {
    if (request.method === "POST" && path === "/object-status") {
      const expected = await request.json();
      if (
        typeof expected.key !== "string" ||
        !expected.key ||
        expected.key.startsWith("_cf-temp/") ||
        typeof expected.contentType !== "string" ||
        !expected.contentType ||
        !Number.isSafeInteger(expected.size) ||
        expected.size < 0 ||
        !/^[0-9a-f]{64}$/.test(expected.sha256)
      )
        return new Response("Invalid object manifest", { status: 422 });
      const object = await env.MEDIA.head(expected.key);
      if (!object) return Response.json({ exists: false });
      const checksum = object.checksums.sha256
        ? Array.from(new Uint8Array(object.checksums.sha256), (x) =>
            x.toString(16).padStart(2, "0"),
          ).join("")
        : null;
      if (
        object.size !== expected.size ||
        checksum !== expected.sha256 ||
        object.httpMetadata?.contentType !== expected.contentType
      )
        return new Response("Existing object differs", { status: 409 });
      return Response.json({ exists: true });
    }
    if (request.method === "POST" && path === "/database")
      return Response.json(await stub.importData(request));
    if (request.method === "POST" && path === "/activate")
      return Response.json(await stub.activateData((await request.json()).id));
    if (request.method === "GET" && path === "/report")
      return Response.json(await stub.importReport());
    if (request.method === "PUT" && path === "/object") {
      const key = new TextDecoder().decode(
          Uint8Array.from(atob(request.headers.get("x-iac-key") ?? ""), (c) =>
            c.charCodeAt(0),
          ),
        ),
        sha256 = request.headers.get("x-iac-sha256");
      if (
        !key ||
        key.startsWith("_cf-temp/") ||
        !/^[0-9a-f]{64}$/.test(sha256 ?? "")
      )
        return new Response("Invalid object manifest", { status: 422 });
      const size = Number(request.headers.get("content-length"));
      if (!Number.isSafeInteger(size) || size < 0)
        return new Response("Content-Length required", { status: 411 });
      const previous = await env.MEDIA.head(key);
      if (previous) {
        const checksum = previous.checksums.sha256
          ? Array.from(new Uint8Array(previous.checksums.sha256), (x) =>
              x.toString(16).padStart(2, "0"),
            ).join("")
          : null;
        if (
          previous.size === size &&
          checksum === sha256 &&
          previous.httpMetadata?.contentType ===
            (request.headers.get("content-type") ?? "application/octet-stream")
        ) {
          if (request.body)
            await request.body.pipeTo(new WritableStream({ write() {} }));
          return Response.json({ key, size, sha256 });
        }
        return new Response("Existing object differs", { status: 409 });
      }
      let body = new Uint8Array(0),
        pipe = Promise.resolve();
      if (size) {
        const fixed = new FixedLengthStream(size);
        pipe = request.body.pipeTo(fixed.writable);
        body = fixed.readable;
      }
      const [object] = await Promise.all([
        env.MEDIA.put(key, body, {
          sha256,
          httpMetadata: {
            contentType:
              request.headers.get("content-type") ?? "application/octet-stream",
          },
        }),
        pipe,
      ]);
      return Response.json({ key, size: object.size, sha256 });
    }
    return new Response("Not found", { status: 404 });
  } catch (e) {
    if (env.CF_TEST_MODE === "true") console.error(e.message);
    return Response.json({ code: "TRANSFER_REJECTED" }, { status: 409 });
  }
}
