import { blobBindings, blobWorker, testBucket } from "./blob-options.mjs";
import { fileURLToPath } from "node:url";
process.chdir(fileURLToPath(new URL("..", import.meta.url)));
import { test, after } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readdir, readFile, mkdir, writeFile, rm } from "node:fs/promises";
import { createHash, createHmac, randomUUID } from "node:crypto";
import { seal, body as transferBody } from "../tools/transfer.mjs";
import { Miniflare, convertV4MiniflareOptions } from "miniflare";
import { PGlite } from "@electric-sql/pglite";
import source from "../schema/postgres.json" with { type: "json" };
const env = {
  ...process.env,
  WRANGLER_SEND_METRICS: "false",
  CLOUDFLARE_LOAD_DEV_VARS_FROM_DOT_ENV: "false",
};
for (const [config, output] of [
  ["wrangler.jsonc", ".artifacts/transfer-api"],
  ["wrangler.migration.jsonc", ".artifacts/transfer-helper"],
])
  execFileSync(
    process.execPath,
    [
      "node_modules/wrangler/bin/wrangler.js",
      "deploy",
      "--dry-run",
      "--config",
      config,
      "--outdir",
      output,
    ],
    { env, stdio: "pipe" },
  );
const modules = async (dir, entry) => [
  { type: "ESModule", path: dir + "/" + entry },
  ...(await readdir(dir))
    .filter((n) => n.endsWith(".wasm"))
    .map((n) => ({ type: "CompiledWasm", path: dir + "/" + n })),
];
const namespace = { className: "IELTSDev", useSQLite: true };
const mf = new Miniflare(
  convertV4MiniflareOptions({
    workers: [
      {
        name: "ielts-api-dev",
        modules: await modules(".artifacts/transfer-api", "worker.js"),
        modulesRoot: ".artifacts/transfer-api",
        compatibilityDate: "2026-10-19",
        durableObjects: { ...blobBindings, IAC_STATE: namespace },

        bindings: {
          JWT_SECRET: "synthetic-contract-test-secret-32-bytes",
          PHONE_VERIFICATION_SECRET: "synthetic-phone-test-secret-32-bytes",
          IAC_REQUIRE_IMPORT: "true",
        },
      },
      {
        name: "ielts-migrate-dev",
        modules: await modules(
          ".artifacts/transfer-helper",
          "migration-worker.js",
        ),
        modulesRoot: ".artifacts/transfer-helper",
        compatibilityDate: "2026-10-19",
        durableObjects: {
          ...blobBindings,
          IAC_STATE: { ...namespace, scriptName: "ielts-api-dev" },
        },

        bindings: { CF_TEST_MODE: "true" },
      },
      {
        name: "ielts-migrate-local",
        modules: [
          { type: "ESModule", path: "tools/migration-local/worker.mjs" },
        ],
        compatibilityDate: "2026-10-19",
        serviceBindings: {
          MIGRATION: {
            name: "ielts-migrate-dev",
            entrypoint: "MigrationBridge",
          },
        },
      },
      {
        name: "named-http-probe",
        modules: [
          {
            type: "ESModule",
            path: "named-http-probe.mjs",
            contents:
              "export default {fetch(request,env){return env.MIGRATION.fetch(request);}}",
          },
        ],
        compatibilityDate: "2026-10-19",
        serviceBindings: {
          MIGRATION: {
            name: "ielts-migrate-dev",
            entrypoint: "MigrationBridge",
          },
        },
      },
      blobWorker(".artifacts/transfer-blob"),
    ],
  }),
);
after(() => mf.dispose());
const hash = (x) => createHash("sha256").update(x).digest("hex"),
  zero = "0".repeat(64);
const roll = (previous, line) =>
  hash(
    Buffer.concat([
      Buffer.from(previous, "hex"),
      Buffer.from(hash(line), "hex"),
    ]),
  );
const pg = new PGlite();
for (const migration of source.migrations)
  await pg.exec(
    await readFile(
      `.artifacts/${source.revision}/source/migrations/${migration.file}`,
      "utf8",
    ),
  );
