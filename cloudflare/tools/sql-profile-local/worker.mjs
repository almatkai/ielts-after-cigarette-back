// Operator-only local bridge. Wrangler authenticates the remote service binding.
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
    return Response.json(await env.DIAGNOSTICS.profile(), {
      headers: { "cache-control": "no-store" },
    });
  },
};
