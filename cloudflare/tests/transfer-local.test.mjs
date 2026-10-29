import { test } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { once } from "node:events";
import { promisify } from "node:util";
import { execFile } from "node:child_process";
import { mkdir, writeFile, rm } from "node:fs/promises";
import { randomUUID, createHash } from "node:crypto";
import { fileURLToPath } from "node:url";
import source from "../schema/postgres.json" with { type: "json" };
import { bindingLocalOrigin } from "../tools/transfer.mjs";
const exec = promisify(execFile),
  root = fileURLToPath(new URL("..", import.meta.url));
test("transfer execution accepts only explicit literal loopback binding mode without tokens", () => {
  for (const endpoint of [
    "http://127.0.0.1:8794",
    "http://127.0.0.1:65535/",
    "http://127.0.0.1:80",
  ])
    assert.equal(
      bindingLocalOrigin(["--binding-local", "--endpoint", endpoint]).hostname,
      "127.0.0.1",
    );
  for (const endpoint of [
    undefined,
    "http://localhost:8794",
    "https://127.0.0.1:8794",
    "http://127.1:8794",
    "http://2130706433:8794",
    "http://0x7f000001:8794",
    "http://0177.0.0.1:8794",
    "http://127.0.0.1:0",
    "http://127.0.0.1:65536",
    "http://127.0.0.1:08794",
    "http://127.0.0.1",
    "http://127.0.0.1:8794/path",
    "http://127.0.0.1:8794?query",
    "http://127.0.0.1:8794#fragment",
    "http://user@127.0.0.1:8794",
    "http://127.0.0.1:8794@evil.invalid",
    "http://[::1]:8794",
    "http://127%2e0%2e0%2e1:8794",
    "http://127.0.0.1:8794\n",
    " http://127.0.0.1:8794",
  ])
    assert.throws(() =>
      bindingLocalOrigin(["--binding-local", "--endpoint", endpoint]),
    );
  assert.throws(() =>
    bindingLocalOrigin(["--endpoint", "http://127.0.0.1:8794"]),
  );
  assert.throws(() =>
    bindingLocalOrigin([
      "--binding-local",
      "--endpoint",
      "http://127.0.0.1:8794",
      "--token-file",
      "unread",
    ]),
  );
});
async function fixture() {
  const directory = root + "/.sensitive/private-binding-" + randomUUID();
  await mkdir(directory, { recursive: true, mode: 0o700 });
  const manifest = {
    format: "iac-cloudflare-dev-v1",
    revision: source.revision,
    tables: Object.fromEntries(
      source.tables.map((t) => [
        t.name,
        {
          columns: t.columns.map((c) => c.name),
          count: 0,
          chain: "0".repeat(64),
        },
      ]),
    ),
    ledger: [],
    sequences: [],
  };
  for (const [name, data] of [
    ["database.json", JSON.stringify(manifest)],
    ["rows.jsonl", ""],
    ["objects.json", "[]"],
    ["redis.json", "[]"],
  ])
    await writeFile(directory + "/" + name, data, { mode: 0o600 });
  return directory;
}
async function listen(handler) {
  const server = createServer(handler);
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  return { server, endpoint: "http://127.0.0.1:" + server.address().port };
}
function invoke(directory, endpoint) {
  return exec(
    process.execPath,
    [
      "tools/transfer.mjs",
      "--directory",
      directory,
      "--execute",
      "--binding-local",
      "--endpoint",
      endpoint,
    ],
    { cwd: root },
  );
}
test("CLI sends sealed database to local capability connection without authorization header", async () => {
  const directory = await fixture();
  let requests = 0;
  const { server, endpoint } = await listen(async (request, response) => {
    requests++;
    assert.equal(request.headers.authorization, undefined);
    assert.equal(request.url, "/database");
    let payload = "";
    for await (const chunk of request) payload += chunk;
    const [header, ...rows] = payload.trimEnd().split("\n");
    assert.deepEqual(rows, []);
    const manifest = JSON.parse(header);
    assert.equal(
      Object.keys(manifest.tables).length,
      Object.keys(source.tables).length,
    );
    response.setHeader("content-type", "application/json");
    response.end(
      JSON.stringify({ id: createHash("sha256").update(header).digest("hex") }),
    );
  });
  try {
    const result = await invoke(directory, endpoint);
    assert.match(result.stdout, /Import verified and staged/);
    assert.equal(requests, 1);
  } finally {
    server.closeAllConnections();
    await new Promise((resolve) => server.close(resolve));
    await rm(directory, { recursive: true, force: true });
  }
});
test("CLI never follows redirect away from its literal loopback connection", async () => {
  const directory = await fixture();
  let leaked = 0;
  const target = await listen((request, response) => {
    leaked++;
    response.end("{}");
  });
  const redirect = await listen((request, response) => {
    request.resume();
    response.writeHead(307, { location: target.endpoint + "/stolen" });
    response.end();
  });
  try {
    await assert.rejects(invoke(directory, redirect.endpoint));
    assert.equal(leaked, 0);
  } finally {
    for (const { server } of [redirect, target]) {
      server.closeAllConnections();
      await new Promise((resolve) => server.close(resolve));
    }
    await rm(directory, { recursive: true, force: true });
  }
});
