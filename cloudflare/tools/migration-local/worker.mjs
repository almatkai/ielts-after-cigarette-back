// Local capability bridge only. Wrangler binds 127.0.0.1 and authenticates its
// remote service connection with the existing Cloudflare account OAuth session.
export default {
  fetch(request, env) {
    const url = new URL(request.url),
      origin = request.headers.get("origin"),
      site = request.headers.get("sec-fetch-site");
    if (
      url.hostname !== "127.0.0.1" ||
      url.protocol !== "http:" ||
      (origin !== null && origin !== url.origin) ||
      (site !== null && !["same-origin", "none"].includes(site))
    )
      return new Response("Forbidden", { status: 403 });
    return env.MIGRATION.transfer(request);
  },
};
