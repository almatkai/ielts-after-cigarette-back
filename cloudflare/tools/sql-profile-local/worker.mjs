// Operator-only preview bridge. Run `wrangler dev --remote`; never deploy it.
export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    const origin = request.headers.get("origin");
    const site = request.headers.get("sec-fetch-site");
    if (
      url.protocol !== "http:" ||
      url.hostname !== "127.0.0.1" ||
      (origin !== null && origin !== url.origin) ||
      (site !== null && !["same-origin", "none"].includes(site))
    ) {
      return new Response("Forbidden", { status: 403 });
    }
    if (request.method !== "GET" || url.pathname !== "/profile") {
      return new Response("Not found", { status: 404 });
    }
    const stub = env.IAC_STATE.get(env.IAC_STATE.idFromName("ielts-dev-v1"));
    return Response.json(await stub.getSqlReadProfile(), {
      headers: { "cache-control": "no-store" },
    });
  },
};