await pg.exec(
  `INSERT INTO users(id,email,role,status,terms_accepted_at) VALUES('11111111-1111-4111-8111-111111111111','contract@example.invalid','STUDENT','REGISTERED','2026-10-01T00:00:00Z')`,
);
await pg.exec(
  `INSERT INTO user_profiles(user_id,display_name,timezone,target_band) VALUES('11111111-1111-4111-8111-111111111111','Контракт café','Asia/Almaty',7.5); INSERT INTO refresh_sessions(id,user_id,token_hash,expires_at,user_agent) VALUES('22222222-2222-4222-8222-222222222222','11111111-1111-4111-8111-111111111111',decode('0001feff','hex'),'2099-01-01T00:00:00.123456Z','contract'); INSERT INTO ai_providers(id,name,endpoint,model,key_ciphertext) VALUES('33333333-3333-4333-8333-333333333333','Contract','https://provider.example.invalid/v1/chat/completions','contract',decode(repeat('00ff',32),'hex'));`,
);
await pg.exec(
  `CREATE TABLE schema_migrations(version bigint NOT NULL PRIMARY KEY,dirty boolean NOT NULL); INSERT INTO schema_migrations VALUES(39,false);UPDATE refresh_sessions SET token_hash=decode('${hash("synthetic-import-refresh")}','hex');SELECT setval('study_learning_content_reviews_id_seq',77,true);`,
);
const directory = ".sensitive/contract-" + randomUUID();
await mkdir(directory, { recursive: true, mode: 0o700 });
after(() => rm(directory, { recursive: true, force: true }));
const query = execFileSync(
  "python3",
  [
    "-c",
    "import importlib.util,json;spec=importlib.util.spec_from_file_location('exporter','tools/export-dev.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);print(m.export_sql(json.load(open('schema/postgres.json'))))",
  ],
  { encoding: "utf8" },
);
const outputs = await pg.exec(query),
  raw =
    outputs
      .flatMap((o) => o.rows ?? [])
      .map((row) => JSON.stringify(row.json_build_object))
      .join("\n") + "\n";
await pg.close();
await writeFile(directory + "/postgres.raw.jsonl", raw, { mode: 0o600 });
execFileSync(
  "python3",
  [
    "-c",
    "import importlib.util,json,pathlib,sys;spec=importlib.util.spec_from_file_location('exporter','tools/export-dev.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);p=pathlib.Path(sys.argv[1]);m.finish_database(p/'postgres.raw.jsonl',p,json.load(open('schema/postgres.json')))",
    directory,
  ],
  { stdio: "pipe" },
);
await mkdir(directory + "/objects", { mode: 0o700 });
const object = {
  key: "_migration/existing-source/fixture.txt",
  size: 7,
  sha256: hash("fixture"),
  contentType: "text/plain",
  file: hash("_migration/existing-source/fixture.txt"),
};
await writeFile(directory + "/objects/" + object.file, "fixture", {
  mode: 0o600,
});
await writeFile(directory + "/objects.json", JSON.stringify([object]), {
  mode: 0o600,
});
await writeFile(
  directory + "/redis.json",
  JSON.stringify([
    {
      kind: "redis",
      key: "rate-limit:public:login:127.0.0.1",
      type: "string",
      value: "100000",
      expires: Date.now() + 3600000,
      dump: "AA==",
    },
  ]),
  { mode: 0o600 },
);
const sealed = await seal(directory),
  manifest = sealed.manifest,
  header = sealed.header;
let payload = "";
for await (const chunk of transferBody(directory, sealed)) payload += chunk;
const records = payload.trimEnd().split("\n").slice(1);
assert.equal((await seal(directory)).id, sealed.id);

