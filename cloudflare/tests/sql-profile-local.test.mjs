import { test } from "node:test";
import assert from "node:assert/strict";
import worker from "../tools/sql-profile-local/worker.mjs";

test("operator SQL profile bridge accepts only literal loopback reads", async () => {
  let reads = 0;
  const env = {
    DIAGNOSTICS: {
      async profile() {
        reads++;
        return { enabled: true, queries: [] };
      },
    },
  };
  const response = await worker.fetch(
    new Request("http://127.0.0.1:8796/profile"),
    env,
  );
  assert.equal(response.status, 200);
  assert.equal(response.headers.get("cache-control"), "no-store");
  assert.deepEqual(await response.json(), { enabled: true, queries: [] });
  for (const [url, options, status] of [
    ["https://public.example/profile", {}, 403],
    ["http://localhost:8796/profile", {}, 403],
    [
      "http://127.0.0.1:8796/profile",
      { headers: { origin: "https://foreign.example" } },
      403,
    ],
    [
      "http://127.0.0.1:8796/profile",
      { headers: { "sec-fetch-site": "cross-site" } },
      403,
    ],
    ["http://127.0.0.1:8796/profile", { method: "POST" }, 404],
    ["http://127.0.0.1:8796/elsewhere", {}, 404],
  ])
    assert.equal(
      (await worker.fetch(new Request(url, options), env)).status,
      status,
    );
  assert.equal(reads, 1);
});
