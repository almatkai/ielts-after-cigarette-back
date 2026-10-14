export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    if (url.hostname !== "ielts.woolet.cc") {
      return new Response("Not found", { status: 404 });
    }
    if (url.protocol !== "https:") {
      url.protocol = "https:";
      return Response.redirect(url.toString(), 308);
    }
    // Replace caller-controlled proxy headers with Cloudflare's client address.
    const headers = new Headers(request.headers);
    headers.set("X-Forwarded-For", request.headers.get("CF-Connecting-IP") || "unknown");
    headers.set("X-Forwarded-Proto", "https");
    headers.delete("Forwarded");
    const target = new URL("http://127.0.0.1:18097");
    target.pathname = url.pathname;
    target.search = url.search;
    const response = await env.ORIGIN.fetch(new Request(target, {
      method: request.method,
      headers,
      body: request.body,
      redirect: "manual",
    }));
    const result = new Response(response.body, response);
    result.headers.set("X-Robots-Tag", "noindex, nofollow");
    result.headers.set("X-IAC-Environment", "development");
    return result;
  },
};