async function helper(path, init = {}) {
  return (await mf.getWorker("ielts-migrate-local")).fetch(
    "http://127.0.0.1:8794" + path,
    init,
  );
}
async function api() {
  return (await mf.getWorker("ielts-api-dev")).fetch(
    "https://api.test/health/ready",
  );
}
test("real DO import verifies hashes, rolls back corruption, preserves data and activates only after private SQLite blob checks", async () => {
  assert.equal((await api()).status, 503);
  const denied = await (
    await mf.getWorker("ielts-migrate-dev")
  ).fetch("https://transfer.test/report");
  assert.equal(denied.status, 404);
  const namedHTTP = await (
    await mf.getWorker("named-http-probe")
  ).fetch("https://transfer.test/report");
  assert.equal(namedHTTP.status, 404);
  for (const [path, method] of [
    ["/report", "GET"],
    ["/database", "POST"],
    ["/activate", "POST"],
    ["/object", "PUT"],
    ["/object-status", "POST"],
  ]) {
    const response = await (
      await mf.getWorker("ielts-migrate-dev")
    ).fetch("https://transfer.test" + path, {
      method,
      body: method === "GET" ? undefined : "{}",
      headers: { authorization: "Bearer ignored-public-header" },
    });
    assert.equal(response.status, 404);
  }
  for (const headers of [
    { origin: "https://untrusted.invalid" },
    { origin: "null" },
    { "sec-fetch-site": "cross-site" },
  ]) {
    const response = await helper("/database", {
      method: "POST",
      body: payload,
      headers,
    });
    assert.equal(response.status, 403);
  }
  const reserved = await helper("/object", {
    method: "PUT",
    headers: {
      "x-iac-key": Buffer.from("_cf-temp/derived-object").toString("base64"),
      "x-iac-sha256": hash("fixture"),
      "content-length": "7",
    },
    body: "fixture",
  });
  assert.equal(reserved.status, 422);
  await reserved.text();
  let response = await helper("/database", {
    method: "POST",
    body: header + "\n" + records.slice(1).join("\n") + "\n",
  });
  assert.equal(response.status, 409);
  response = await helper("/activate", {
    method: "POST",
    body: JSON.stringify({ id: hash(header) }),
  });
  assert.equal(response.status, 409);
  response = await helper("/database", { method: "POST", body: payload });
  assert.equal(response.status, 200, await response.clone().text());
  const receipt = await response.json();
  assert.equal(receipt.id, hash(header));
  assert.equal(receipt.tables.users.count, 1);
  assert.equal(receipt.coordination.restored, 1);
  assert.equal((await api()).status, 503);
  response = await helper("/activate", {
    method: "POST",
    body: JSON.stringify({ id: receipt.id }),
  });
  assert.equal(response.status, 409);
  const absent = await helper("/object-status", {
    method: "POST",
    body: JSON.stringify({
      key: "_migration/existing-source/fixture.txt",
      size: 7,
      sha256: hash("fixture"),
      contentType: "text/plain",
    }),
  });
  assert.equal((await absent.json()).exists, false);
  response = await helper("/object", {
    method: "PUT",
    headers: {
      "x-iac-key": Buffer.from(
        "_migration/existing-source/fixture.txt",
      ).toString("base64"),
      "x-iac-sha256": hash("fixture"),
      "content-type": "text/plain",
      "content-length": "7",
    },
    body: "fixture",
  });
  assert.equal(response.status, 200);
  const present = await helper("/object-status", {
    method: "POST",
    body: JSON.stringify({
      key: "_migration/existing-source/fixture.txt",
      size: 7,
      sha256: hash("fixture"),
      contentType: "text/plain",
    }),
  });
  assert.equal((await present.json()).exists, true);
  const targetBucket = await testBucket(mf);
  const sourceKey = "_migration/existing-source/fixture.txt";
  await targetBucket.put(sourceKey, "fixture", {
    sha256: hash("fixture"),
    httpMetadata: { contentType: "application/octet-stream" },
  });
  const metadataConflict = await helper("/object-status", {
    method: "POST",
    body: JSON.stringify({
      key: sourceKey,
      size: 7,
      sha256: hash("fixture"),
      contentType: "text/plain",
    }),
  });
  assert.equal(metadataConflict.status, 409);
  await metadataConflict.text();
  const wrongType = await helper("/activate", {
    method: "POST",
    body: JSON.stringify({ id: receipt.id }),
  });
  assert.equal(wrongType.status, 409);
  await wrongType.text();
  assert.equal((await api()).status, 503);
  await targetBucket.put(sourceKey, "fixture", {
    sha256: hash("fixture"),
    httpMetadata: { contentType: "text/plain" },
  });
  response = await helper("/activate", {
    method: "POST",
    body: JSON.stringify({ id: receipt.id }),
  });
  assert.equal(response.status, 200, await response.clone().text());
  assert.equal((await api()).status, 200);
  const enc = (x) => Buffer.from(JSON.stringify(x)).toString("base64url"),
    claims =
      enc({ alg: "HS256", typ: "JWT" }) +
      "." +
      enc({
        sub: "11111111-1111-4111-8111-111111111111",
        role: "STUDENT",
        version: 0,
        iss: "ielts-api",
        aud: ["ielts-web"],
        exp: Math.floor(Date.now() / 1000) + 3600,
        iat: Math.floor(Date.now() / 1000),
        jti: randomUUID(),
      }),
    jwt =
      claims +
      "." +
      createHmac("sha256", "synthetic-contract-test-secret-32-bytes")
        .update(claims)
        .digest("base64url"),
    backend = await mf.getWorker("ielts-api-dev");
  let profileResponse = await backend.fetch("https://api.test/api/v1/profile", {
    headers: { authorization: "Bearer " + jwt },
  });
  assert.equal(profileResponse.status, 200);
  const profile = await profileResponse.json();
  assert.equal(profile.displayName, "Контракт café");
  assert.equal(profile.targetBand, 7.5);
  const limited = await backend.fetch("https://api.test/api/v1/auth/google", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ googleToken: "synthetic-never-sent" }),
  });
  assert.equal(limited.status, 429);
  await limited.text();
  const refreshed = await backend.fetch(
    "https://api.test/api/v1/auth/refresh",
    {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ refreshToken: "synthetic-import-refresh" }),
    },
  );
  assert.equal(refreshed.status, 200);
  await refreshed.text();
  const bucket = await testBucket(mf);
  const object = await bucket.get("_migration/existing-source/fixture.txt");
  assert.equal(await object.text(), "fixture");

  response = await helper("/database", { method: "POST", body: payload });
  assert.equal(response.status, 200);
  response = await helper("/database", {
    method: "POST",
    body: payload.replace("iac-cloudflare-dev-v1", "wrong"),
  });
  assert.equal(response.status, 409);
});
