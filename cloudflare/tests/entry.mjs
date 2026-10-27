import worker, { IELTSDev } from "../runtime/worker.mjs";
import { env } from "cloudflare:workers";
const fetchOriginal = globalThis.fetch;
globalThis.fetch = (url, init) => {
  const parsed = new URL(typeof url === "string" ? url : url.url);
  if (parsed.hostname === "provider-contract.example.invalid")
    return fetchOriginal(env.CF_TEST_PROVIDER_URL, init);
  if (parsed.hostname === "stt-contract.example.invalid")
    return (async () => {
      const request = new Request(url, init),
        form = await request.formData(),
        file = form.get("file");
      if (
        !file ||
        !file.name.endsWith(".wav") ||
        new TextDecoder().decode((await file.arrayBuffer()).slice(0, 4)) !==
          "RIFF"
      )
        return Response.json(
          { error: "Invalid native WAV transfer" },
          { status: 422 },
        );
      return Response.json({
        text: "A synthetic native transcription.",
        duration: 2,
        segments: [
          {
            id: 0,
            start: 0,
            end: 2,
            text: "A synthetic native transcription.",
          },
        ],
      });
    })();
  if (parsed.hostname === "infobip-contract.example.invalid") {
    globalThis.syntheticCode = JSON.parse(
      new TextDecoder().decode(init.body),
    ).messages[0].content.templateData.body.placeholders[0];
    return Promise.resolve(Response.json({ messages: [] }));
  }
  if (parsed.hostname === "oauth2.googleapis.com")
    return Promise.resolve(
      Response.json(
        ["synthetic-google", "synthetic-waitlist"].includes(
          parsed.searchParams.get("id_token"),
        )
          ? {
              sub: parsed.searchParams.get("id_token") + "-id",
              email: parsed.searchParams.get("id_token") + "@example.invalid",
              email_verified: "true",
              aud: "synthetic-client",
              name: "Google contract",
            }
          : { error: "invalid" },
        {
          status: ["synthetic-google", "synthetic-waitlist"].includes(
            parsed.searchParams.get("id_token"),
          )
            ? 200
            : 400,
        },
      ),
    );
  if (parsed.hostname === "challenges.cloudflare.com")
    return Promise.resolve(
      Response.json({
        success: true,
        hostname: "web.test",
        action: "guest_mock",
      }),
    );
  return fetchOriginal(url, init);
};
export default worker;
export class IELTSDevTest extends IELTSDev {
  bridge(op, input, bytes, id) {
    this.bridgeCounts ??= {};
    this.bridgeCounts[op] = (this.bridgeCounts[op] ?? 0) + 1;
    return super.bridge(op, input, bytes, id);
  }
  async fetch(request) {
    if (new URL(request.url).pathname === "/__test") {
      const { sql, args = [] } = await request.json();
      if (sql === "__reset") {
        this.ctx.abort("Synthetic Durable Object reset");
        throw Error("Reset did not abort");
      }
      if (sql === "__metrics") return this.getMetrics();
      if (sql === "__code")
        return Response.json({ code: globalThis.syntheticCode });
      if (sql === "__configure") {
        await globalThis.iacInit(
          JSON.stringify({ ...this.settings, ...args[0] }),
        );
        return Response.json({ ok: true });
      }
      if (sql === "__alarm") {
        await this.alarm();
        return Response.json({ ok: true });
      }
      return this.serial(async () => {
        if (sql === "__routes")
          return Response.json(JSON.parse(globalThis.iacRoutes));
        if (sql === "__counts") return Response.json(this.bridgeCounts ?? {});
        if (sql === "__memory")
          return Response.json({
            bytes: this.wasmMemory?.buffer.byteLength ?? 0,
          });
        if (sql === "__bridge")
          return Response.json(JSON.parse(await this.bridge(args[0], args[1])));
        const c = this.ctx.storage.sql.exec(sql, ...args);
        return Response.json(c.toArray());
      });
    }
    return super.fetch(request);
  }
}

export { IELTSDev };
