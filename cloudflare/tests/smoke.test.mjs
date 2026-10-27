import { fileURLToPath } from "node:url";
process.chdir(fileURLToPath(new URL("..", import.meta.url)));
import { test, after } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { Miniflare, convertV4MiniflareOptions, FormData } from "miniflare";
import { readFile, readdir, writeFile } from "node:fs/promises";
execFileSync(
  process.execPath,
  [
    "node_modules/wrangler/bin/wrangler.js",
    "deploy",
    "--dry-run",
    "--config",
    "wrangler.jsonc",
    "--outdir",
    ".artifacts/test-bundle",
    "--name",
    "ielts-api-dev",
    "tests/entry.mjs",
  ],
  {
    env: {
      ...process.env,
      WRANGLER_SEND_METRICS: "false",
      CLOUDFLARE_LOAD_DEV_VARS_FROM_DOT_ENV: "false",
    },
    stdio: "pipe",
  },
);
import { createServer } from "node:http";
let providerCalls = 0,
  providerFail = false,
  streamFinished = false,
  audioSeen = [];
const provider = createServer(async (req, res) => {
  let raw = "";
  for await (const chunk of req) raw += chunk;
  providerCalls++;
  if (providerFail) {
    res.writeHead(503);
    res.end("synthetic failure");
    return;
  }
  const payload = JSON.parse(raw);
  if (payload.stream) {
    streamFinished = false;
    res.setHeader("content-type", "text/event-stream");
    res.write(
      "data: " +
        JSON.stringify({
          choices: [{ delta: { content: "contract first" } }],
        }) +
        "\n\n",
    );
    setTimeout(() => {
      streamFinished = true;
      res.end(
        "data: " +
          JSON.stringify({ choices: [{ delta: {}, finish_reason: "stop" }] }) +
          "\n\ndata: [DONE]\n\n",
      );
    }, 500);
    return;
  }
  const content = payload.messages.at(-1).content;
  let input;
  try {
    input = JSON.parse(
      (Array.isArray(content)
        ? content.find((p) => p.type === "text").text
        : content
      ).replace(/\n\/no_think$/, ""),
    );
  } catch {
    res.setHeader("content-type", "application/json");
    res.end(
      JSON.stringify({ choices: [{ message: { content: "Contract reply" } }] }),
    );
    return;
  }
  if (input.parts) {
    audioSeen = Array.isArray(content)
      ? content
          .filter((p) => p.type === "input_audio")
          .map((p) => {
            const b = Buffer.from(p.input_audio.data, "base64");
            return {
              size: b.length,
              sha256: createHash("sha256").update(b).digest("hex"),
            };
          })
      : [];
    res.setHeader("content-type", "application/json");
    res.end(
      JSON.stringify({
        model: "synthetic-test",
        choices: [
          {
            message: {
              content: JSON.stringify({
                criteria: Object.fromEntries(
                  [
                    "fluency",
                    "lexicalResource",
                    "grammar",
                    "pronunciation",
                  ].map((k) => [
                    k,
                    { band: 6.5, feedback: "Contract fixture" },
                  ]),
                ),
                summary: "Contract fixture",
                partFeedback: input.parts.map((p) => ({
                  partId: p.partId,
                  transcript: p.candidateTranscript,
                  feedback: "Contract fixture",
                  strengths: [],
                  improvements: [],
                })),
              }),
            },
          },
        ],
      }),
    );
    return;
  }
  const result = {
    summary: "Contract fixture",
    taskEvaluations: input.tasks.map((t) => ({
      taskId: t.taskId,
      taskNumber: t.taskNumber,
      criteria: Object.fromEntries(
        [
          "taskAchievementResponse",
          "coherence",
          "lexicalResource",
          "grammar",
        ].map((k) => [k, { band: 6.5, feedback: "Contract fixture" }]),
      ),
      feedback: "Contract fixture",
      strengths: [],
      improvements: [],
    })),
  };
  res.setHeader("content-type", "application/json");
  res.end(
    JSON.stringify({
      model: "synthetic-test",
      choices: [{ message: { content: JSON.stringify(result) } }],
    }),
  );
});
await new Promise((r) => provider.listen(0, "127.0.0.1", r));
after(
  () =>
    new Promise((r) => {
      provider.closeAllConnections();
      provider.close(r);
    }),
);
execFileSync(
  process.execPath,
  [
    "node_modules/wrangler/bin/wrangler.js",
    "deploy",
    "--dry-run",
    "--config",
    "wrangler.audio.jsonc",
    "--outdir",
    ".artifacts/smoke-audio",
  ],
  {
    env: {
      ...process.env,
      WRANGLER_SEND_METRICS: "false",
      CLOUDFLARE_LOAD_DEV_VARS_FROM_DOT_ENV: "false",
    },
    stdio: "pipe",
  },
);
const apiOptions = {
  name: "ielts-api-dev",
  modules: [
    { type: "ESModule", path: ".artifacts/test-bundle/entry.js" },
    ...(await readdir(".artifacts/test-bundle"))
      .filter((f) => f.endsWith(".wasm"))
      .map((f) => ({
        type: "CompiledWasm",
        path: ".artifacts/test-bundle/" + f,
      })),
  ],
  modulesRoot: ".artifacts/test-bundle",
  compatibilityDate: "2026-10-19",
  durableObjects: {
    IAC_STATE: { className: "IELTSDevTest", useSQLite: true },
    IAC_AUDIO: {
      className: "IACAudio",
      scriptName: "ielts-audio-dev",
      useSQLite: true,
    },
  },
  r2Buckets: ["MEDIA"],
  bindings: {
    JWT_SECRET: "synthetic-contract-test-secret-32-bytes",
    PHONE_VERIFICATION_SECRET: "synthetic-phone-test-secret-32-bytes",
    CORS_ALLOWED_ORIGINS: "https://web.test",
    CF_TEST_MODE: "true",
    IAC_ADAPTER_TEST: "true",
    CF_TEST_PROVIDER_URL: "http://127.0.0.1:" + provider.address().port,
    IAC_REQUIRE_IMPORT: "false",
    GOOGLE_CLIENT_ID: "synthetic-client",
    AI_API_KEY: "synthetic-test-key",
    AI_CHAT_COMPLETIONS_URL: "http://127.0.0.1:" + provider.address().port,
    AI_MODEL: "synthetic-test",
    AI_SPEAKING_MODEL: "synthetic-test",
    AI_TIMEOUT: "3s",
  },
};
const audioOptions = {
  name: "ielts-audio-dev",
  modules: [
    { type: "ESModule", path: ".artifacts/smoke-audio/audio-worker.js" },
    ...(await readdir(".artifacts/smoke-audio"))
      .filter((f) => f.endsWith(".wasm"))
      .map((f) => ({
        type: "CompiledWasm",
        path: ".artifacts/smoke-audio/" + f,
      })),
  ],
  modulesRoot: ".artifacts/smoke-audio",
  compatibilityDate: "2026-10-19",
  durableObjects: { IAC_AUDIO: { className: "IACAudio", useSQLite: true } },
  r2Buckets: { MEDIA: "shared-media" },
};
apiOptions.r2Buckets = { MEDIA: "shared-media" };
const mf = new Miniflare(
  convertV4MiniflareOptions({ workers: [apiOptions, audioOptions] }),
);
after(() => mf.dispose());
const observations = [];
let inventory = [];
const call = async (path, init) => {
  const response = await mf.dispatchFetch("https://api.test" + path, init);
  if (path !== "/__test")
    observations.push({
      method: init?.method ?? "GET",
      path: new URL(path, "https://api.test").pathname,
      status: response.status,
    });
  return response;
};
test("full pinned Go router starts inside real workerd with native SQLite/R2", async () => {
  let r = await call("/health/live");
  assert.equal(r.status, 200, await r.text());
  r = await call("/health/ready");
  assert.equal(r.status, 200, await r.text());
  r = await call("/api/v1/guest/config");
  assert.equal(r.status, 200, await r.text());
  r = await call("/api/v1/users/me");
  assert.equal(r.status, 401, await r.text());
  r = await call("/__test", {
    method: "POST",
    body: JSON.stringify({ sql: "__memory" }),
  });
  console.log("Wasm linear memory", await r.json());
});

import { createHmac, createHash, randomUUID } from "node:crypto";
const uid = "11111111-1111-4111-8111-111111111111",
  other = "22222222-2222-4222-8222-222222222222",
  admin = "33333333-3333-4333-8333-333333333333";
const secret = "synthetic-contract-test-secret-32-bytes";
function token(id = uid, role = "STUDENT", version = 0) {
  const enc = (x) => Buffer.from(JSON.stringify(x)).toString("base64url");
  const now = Math.floor(Date.now() / 1000),
    body =
      enc({ alg: "HS256", typ: "JWT" }) +
      "." +
      enc({
        sub: id,
        role,
        version,
        iss: "ielts-api",
        aud: ["ielts-web"],
        exp: now + 3600,
        iat: now,
        nbf: now,
        jti: randomUUID(),
      });
  return (
    body + "." + createHmac("sha256", secret).update(body).digest("base64url")
  );
}
const sql = async (q, args = []) => {
  const r = await call("/__test", {
    method: "POST",
    body: JSON.stringify({ sql: q, args }),
  });
  assert.equal(r.status, 200, await r.clone().text());
  return r.json();
};
async function api(
  path,
  {
    method = "GET",
    body,
    as = uid,
    role = "STUDENT",
    status = 200,
    headers = {},
  } = {},
) {
  const r = await call("/api/v1" + path, {
    method,
    headers: {
      authorization: "Bearer " + token(as, role),
      ...(body !== undefined ? { "content-type": "application/json" } : {}),
      ...headers,
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await r.text();
  assert.equal(
    r.status,
    status,
    `${method} ${path}: ${text.replace(/"(accessToken|refreshToken|registrationToken)":"[^"]*"/g, '"$1":"<redacted>"')}`,
  );
  return text ? JSON.parse(text) : null;
}
async function seedUser(id, role) {
  await sql(
    "INSERT INTO users(id,email,role,status,terms_accepted_at) VALUES(?,?,?,'REGISTERED',?)",
    [id, id + "@example.invalid", role, "2026-10-01T00:00:00.000000000Z"],
  );
  await sql(
    "INSERT INTO user_profiles(user_id,display_name,exam_type,timezone,target_band) VALUES(?,'Contract fixture','academic','Asia/Almaty',7)",
    [id],
  );
  for (const skill of ["reading", "listening", "writing", "speaking"])
    await sql(
      "INSERT INTO user_skill_progress(id,user_id,skill) VALUES(?,?,?)",
      [randomUUID(), id, skill],
    );
}
test("JWT ownership, token revocation, native material create/publish and attempts", async () => {
  await seedUser(uid, "STUDENT");
  await seedUser(other, "STUDENT");
  await seedUser(admin, "ADMIN");
  const me = await api("/users/me");
  assert.equal(me.id, uid);
  await api("/profile");
  await api("/dashboard");
  for (const path of [
    "/reading/materials",
    "/listening/tests",
    "/writing/materials",
    "/speaking/materials",
    "/attempts",
    "/full-mocks/overview",
    "/ai-limits",
  ])
    await api(path, { status: path === "/ai-limits" ? 404 : 200 });
  const input = {
    slug: "contract-reading",
    examType: "academic",
    difficulty: "foundation",
    title: "Contract passage",
    description: "Fixture",
    body: "A source passage for a deterministic contract test.",
    questionGroups: [
      {
        position: 1,
        type: "short_answer",
        instructions: "Write one word.",
        questions: [
          {
            position: 1,
            prompt: "Which word?",
            content: {},
            answer: { accepted: ["source"] },
            explanation: "The passage says source.",
            points: 1,
          },
        ],
      },
    ],
  };
  const material = await api("/admin/reading/materials", {
    method: "POST",
    body: input,
    as: admin,
    role: "ADMIN",
    status: 201,
  });
  const published = await api(
    `/admin/reading/materials/${material.id}/publish`,
    {
      method: "POST",
      body: { revision: material.revision },
      as: admin,
      role: "ADMIN",
    },
  );
  const publicMaterial = await api(`/reading/materials/${material.id}`);
  assert.ok(!JSON.stringify(publicMaterial).includes("accepted"));
  const started = await api(`/reading/materials/${material.id}/attempts`, {
    method: "POST",
    status: 201,
  });
  const id = started.attempt.id,
    question = publicMaterial.questionGroups[0].questions[0];
  await api(`/attempts/${id}`, { as: other, status: 404 });
  await api(`/attempts/${id}/answers`, {
    method: "PUT",
    body: {
      answers: [{ questionId: question.id, answer: { value: "source" } }],
    },
  });
  const submitted = await api(`/attempts/${id}/submit`, {
    method: "POST",
    body: {
      answers: [{ questionId: question.id, answer: { value: "wrong" } }],
    },
  });
  assert.equal(submitted.status ?? submitted.attempt?.status, "SUBMITTED");
  await api("/attempts");
  await api("/attempts/mistakes");
  await api("/attempts/mistakes/attempts");
  await api(`/attempts/${id}/material`);
  await sql("UPDATE users SET token_version=1 WHERE id=?", [uid]);
  const r = await call("/api/v1/users/me", {
    headers: { authorization: "Bearer " + token() },
  });
  assert.equal(r.status, 401);
  await sql("UPDATE users SET token_version=0 WHERE id=?", [uid]);
});
test("adaptive study plan and learning use actual persisted attempts and optimistic revisions", async () => {
  const plan = await api("/study-plan", {
    method: "POST",
    body: {
      dailyMinutes: 30,
      studyDays: [1, 2, 3, 4, 5, 6, 7],
      weeklyMockEnabled: false,
    },
  });
  assert.ok(plan.plan?.id);
  assert.equal(plan.overallBand, null);
  await api("/study-plan");
  const learning = await api("/study-plan/learning");
  assert.ok(learning.cycles.length > 0);
  const cycle = learning.cycles[0];
  await api(`/study-plan/learning/cycles/${cycle.id}/view`, {
    method: "POST",
    body: {
      expectedRevision: cycle.revision,
      idempotencyKey: "contract-view-1",
    },
  });
  await api("/study-plan/learning/inventory");
  await api("/study-plan/settings", {
    method: "PUT",
    body: {
      dailyMinutes: 45,
      studyDays: [1, 3, 5],
      weeklyMockEnabled: false,
      expectedRevision: 0,
    },
    status: 409,
  });
  await api("/study-plan/replan", {
    method: "POST",
    body: { expectedRevision: (await api("/study-plan")).plan.revision },
  });
});

test("refresh rotation is atomic; reuse revokes the replacement; logout clears cookies", async () => {
  const raw = "synthetic-refresh-token",
    hash = createHash("sha256").update(raw).digest("hex");
  await sql(
    `INSERT INTO refresh_sessions(id,user_id,token_hash,expires_at) VALUES(?,?,X'${hash}',?)`,
    [randomUUID(), uid, "2099-01-01T00:00:00.000000000Z"],
  );
  const first = await call("/api/v1/auth/refresh", {
    method: "POST",
    headers: { "content-type": "application/json", origin: "https://web.test" },
    body: JSON.stringify({ refreshToken: raw }),
  });
  assert.equal(first.status, 200, await first.clone().text());
  assert.match(first.headers.get("set-cookie"), /HttpOnly/);
  const next = await first.json();
  await api("/auth/refresh", {
    method: "POST",
    body: { refreshToken: raw },
    status: 401,
  });
  await api("/auth/refresh", {
    method: "POST",
    body: { refreshToken: next.refreshToken },
    status: 401,
  });
  const out = await call("/api/v1/auth/logout", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ refreshToken: next.refreshToken }),
  });
  assert.equal(out.status, 204);
  assert.match(out.headers.get("set-cookie"), /Max-Age=0/);
});
test("writing async grading uses the original evaluator and durable idempotent alarm claims", async () => {
  const image = await sql(
    "INSERT INTO writing_media(id,kind,original_name,mime_type,storage_key,byte_size,created_by) VALUES(?,'image','fixture.png','image/png','fixture/image.png',10,?) RETURNING id",
    [randomUUID(), admin],
  );
  const tasks = [
    {
      position: 1,
      type: "task1",
      prompt: "Describe the information in this chart.",
      minimumWords: 1,
      visualType: "bar_chart",
      visualAssetId: image[0].id,
      assessmentNotes: "Synthetic chart: one value is ten.",
    },
    {
      position: 2,
      type: "task2",
      prompt: "Discuss the benefits of careful planning.",
      minimumWords: 1,
      essayType: "opinion",
    },
  ];
  const input = {
    slug: "contract-writing",
    examType: "academic",
    difficulty: "foundation",
    title: "Contract writing",
    description: "Fixture",
    durationMinutes: 60,
    tasks,
  };
  const material = await api("/admin/writing/materials", {
    method: "POST",
    body: input,
    as: admin,
    role: "ADMIN",
    status: 201,
  });
  await api(`/admin/writing/materials/${material.id}/publish`, {
    method: "POST",
    body: { revision: material.revision },
    as: admin,
    role: "ADMIN",
  });
  const started = await api(`/writing/materials/${material.id}/attempts`, {
    method: "POST",
    status: 201,
  });
  const id = started.attempt.id;
  const answers = started.material?.tasks ?? started.writing?.tasks;
  const actualTasks =
    answers ?? (await api(`/writing/materials/${material.id}`)).tasks;
  const result = await api(`/attempts/${id}/submit`, {
    method: "POST",
    body: {
      answers: actualTasks.map((t) => ({
        questionId: t.id,
        answer: { value: "A synthetic answer for testing." },
      })),
    },
    status: 200,
  });
  assert.equal(result.status ?? result.attempt?.status, "PROCESSING");
  await sql("__alarm");
  await api(`/attempts/${id}/status`);
  const state = await sql(
    "SELECT status,attempts FROM writing_assessment_jobs WHERE attempt_id=?",
    [id],
  );
  assert.equal(state[0].status, "READY");
  assert.equal(state[0].attempts, 1);
  const calls = providerCalls;
  await sql("__alarm");
  assert.equal(providerCalls, calls);
  assert.equal(
    (
      await sql(
        "SELECT count(*) n FROM writing_evaluations WHERE attempt_id=?",
        [id],
      )
    )[0].n,
    1,
  );
});
test("authoritative runtime route inventory", async () => {
  const routes = (await sql("__routes")).sort(
    (a, b) =>
      a.path.localeCompare(b.path, "en") ||
      a.method.localeCompare(b.method, "en"),
  );
  inventory = routes;
  assert.ok(routes.length > 140);
  await writeFile(
    "schema/routes.json",
    JSON.stringify(
      { revision: "40ed0dc5fca0b9df989a29d421a81476e7df0f50", routes },
      null,
      2,
    ) + "\n",
  );
  console.log("Registered routes", routes.length);
});
test("admin account, AI configuration and analytics contracts", async () => {
  for (const path of [
    "/admin/access",
    "/admin/users",
    "/admin/users/" + uid,
    "/admin/users/" + uid + "/attempts",
    "/admin/ai-providers",
    "/admin/ai-providers/routing",
    "/admin/ai-providers/stats",
    "/admin/ai-limits",
    "/admin/waitlist",
    "/admin/super-admins",
    "/admin/analytics/realtime",
    "/admin/analytics/overview",
  ])
    await api(path, { as: admin, role: "ADMIN" });
});
const png = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=",
  "base64",
);
async function upload(
  path,
  bytes = png,
  name = "fixture.png",
  type = "image/png",
  fields = {},
  as = admin,
  role = "ADMIN",
) {
  const data = new FormData();
  data.set("file", new Blob([bytes], { type }), name);
  for (const [k, v] of Object.entries(fields)) data.set(k, v);
  const r = await call("/api/v1" + path, {
    method: "POST",
    headers: { authorization: "Bearer " + token(as, role) },
    body: data,
  });
  assert.equal(r.status, 201, await r.clone().text());
  return r.json();
}
let listeningID, speakingID;
const question = (number) => ({
  position: number,
  prompt: "Contract question " + number,
  number,
  content: {},
  answer: { accepted: ["source"] },
  explanation: "Contract fixture.",
  points: 1,
});
test("private R2 uploads preserve bytes, seek/range and authorization; Listening and Speaking versioned materials", async () => {
  const image = await upload("/admin/writing/media");
  const stored = (
    await sql("SELECT storage_key FROM writing_media WHERE id=?", [image.id])
  )[0].storage_key;
  let r = await call("/api/v1/writing/media/" + image.id, {
    headers: {
      authorization: "Bearer " + token(admin, "ADMIN"),
      range: "bytes=0-7",
    },
  });
  assert.equal(r.status, 206);
  assert.deepEqual(Buffer.from(await r.arrayBuffer()), png.subarray(0, 8));
  await api("/writing/media/" + image.id, { status: 404 });
  const audio = await upload(
    "/admin/listening/media",
    Buffer.from("ID3-contract-fixture"),
    "fixture.mp3",
    "audio/mpeg",
    { kind: "audio" },
  );
  const input = {
    slug: "contract-listening",
    examType: "academic",
    title: "Contract listening",
    description: "Fixture",
    durationMinutes: 30,
    parts: Array.from({ length: 4 }, (_, p) => ({
      position: p + 1,
      title: "Part " + (p + 1),
      audioAssetId: audio.id,
      groups: [
        {
          position: 1,
          type: "short_answer",
          instructions: "Write one word.",
          context: "Contract fixture",
          config: {},
          questions: Array.from({ length: 10 }, (_, i) =>
            question(p * 10 + i + 1),
          ),
        },
      ],
    })),
  };
  const listening = await api("/admin/listening/tests", {
    method: "POST",
    body: input,
    as: admin,
    role: "ADMIN",
    status: 201,
  });
  listeningID = listening.id;
  await api(`/admin/listening/tests/${listening.id}/publish`, {
    method: "POST",
    body: { revision: listening.revision },
    as: admin,
    role: "ADMIN",
  });
  const published = await api(`/listening/tests/${listening.id}`);
  assert.equal(published.parts.length, 4);
  assert.ok(!JSON.stringify(published).includes("accepted"));
  r = await call("/api/v1/listening/media/" + audio.id, {
    headers: { authorization: "Bearer " + token() },
  });
  assert.equal(r.status, 200);
  assert.equal(await r.text(), "ID3-contract-fixture");
  const started = await api(`/listening/tests/${listening.id}/attempts`, {
    method: "POST",
    status: 201,
  });
  const answers = published.parts
    .flatMap((p) => p.groups.flatMap((g) => g.questions))
    .map((q) => ({ questionId: q.id, answer: { value: "source" } }));
  const done = await api(`/attempts/${started.attempt.id}/submit`, {
    method: "POST",
    body: { answers },
  });
  assert.equal(done.score, 40);
  const parts = ["part1", "part2", "part3"].map((type, i) => ({
    type,
    title: "Contract part " + (i + 1),
    instructions: "Fixture",
    responseSeconds: 120,
    ...(i === 1
      ? {
          preparationSeconds: 60,
          cueCard: [
            "First fixture cue",
            "Second fixture cue",
            "Third fixture cue",
          ],
        }
      : {
          questions: [
            { prompt: "First fixture question?" },
            { prompt: "Second fixture question?" },
          ],
        }),
  }));
  const speaking = await api("/admin/speaking/materials", {
    method: "POST",
    body: {
      slug: "contract-speaking",
      examType: "academic",
      difficulty: "foundation",
      title: "Contract speaking",
      description: "Fixture",
      parts,
    },
    as: admin,
    role: "ADMIN",
    status: 201,
  });
  speakingID = speaking.id;
  await api(`/admin/speaking/materials/${speaking.id}/publish`, {
    method: "POST",
    body: { revision: speaking.revision },
    as: admin,
    role: "ADMIN",
  });
  await api(`/speaking/materials/${speaking.id}`);
  await api(`/speaking/materials/${speaking.id}/attempts`, {
    method: "POST",
    status: 201,
  });
});
test("full mock generation, pinned section clocks, pause/resume and exam ownership", async () => {
  const passages = Array.from({ length: 3 }, (_, p) => ({
    slug: "mock-passage-" + p,
    examType: "academic",
    difficulty: "foundation",
    title: "Contract passage " + p,
    description: "Fixture",
    body: "A deterministic passage for the full mock contract.",
    questionGroups: [
      {
        position: 1,
        type: "short_answer",
        instructions: "Write one word.",
        questions: Array.from({ length: p === 2 ? 14 : 13 }, (_, i) => {
          const q = question(i + 1);
          delete q.number;
          return q;
        }),
      },
    ],
  }));
  const imported = await api("/admin/reading/import", {
    method: "POST",
    body: { title: "Contract full reading", durationMinutes: 60, passages },
    as: admin,
    role: "ADMIN",
    status: 201,
  });
  const reading = imported.items[0];
  await api(`/admin/reading/materials/${reading.id}/publish`, {
    method: "POST",
    body: { revision: reading.revision },
    as: admin,
    role: "ADMIN",
  });
  const overview = await api("/full-mocks/overview");
  assert.equal(overview.ready, true);
  const session = await api("/full-mocks/start", {
    method: "POST",
    body: {},
    status: 201,
  });
  assert.ok(session.id);
  const again = await api("/full-mocks/start", { method: "POST", body: {} });
  assert.equal(again.id, session.id);
  await api("/full-mock-sessions/" + session.id);
  await api("/full-mock-sessions/" + session.id, { as: other, status: 404 });
  const section = await api(`/full-mock-sessions/${session.id}/sections/1`);
  assert.ok(section);
  await api(`/full-mock-sessions/${session.id}/pause`, {
    method: "POST",
    body: {},
  });
  const paused = await sql(
    "SELECT remaining_milliseconds,deadline_at FROM full_mock_session_sections WHERE session_id=? AND position=1",
    [session.id],
  );
  assert.ok(paused[0].remaining_milliseconds > 0);
  assert.equal(paused[0].deadline_at, null);
  await api(`/full-mock-sessions/${session.id}/sections/1`);
  await api(`/full-mock-sessions/${session.id}/advance`, {
    method: "POST",
    body: {},
    status: 409,
  });
  await sql(
    "UPDATE full_mock_session_sections SET started_at='2020-01-01T00:00:00.000000000Z',deadline_at='2020-01-01T00:00:01.000000000Z' WHERE session_id=? AND position=1",
    [session.id],
  );
  await api(`/full-mock-sessions/${session.id}/advance`, {
    method: "POST",
    body: {},
  });
  await api(`/full-mock-sessions/${session.id}/finish`, {
    method: "POST",
    body: {},
  });
});
test("writer blog save/publish and public sanitization", async () => {
  const media = await upload("/blog/media");
  const post = await api("/writer/posts", {
    method: "POST",
    body: {
      slug: "contract-blog",
      title: "Contract blog post",
      description: "Contract description",
      coverMediaId: media.id,
      bodyHtml:
        "<p>" + "Contract article. ".repeat(10) + "</p><script>bad()</script>",
      bodyJson: { type: "doc", content: [] },
    },
    as: admin,
    role: "ADMIN",
    status: 201,
  });
  await api(`/writer/posts/${post.id}/publish`, {
    method: "POST",
    body: {},
    as: admin,
    role: "ADMIN",
  });
  await api("/blog/posts/contract-blog", { status: 404 });
  const published = await api("/admin/blog/posts/" + post.id, {
    as: admin,
    role: "ADMIN",
  });
  assert.ok(!published.bodyHtml.includes("<script>"));
  await api("/blog/posts", { status: 404 });
  await api("/blog/posts", { as: admin, role: "ADMIN" });
  await api("/blog/posts/contract-blog", { as: admin, role: "ADMIN" });
  await api("/writer/posts", { as: admin, role: "ADMIN" });
  await api("/admin/blog/posts", { as: admin, role: "ADMIN" });
});

test("admin attempt details preserve structured JSON and legacy booleans", async () => {
  const row = (
    await sql(
      "SELECT id FROM attempts WHERE user_id=? ORDER BY started_at LIMIT 1",
      [uid],
    )
  )[0];
  const value = await api(`/admin/users/${uid}/attempts/${row.id}`, {
    as: admin,
    role: "ADMIN",
  });
  assert.equal(typeof value.attempt, "object");
  assert.ok(Array.isArray(value.answers));
  const user = await api(`/admin/users/${uid}`, { as: admin, role: "ADMIN" });
  assert.equal(typeof user.googleConnected, "boolean");
  assert.ok(Array.isArray(user.permissions));
  for (const key of ["skills", "sessions", "activityDays"])
    assert.ok(Array.isArray(user[key]), key);
  assert.equal(user.blocked, false);
  assert.equal(user.googleConnected, false);
});

test("Google sign-in pending registration, completion and existing identity login preserve cookies", async () => {
  const pending = await api("/auth/google", {
    method: "POST",
    body: { googleToken: "synthetic-google" },
  });
  assert.equal(pending.registrationRequired, true);
  assert.ok(pending.registrationToken);
  const registered = await api("/auth/google/complete", {
    method: "POST",
    body: {
      registrationToken: pending.registrationToken,
      name: "Google contract",
      phone: "+77000000001",
      acceptedTerms: true,
    },
    status: 201,
  });
  assert.ok(registered.accessToken);
  const logged = await api("/auth/google", {
    method: "POST",
    body: { googleToken: "synthetic-google" },
  });
  assert.equal(logged.user.id, registered.user.id);
  await api("/auth/google", {
    method: "POST",
    body: { googleToken: "synthetic-invalid" },
    status: 401,
  });
});
test("SSE streams before provider completion; concurrent requests do not block or share HTTP state", async () => {
  const response = await call("/api/v1/assistant/chat/stream", {
    method: "POST",
    headers: {
      authorization: "Bearer " + token(),
      "content-type": "application/json",
    },
    body: JSON.stringify({
      messages: [{ role: "user", content: "Contract stream" }],
    }),
  });
  assert.equal(response.status, 200);
  const reader = response.body.getReader(),
    first = await reader.read();
  assert.ok(first.value.length > 0);
  assert.equal(streamFinished, false);
  const results = await Promise.all(
    Array.from({ length: 4 }, () => api("/profile")),
  );
  assert.ok(results.every((p) => p));
  assert.equal(streamFinished, false);
  let output = new TextDecoder().decode(first.value);
  for (;;) {
    const item = await reader.read();
    if (item.done) break;
    output += new TextDecoder().decode(item.value);
  }
  assert.ok(output.includes('\"type\":\"done\"'));
  assert.ok(!output.includes("unavailable"));
});
test("concurrent duplicate material creation commits one unique record and rolls back the loser", async () => {
  const input = {
    slug: "atomic-duplicate",
    examType: "academic",
    difficulty: "foundation",
    title: "Contract passage",
    description: "Fixture",
    body: "A deterministic source passage for an atomicity test.",
    questionGroups: [],
  };
  const requests = Array.from({ length: 2 }, () =>
    call("/api/v1/admin/reading/materials", {
      method: "POST",
      headers: {
        authorization: "Bearer " + token(admin, "ADMIN"),
        "content-type": "application/json",
      },
      body: JSON.stringify(input),
    }),
  );
  const responses = await Promise.all(requests);
  const statuses = responses.map((r) => r.status).sort();
  for (const response of responses) await response.text();
  assert.deepEqual(statuses, [201, 409]);
  assert.equal(
    (
      await sql(
        "SELECT count(*) n FROM reading_materials WHERE slug='atomic-duplicate'",
      )
    )[0].n,
    1,
  );
});

test("Speaking media and AI feedback retain the original live synchronous contracts", async () => {
  const material = await api(`/speaking/materials/${speakingID}`),
    started = await api(`/speaking/materials/${speakingID}/attempts`, {
      method: "POST",
    });
  const audio = Buffer.alloc(12 * 1024 * 1024, 1);
  Buffer.from("RIFF").copy(audio);
  let response;
  for (const part of material.parts) {
    const form = new FormData();
    form.set("partId", part.id);
    form.set(
      "recording",
      new Blob([audio], { type: "audio/wav" }),
      "fixture.wav",
    );
    response = await call(`/api/v1/attempts/${started.attempt.id}/recordings`, {
      method: "POST",
      headers: { authorization: "Bearer " + token() },
      body: form,
    });
    assert.equal(response.status, 201, await response.clone().text());
    await response.text();
  }
  response = await call(
    `/api/v1/attempts/${started.attempt.id}/recordings/${material.parts[0].id}`,
    { headers: { authorization: "Bearer " + token(), range: "bytes=0-3" } },
  );
  assert.equal(response.status, 206);
  assert.equal(await response.text(), "RIFF");
  const done = await api(`/attempts/${started.attempt.id}/submit`, {
    method: "POST",
    body: {
      answers: material.parts.map((p) => ({
        questionId: p.id,
        answer: {
          value: "A synthetic transcript for a speaking contract test.",
        },
      })),
    },
  });
  assert.equal(done.status, "SUBMITTED");
  assert.equal(
    (
      await sql(
        "SELECT count(*) n FROM speaking_evaluations WHERE attempt_id=?",
        [done.id],
      )
    )[0].n,
    1,
  );
  const memory = await sql("__memory");
  assert.equal(audioSeen.length, 3);
  for (const item of audioSeen) {
    assert.equal(item.size, audio.length);
    assert.equal(item.sha256, createHash("sha256").update(audio).digest("hex"));
  }
  console.log("Speaking three max audio Wasm bytes", memory.bytes);
  assert.ok(memory.bytes < 100 * 1024 * 1024);
});

test("admin literal Unicode search works beyond the native LIKE pattern limit", async () => {
  const text = "Контракт".repeat(4) + "%_";
  await sql("UPDATE user_profiles SET display_name=? WHERE user_id=?", [
    text,
    uid,
  ]);
  const result = await api("/admin/users?q=" + encodeURIComponent(text), {
    as: admin,
    role: "ADMIN",
  });
  assert.equal(result.total, 1);
  assert.equal(result.items[0].id, uid);
});
test("large multipart media uses bounded native buffers and streamed R2 parts", async () => {
  const data = Buffer.alloc(50 * 1024 * 1024, 1);
  Buffer.from("ID3").copy(data);
  const started = performance.now();
  const media = await upload(
    "/admin/listening/media",
    data,
    "large-contract.mp3",
    "audio/mpeg",
    { kind: "audio" },
  );
  assert.equal(media.byteSize, data.length);
  const response = await call("/api/v1/listening/media/" + media.id, {
    headers: {
      authorization: "Bearer " + token(admin, "ADMIN"),
      range: "bytes=52428789-52428799",
    },
  });
  assert.equal(response.status, 206);
  assert.equal((await response.arrayBuffer()).byteLength, 11);
  const before = await sql("__counts");
  const full = await call("/api/v1/listening/media/" + media.id, {
    headers: { authorization: "Bearer " + token(admin, "ADMIN") },
  });
  assert.equal(full.status, 200);
  const hash = createHash("sha256");
  let received = 0;
  for await (const chunk of full.body) {
    received += chunk.byteLength;
    hash.update(chunk);
  }
  assert.equal(received, data.length);
  assert.equal(
    hash.digest("hex"),
    createHash("sha256").update(data).digest("hex"),
  );
  const after = await sql("__counts");
  assert.equal(
    (after["http.r2stream"] ?? 0) - (before["http.r2stream"] ?? 0),
    1,
  );
  assert.ok((after.r2range ?? 0) - (before.r2range ?? 0) <= 1);
  const memory = await sql("__memory");
  assert.ok(memory.bytes < 100 * 1024 * 1024);
  console.log(
    "Large upload wall ms",
    Math.round(performance.now() - started),
    "Wasm bytes",
    memory.bytes,
  );
});
test("durable writing retries honor backoff, recover expired leases and commit one evaluation", async () => {
  const material = (
    await sql("SELECT id FROM writing_materials ORDER BY created_at LIMIT 1")
  )[0];
  const publicMaterial = await api(`/writing/materials/${material.id}`);
  async function queue() {
    const started = await api(`/writing/materials/${material.id}/attempts`, {
      method: "POST",
      status: 201,
    });
    await api(`/attempts/${started.attempt.id}/submit`, {
      method: "POST",
      body: {
        answers: publicMaterial.tasks.map((t) => ({
          questionId: t.id,
          answer: { value: "A synthetic retry answer." },
        })),
      },
    });
    return started.attempt.id;
  }
  const id = await queue();
  providerFail = true;
  await sql("__alarm");
  providerFail = false;
  let job = (
    await sql(
      "SELECT status,attempts,error_code,next_attempt_at FROM writing_assessment_jobs WHERE attempt_id=?",
      [id],
    )
  )[0];
  assert.equal(job.status, "QUEUED");
  assert.equal(job.attempts, 1);
  assert.ok(job.error_code);
  assert.ok(new Date(job.next_attempt_at) > new Date());
  const calls = providerCalls;
  await sql("__alarm");
  assert.equal(providerCalls, calls);
  await sql(
    "UPDATE writing_assessment_jobs SET next_attempt_at='2020-01-01T00:00:00.000000000Z' WHERE attempt_id=?",
    [id],
  );
  await sql("__alarm");
  job = (
    await sql(
      "SELECT status,attempts FROM writing_assessment_jobs WHERE attempt_id=?",
      [id],
    )
  )[0];
  assert.equal(job.status, "READY");
  assert.equal(job.attempts, 2);
  assert.equal(
    (
      await sql(
        "SELECT count(*) n FROM writing_evaluations WHERE attempt_id=?",
        [id],
      )
    )[0].n,
    1,
  );
  const recovered = await queue();
  await sql(
    "UPDATE writing_assessment_jobs SET status='PROCESSING',attempts=1,updated_at='2020-01-01T00:00:00.000000000Z' WHERE attempt_id=?",
    [recovered],
  );
  await sql("__alarm");
  await sql("__alarm");
  assert.equal(
    (
      await sql(
        "SELECT status FROM writing_assessment_jobs WHERE attempt_id=?",
        [recovered],
      )
    )[0].status,
    "READY",
  );
  assert.equal(
    (
      await sql(
        "SELECT count(*) n FROM writing_evaluations WHERE attempt_id=?",
        [recovered],
      )
    )[0].n,
    1,
  );
});
test("guest Turnstile start, restrictions, expiry and claim preserve the fixed dev exam", async () => {
  await sql("__configure", [
    {
      GUEST_TRIAL_ENABLED: "true",
      TURNSTILE_SITE_KEY: "synthetic-site-key",
      TURNSTILE_SECRET_KEY: "synthetic-secret",
      TURNSTILE_HOSTNAMES: "web.test",
    },
  ]);
  const response = await call("/api/v1/guest/start", {
    method: "POST",
    headers: { origin: "https://web.test", "content-type": "application/json" },
    body: JSON.stringify({
      examType: "academic",
      turnstileToken: "synthetic-turnstile",
      acceptedTerms: true,
    }),
  });
  assert.equal(response.status, 200, await response.clone().text());
  const cookie = response.headers.get("set-cookie").split(";")[0],
    session = await response.json();
  assert.ok(session.id);
  async function guest(path, { method = "GET", body, status = 200 } = {}) {
    const r = await call("/api/v1" + path, {
      method,
      headers: {
        cookie,
        origin: "https://web.test",
        ...(body ? { "content-type": "application/json" } : {}),
      },
      body: body ? JSON.stringify(body) : undefined,
    });
    const text = await r.text();
    assert.equal(r.status, status, `${path}: ${text}`);
    return text ? JSON.parse(text) : null;
  }
  await guest("/guest/session");
  await guest("/profile", { status: 401 });
  await guest(`/full-mock-sessions/${session.id}/finish`, {
    method: "POST",
    body: {},
    status: 409,
  });
  for (let position = 1; position <= 4; position++) {
    await guest(`/full-mock-sessions/${session.id}/sections/${position}`);
    await sql(
      "UPDATE full_mock_session_sections SET started_at='2020-01-01T00:00:00.000000000Z',deadline_at='2020-01-01T00:00:01.000000000Z' WHERE session_id=? AND position=?",
      [session.id, position],
    );
    await guest(`/full-mock-sessions/${session.id}/advance`, {
      method: "POST",
      body: {},
    });
  }
  const claimed = await api("/guest/claim", {
    method: "POST",
    body: {},
    headers: { cookie, origin: "https://web.test" },
  });
  assert.equal(claimed.sessionId, session.id);
  const again = await api("/guest/claim", {
    method: "POST",
    body: {},
    headers: { cookie, origin: "https://web.test" },
  });
  assert.equal(again.sessionId, session.id);
  await api("/guest/claim", {
    method: "POST",
    body: {},
    as: other,
    headers: { cookie, origin: "https://web.test" },
    status: 409,
  });
});

after(async () => {
  const routes = inventory.map((route) => {
    const pattern = new RegExp(
      "^" + route.path.replace(/\{[^}]+\}/g, "[^/]+") + "$",
    );
    const cases = observations.filter(
      (o) => o.method === route.method && pattern.test(o.path),
    );
    return {
      ...route,
      contractRequests: cases.length,
      statuses: [...new Set(cases.map((c) => c.status))].sort(),
      state: cases.length ? "exercised" : "not-exercised",
    };
  });
  await writeFile(
    "schema/coverage.json",
    JSON.stringify(
      {
        revision: "40ed0dc5fca0b9df989a29d421a81476e7df0f50",
        runtime:
          "real workerd + SQLite Durable Object + R2; synthetic identities/providers only",
        registered: routes.length,
        exercised: routes.filter((r) => r.contractRequests).length,
        successful: routes.filter((r) =>
          r.statuses.some((s) => s >= 200 && s < 300),
        ).length,
        routes,
      },
      null,
      2,
    ) + "\n",
  );
});

test("learning corrections keep source answers immutable and replay identical commands", async () => {
  let learning = await api("/study-plan/learning"),
    cycle = learning.cycles.find((c) => c.source.originalAnswer === "wrong");
  assert.ok(cycle);
  await api(`/study-plan/learning/cycles/${cycle.id}`, {
    as: other,
    status: 404,
  });
  cycle = await api(`/study-plan/learning/cycles/${cycle.id}`);
  const wrong = {
    expectedRevision: cycle.revision,
    idempotencyKey: "correction-wrong",
    text: "still wrong",
    explanation: "Contract evidence",
    assisted: false,
  };
  cycle = await api(`/study-plan/learning/cycles/${cycle.id}/correction`, {
    method: "POST",
    body: wrong,
  });
  assert.equal(cycle.correction.verdict, "incorrect");
  const repeat = await api(
    `/study-plan/learning/cycles/${cycle.id}/correction`,
    { method: "POST", body: wrong },
  );
  assert.equal(repeat.revision, cycle.revision);
  await api(`/study-plan/learning/cycles/${cycle.id}/correction`, {
    method: "POST",
    body: { ...wrong, text: "different" },
    status: 409,
  });
  cycle = await api(`/study-plan/learning/cycles/${cycle.id}/correction`, {
    method: "POST",
    body: {
      ...wrong,
      expectedRevision: cycle.revision,
      idempotencyKey: "correction-right",
      text: "source",
    },
  });
  assert.equal(cycle.correction.verdict, "correct");
  const original = (
    await sql(
      "SELECT answer FROM attempt_answers WHERE attempt_id=? AND question_id=?",
      [cycle.source.attemptId, cycle.source.questionId],
    )
  )[0];
  assert.equal(JSON.parse(original.answer).value, "wrong");
  learning = await api("/study-plan/learning");
  const drill = learning.drills.find((d) => d.id === "iac-r-library");
  assert.ok(drill);
  const session = await api(`/study-plan/learning/drills/${drill.id}/start`, {
    method: "POST",
    body: {
      expectedRevision: learning.revision,
      idempotencyKey: "drill-start",
    },
  });
  await api(`/study-plan/learning/sessions/${session.id}`, {
    as: other,
    status: 404,
  });
  await api(`/study-plan/learning/sessions/${session.id}`);
  const submitted = await api(
    `/study-plan/learning/sessions/${session.id}/submit`,
    {
      method: "POST",
      body: {
        expectedRevision: session.revision,
        idempotencyKey: "drill-submit",
        text: "TRUE",
        assisted: false,
      },
    },
  );
  assert.equal(submitted.status, "submitted");
  assert.equal(submitted.independent, false);
  const skipped = await api(`/study-plan/learning/cycles/${cycle.id}/skip`, {
    method: "POST",
    body: { expectedRevision: cycle.revision, idempotencyKey: "cycle-skip" },
  });
  assert.ok(skipped.skippedAt);
});
test("profile, goal, waitlist and analytics persist native mutations", async () => {
  const profile = await api("/profile", {
    method: "PATCH",
    body: { displayName: "Native contract", timezone: "Asia/Almaty" },
  });
  assert.equal(profile.displayName, "Native contract");
  await api("/profile/goal", {
    method: "PUT",
    body: { targetBand: 7.5, examType: "academic", examDate: "2027-01-01" },
  });
  await api("/waitlist", {
    method: "POST",
    body: {
      firstName: "Contract",
      lastName: "Fixture",
      email: "waitlist@example.invalid",
      phone: "+77000000002",
      source: "contract",
      googleToken: "synthetic-waitlist",
    },
    status: 201,
  });
  const found = await api("/waitlist/check", {
    method: "POST",
    body: { phone: "+77000000002", googleToken: "synthetic-waitlist" },
  });
  assert.equal(found.accountRegistered, true);
  await api("/analytics/ping", {
    method: "POST",
    body: { visitorId: randomUUID(), path: "/contract", view: true },
    status: 204,
  });
  await api("/admin/analytics/realtime", { as: admin, role: "ADMIN" });
  await api("/admin/analytics/overview?days=30", { as: admin, role: "ADMIN" });
});
test("versioned admin lists, previews and edits use full native repositories", async () => {
  const options = { as: admin, role: "ADMIN" };
  for (const [kind, table] of [
    ["reading/materials", "reading_materials"],
    ["listening/tests", "listening_tests"],
    ["writing/materials", "writing_materials"],
    ["speaking/materials", "speaking_materials"],
  ]) {
    await api("/admin/" + kind, options);
    const row = (
      await sql(`SELECT id FROM ${table} ORDER BY created_at LIMIT 1`)
    )[0];
    const full = await api(`/admin/${kind}/${row.id}`, options);
    if (["reading/materials", "listening/tests"].includes(kind))
      await api(`/admin/${kind}/${row.id}/preview`, options);
    const fields = kind.startsWith("reading")
      ? [
          "kind",
          "slug",
          "examType",
          "difficulty",
          "title",
          "description",
          "body",
          "durationMinutes",
          "sourceTitle",
          "sourceUrl",
          "questionGroups",
          "revision",
        ]
      : kind.startsWith("listening")
        ? [
            "slug",
            "examType",
            "title",
            "description",
            "durationMinutes",
            "parts",
            "revision",
          ]
        : kind.startsWith("writing")
          ? [
              "slug",
              "examType",
              "difficulty",
              "title",
              "description",
              "durationMinutes",
              "tasks",
              "revision",
            ]
          : [
              "slug",
              "examType",
              "difficulty",
              "title",
              "description",
              "parts",
              "revision",
            ];
    const input = Object.fromEntries(
      fields.filter((k) => k in full).map((k) => [k, full[k]]),
    );
    const updated = await api(`/admin/${kind}/${row.id}`, {
      ...options,
      method: "PUT",
      body: input,
    });
    await api(`/admin/${kind}/${row.id}/publish`, {
      ...options,
      method: "POST",
      body: { revision: updated.revision },
    });
  }
  await api("/admin/full-mocks", options);
});

test("provider encryption, protected native fetch, routing, order and statistics keep original contracts", async () => {
  await sql("__configure", [
    { AI_PROVIDER_ENCRYPTION_KEY: Buffer.alloc(32, 7).toString("base64") },
  ]);
  const options = { as: admin, role: "ADMIN" },
    input = {
      name: "Synthetic provider contract",
      endpoint: "https://provider-contract.example.invalid/v1/chat/completions",
      model: "synthetic-test",
      speakingModel: "synthetic-test",
      scopes: ["assistant", "writing", "speaking"],
      enabled: true,
      priority: 1,
      timeoutSeconds: 5,
      apiKey: "synthetic-provider-key",
    };
  let item = await api("/admin/ai-providers", {
    ...options,
    method: "POST",
    body: input,
    status: 201,
  });
  assert.equal(item.hasKey, true);
  assert.ok(!JSON.stringify(item).includes(input.apiKey));
  const encrypted = (
    await sql("SELECT hex(key_ciphertext) value FROM ai_providers WHERE id=?", [
      item.id,
    ])
  )[0];
  assert.ok(encrypted.value.length > 64);
  await api(`/admin/ai-providers/${item.id}/test`, {
    ...options,
    method: "POST",
  });
  await api("/admin/ai-providers/test", {
    ...options,
    method: "POST",
    body: input,
  });
  item = await api(`/admin/ai-providers/${item.id}`, {
    ...options,
    method: "PUT",
    body: {
      ...input,
      apiKey: "",
      name: "Updated contract",
      revision: item.revision,
    },
  });
  await api("/admin/ai-providers/order", {
    ...options,
    method: "POST",
    body: {
      items: (await api("/admin/ai-providers", options)).items.map((p) => ({
        id: p.id,
        revision: p.revision,
      })),
    },
  });
  const routing = (await api("/admin/ai-providers/routing", options)).routing;
  await api("/admin/ai-providers/routing", {
    ...options,
    method: "PUT",
    body: {
      ...routing,
      mode: "sequential",
      maxParallel: 1,
      hedgeDelayMs: 0,
      providerIds: [item.id],
    },
  });
  const reply = await api("/assistant/chat", {
    method: "POST",
    body: { messages: [{ role: "user", content: "Contract request" }] },
  });
  assert.ok(reply);
  await api("/admin/ai-providers/stats", options);
  const fresh = (await api("/admin/ai-providers", options)).items.find(
    (p) => p.id === item.id,
  );
  await api(`/admin/ai-providers/${item.id}?revision=${fresh.revision}`, {
    ...options,
    method: "DELETE",
    status: 204,
  });
  const limits = await api("/admin/ai-limits", {
    ...options,
    method: "PUT",
    body: {
      assistantLimit: 30,
      guestAssistantLimit: 3,
      writingLimit: 10,
      speakingLimit: 10,
    },
  });
  assert.equal(limits.speakingLimit, 10);
});
test("WhatsApp challenges preserve signed proofs, cooldown, failures and one-time confirmation", async () => {
  await sql("__configure", [
    {
      INFOBIP_ENABLED: "true",
      INFOBIP_BASE_URL: "https://infobip-contract.example.invalid",
      INFOBIP_API_KEY: "synthetic-infobip-key",
      INFOBIP_WHATSAPP_SENDER: "synthetic-sender",
      INFOBIP_WHATSAPP_TEMPLATE: "synthetic-template",
    },
  ]);
  const request = { phone: "+77000000003", purpose: "REGISTRATION" },
    sent = await api("/phone-verifications", {
      method: "POST",
      body: request,
      status: 202,
    });
  await api("/phone-verifications", {
    method: "POST",
    body: request,
    status: 429,
  });
  await api(`/phone-verifications/${sent.verificationId}/confirm`, {
    method: "POST",
    body: { ...request, code: "not-a-code" },
    status: 422,
  });
  const { code } = await sql("__code"),
    confirmed = await api(
      `/phone-verifications/${sent.verificationId}/confirm`,
      { method: "POST", body: { ...request, code } },
    );
  assert.ok(confirmed.verificationToken);
  await api(`/phone-verifications/${sent.verificationId}/confirm`, {
    method: "POST",
    body: { ...request, code },
    status: 422,
  });
});
test("editorial decisions persist immutable review history and enforce revisions", async () => {
  const options = { as: admin, role: "ADMIN" },
    catalog = await api("/admin/study-learning/content", options),
    item = catalog.items.find(
      (x) => x.kind === "drill" && x.id === "iac-r-library",
    );
  assert.ok(item);
  const input = {
    version: item.version,
    contentHash: item.contentHash,
    familyId: "contract-library-family",
    status: "reviewed",
    expectedRevision: item.metadata?.revision ?? 0,
    reviewNotes:
      "Synthetic editorial contract; no production content approval.",
    keysChecked: true,
    familyChecked: true,
    sourceChecked: true,
  };
  await api(`/admin/study-learning/content/${item.kind}/${item.id}`, {
    ...options,
    method: "PUT",
    body: input,
  });
  const history = await api(
    `/admin/study-learning/content/${item.kind}/${item.id}/history?version=${item.version}`,
    options,
  );
  assert.equal(history.items.length, 1);
  await api(`/admin/study-learning/content/${item.kind}/${item.id}`, {
    ...options,
    method: "PUT",
    body: input,
    status: 409,
  });
});
test("admin password hashing, revocation, role changes and deletion stay atomic", async () => {
  const id = randomUUID();
  await seedUser(id, "STUDENT");
  const options = { as: admin, role: "ADMIN" };
  const changed = await api(`/admin/users/${id}`, {
    ...options,
    method: "PUT",
    body: {
      displayName: "Updated contract",
      email: id + "@example.invalid",
      phone: "+77000000004",
      role: "STUDENT",
      permissions: [],
      blocked: false,
    },
  });
  assert.equal(changed.displayName, "Updated contract");
  const blocked = await api(`/admin/users/${id}`, {
    ...options,
    method: "PUT",
    body: {
      displayName: "Updated contract",
      email: id + "@example.invalid",
      phone: "+77000000004",
      role: "STUDENT",
      permissions: [],
      blocked: true,
    },
  });
  assert.equal(blocked.blocked, true);
  assert.equal(blocked.googleConnected, false);
  const before = performance.now();
  await api(`/admin/users/${id}/password`, {
    ...options,
    method: "POST",
    body: { password: "Synthetic password 123!" },
    status: 204,
  });
  console.log(
    "bcrypt default cost wall ms",
    Math.round(performance.now() - before),
  );
  const stored = (
    await sql("SELECT password_hash,token_version FROM users WHERE id=?", [id])
  )[0];
  assert.match(stored.password_hash, /^\$2[aby]\$10\$/);
  assert.ok(stored.token_version > 0);
  await api(`/admin/users/${id}/revoke-sessions`, {
    ...options,
    method: "POST",
    body: {},
    status: 204,
  });
  await api(`/admin/users/${id}`, {
    ...options,
    method: "DELETE",
    body: { email: id + "@example.invalid" },
    status: 204,
  });
  assert.equal(
    (await sql("SELECT count(*) n FROM users WHERE id=?", [id]))[0].n,
    0,
  );
  await api("/admin/super-admins", {
    ...options,
    method: "POST",
    body: { email: "synthetic-admin@example.invalid" },
    status: 201,
  });
  await api("/admin/super-admins/synthetic-admin%40example.invalid", {
    ...options,
    method: "DELETE",
    status: 204,
  });
});

test("compressed Speaking transcription uses a separate native Wasm Worker before original assessment", async () => {
  await sql("__configure", [
    {
      STT_API_URL:
        "https://stt-contract.example.invalid/v1/audio/transcriptions",
    },
  ]);
  execFileSync(
    "ffmpeg",
    [
      "-v",
      "error",
      "-y",
      "-f",
      "lavfi",
      "-i",
      "sine=frequency=440:duration=2",
      "-c:a",
      "libopus",
      ".artifacts/smoke-recording.webm",
    ],
    { stdio: "pipe" },
  );
  const original = await api("/admin/speaking/materials/" + speakingID, {
      as: admin,
      role: "ADMIN",
    }),
    clone = await api("/admin/speaking/materials", {
      method: "POST",
      as: admin,
      role: "ADMIN",
      body: {
        ...materialInput("speaking", original),
        slug: "compressed-native-contract",
        revision: 0,
      },
      status: 201,
    });
  await api(`/admin/speaking/materials/${clone.id}/publish`, {
    method: "POST",
    as: admin,
    role: "ADMIN",
    body: { revision: clone.revision },
  });
  const material = await api(`/speaking/materials/${clone.id}`),
    started = await api(`/speaking/materials/${clone.id}/attempts`, {
      method: "POST",
      status: 201,
    }),
    form = new FormData();
  form.set("partId", material.parts[0].id);
  form.set(
    "recording",
    new Blob([await readFile(".artifacts/smoke-recording.webm")], {
      type: "audio/webm",
    }),
    "contract.webm",
  );
  const uploaded = await call(
    `/api/v1/attempts/${started.attempt.id}/recordings`,
    {
      method: "POST",
      headers: { authorization: "Bearer " + token() },
      body: form,
    },
  );
  assert.equal(uploaded.status, 201, await uploaded.clone().text());
  await uploaded.text();
  const done = await api(`/attempts/${started.attempt.id}/submit`, {
    method: "POST",
    body: {
      answers: material.parts.slice(1).map((p) => ({
        questionId: p.id,
        answer: { value: "A synthetic typed transcript." },
      })),
    },
  });
  assert.equal(done.status, "SUBMITTED");
  const saved = (
    await sql(
      "SELECT answer FROM attempt_answers WHERE attempt_id=? AND question_id=?",
      [done.id, material.parts[0].id],
    )
  )[0];
  assert.equal(
    JSON.parse(saved.answer).value,
    "A synthetic native transcription.",
  );
});
test("analytics CSV exports stream all four real datasets", async () => {
  for (const dataset of ["users", "attempts", "activity", "full_mocks"]) {
    const response = await call("/api/v1/admin/analytics/export/" + dataset, {
      headers: { authorization: "Bearer " + token(admin, "ADMIN") },
    });
    assert.equal(response.status, 200, await response.clone().text());
    const text = await response.text();
    assert.ok(
      text.startsWith(dataset === "activity" ? "day," : "id,"),
      text.slice(0, 100),
    );
    assert.ok(text.includes("\n"));
  }
});

test("writer applications, certificates, moderation and posts preserve ownership and cleanup", async () => {
  const applicant = randomUUID();
  await seedUser(applicant, "STUDENT");
  const options = { as: admin, role: "ADMIN" };
  const certificate = await upload(
    "/blog/media",
    png,
    "certificate.png",
    "image/png",
    { kind: "certificate" },
    applicant,
    "STUDENT",
  );
  const application = await api("/writers/applications", {
    method: "POST",
    as: applicant,
    body: {
      overallBand: 8,
      readingBand: 8,
      writingBand: 8,
      speakingBand: 8,
      trfNumber: "SYNTHETIC123",
      bio: "Technical contract fixture",
      certificateMediaId: certificate.id,
    },
    status: 201,
  });
  const mine = await api("/writers/applications/mine", { as: applicant });
  assert.equal(mine.application.id, application.id);
  await api("/admin/writers/applications", options);
  await api("/admin/writers/applications/" + application.id, options);
  const file = await call(
    `/api/v1/writers/applications/${application.id}/certificate`,
    { headers: { authorization: "Bearer " + token(applicant) } },
  );
  assert.equal(file.status, 200);
  assert.deepEqual(Buffer.from(await file.arrayBuffer()), png);
  await api(`/writers/applications/${application.id}/certificate`, {
    as: other,
    status: 404,
  });
  const approved = await api(
    `/admin/writers/applications/${application.id}/approve`,
    {
      ...options,
      method: "POST",
      body: { notes: "Synthetic review contract" },
    },
  );
  assert.equal(approved.status, "APPROVED");
  await api(`/admin/writers/applications/${application.id}/reject`, {
    ...options,
    method: "POST",
    body: { notes: "Cannot replace a completed decision" },
    status: 404,
  });
  const author = { as: applicant, role: "WRITER" },
    input = {
      slug: "contract-writer-owned",
      title: "Contract writer post",
      description: "Technical fixture",
      bodyHtml: "<p>" + "Contract article. ".repeat(10) + "</p>",
      bodyJson: { type: "doc", content: [] },
    };
  let post = await api("/writer/posts", {
    ...author,
    method: "POST",
    body: input,
    status: 201,
  });
  post = await api(`/writer/posts/${post.id}`, {
    ...author,
    method: "PUT",
    body: { ...input, title: "Edited contract" },
  });
  post = await api(`/admin/blog/posts/${post.id}`, {
    ...options,
    method: "PUT",
    body: { ...input, revision: post.revision, title: "Admin contract edit" },
  });
  post = await api(`/admin/blog/posts/${post.id}/publish`, {
    ...options,
    method: "POST",
    body: { revision: post.revision },
  });
  await api(`/writer/posts/${post.id}/archive`, {
    ...author,
    method: "POST",
    body: {},
  });
  const originalPost = (
      await sql("SELECT id FROM blog_posts WHERE slug='contract-blog'")
    )[0],
    original = await api("/admin/blog/posts/" + originalPost.id, options);
  await api(`/admin/blog/posts/${original.id}/archive`, {
    ...options,
    method: "POST",
    body: { revision: original.revision },
  });
  const media = await upload("/blog/media");
  const image = await call("/api/v1/blog/media/" + media.id, {
    headers: { authorization: "Bearer " + token(admin, "ADMIN") },
  });
  assert.equal(image.status, 200);
  assert.deepEqual(Buffer.from(await image.arrayBuffer()), png);
  await api(`/admin/users/${applicant}`, {
    ...options,
    method: "DELETE",
    body: { email: applicant + "@example.invalid" },
    status: 204,
  });
  assert.equal(
    (await sql("SELECT count(*) n FROM account_object_deletions"))[0].n,
    0,
  );
});
function materialInput(kind, full) {
  const common = ["slug", "examType", "title", "description", "revision"],
    fields =
      kind === "reading"
        ? [
            ...common,
            "kind",
            "difficulty",
            "body",
            "durationMinutes",
            "sourceTitle",
            "sourceUrl",
            "questionGroups",
          ]
        : kind === "listening"
          ? [...common, "durationMinutes", "parts"]
          : kind === "writing"
            ? [...common, "difficulty", "durationMinutes", "tasks"]
            : [...common, "difficulty", "parts"];
  return Object.fromEntries(
    fields.filter((k) => k in full).map((k) => [k, full[k]]),
  );
}
test("all material import and archive contracts retain deterministic native parsing and version writes", async () => {
  const options = { as: admin, role: "ADMIN" };
  for (const kind of ["reading", "listening"]) {
    const parsed = await api(`/admin/${kind}/import/parse`, {
      ...options,
      method: "POST",
      body: { source: "invalid technical fixture", examType: "academic" },
    });
    assert.ok(parsed.errors.length);
  }
  for (const kind of ["listening", "writing", "speaking"]) {
    const collection = kind === "listening" ? "tests" : "materials",
      table = kind === "listening" ? "listening_tests" : kind + "_materials",
      id = (await sql(`SELECT id FROM ${table} ORDER BY created_at LIMIT 1`))[0]
        .id;
    const full = await api(`/admin/${kind}/${collection}/${id}`, options),
      input = { ...materialInput(kind, full), slug: "import-contract-" + kind };
    delete input.revision;
    if (kind !== "listening") {
      const parsed = await api(`/admin/${kind}/import/parse`, {
        ...options,
        method: "POST",
        body: {
          source: JSON.stringify({
            format: "IELTS_SPEAKING_IMPORT_V1",
            materials: [input],
          }),
        },
      });
      assert.equal(parsed.errors.length, 0);
      assert.equal(parsed.materials.length, 1);
    }
    const result = await api(`/admin/${kind}/import`, {
      ...options,
      method: "POST",
      body: kind === "listening" ? input : { materials: [input] },
      status: 201,
    });
    const imported = kind === "listening" ? result : result.items[0];
    await api(`/admin/${kind}/${collection}/${imported.id}/archive`, {
      ...options,
      method: "POST",
      body: { revision: imported.revision },
    });
  }
  const reading = (
      await sql(
        "SELECT id FROM reading_materials WHERE slug='contract-reading'",
      )
    )[0],
    item = await api("/admin/reading/materials/" + reading.id, options);
  await api(`/admin/reading/materials/${reading.id}/archive`, {
    ...options,
    method: "POST",
    body: { revision: item.revision },
  });
});
test("study plan assignment actions and weekly mock offers persist revisions", async () => {
  const weekday = new Intl.DateTimeFormat("en-US", {
    timeZone: "Asia/Almaty",
    weekday: "short",
  }).format(new Date());
  const day =
    ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"].indexOf(weekday) + 1;
  let plan = await api("/study-plan");
  plan = await api("/study-plan/settings", {
    method: "PUT",
    body: {
      dailyMinutes: 90,
      studyDays: [day],
      weeklyMockEnabled: true,
      expectedRevision: plan.plan.revision,
    },
  });
  const today = plan.plan.days.find((d) => d.date === plan.today);
  assert.ok(today);
  assert.ok(today.assignments.length);
  const assignment =
    today.assignments.find((a) => a.kind === "REVIEW") ?? today.assignments[0];
  await api(`/study-plan/assignments/${assignment.id}/start`, {
    method: "POST",
    body: { expectedRevision: plan.plan.revision },
    status: assignment.drillId ? 409 : 200,
  });
  plan = await api("/study-plan");
  await api(`/study-plan/assignments/${assignment.id}/review`, {
    method: "POST",
    body: { expectedRevision: plan.plan.revision },
    status: assignment.kind === "REVIEW" ? 200 : 409,
  });
  plan = await api("/study-plan");
  const skip = plan.plan.days
    .find((d) => d.date === plan.today)
    .assignments.find((a) => !["COMPLETED", "SKIPPED"].includes(a.status));
  assert.ok(skip);
  await api(`/study-plan/assignments/${skip.id}/skip`, {
    method: "POST",
    body: { expectedRevision: plan.plan.revision, reason: "no_time" },
  });
  plan = await api("/study-plan");
  await api("/study-plan/weeks/1/mock", {
    method: "POST",
    body: { expectedRevision: plan.plan.revision, action: "decline" },
  });
});

test("pending Google registration and attempt aliases retain their signed and pinned contracts", async () => {
  const pending = await api("/auth/google", {
    method: "POST",
    body: { googleToken: "synthetic-waitlist" },
  });
  assert.ok(pending.registrationToken);
  const restored = await api("/auth/google/pending", {
    headers: {
      cookie: "ielts_refresh_google_registration=" + pending.registrationToken,
    },
  });
  assert.ok(restored.profile);
  const row = (
    await sql(
      "SELECT id FROM attempts WHERE user_id=? AND material_type='reading' AND status='SUBMITTED' ORDER BY started_at LIMIT 1",
      [uid],
    )
  )[0];
  await api(`/attempts/${row.id}/mistakes`);
  const material = (
    await sql(
      "SELECT id FROM listening_tests WHERE status='PUBLISHED' AND NOT EXISTS(SELECT 1 FROM guest_mock_materials g WHERE g.material_id=listening_tests.id) LIMIT 1",
    )
  )[0];
  // The guest exam reserves its selected bank; use a distinct published clone.
  const options = { as: admin, role: "ADMIN" },
    source = await api("/admin/listening/tests/" + listeningID, options),
    clone = await api("/admin/listening/import", {
      ...options,
      method: "POST",
      body: {
        ...materialInput("listening", source),
        slug: "answers-alias-contract",
        revision: 0,
      },
      status: 201,
    });
  await api(`/admin/listening/tests/${clone.id}/publish`, {
    ...options,
    method: "POST",
    body: { revision: clone.revision },
  });
  const started = await api(`/listening/tests/${clone.id}/attempts`, {
    method: "POST",
    status: 201,
  });
  await api(`/attempts/${started.attempt.id}/answers`, {
    method: "POST",
    body: { answers: [] },
  });
});
test("Listening transcription streams R2 media through the original STT and version save contracts", async () => {
  execFileSync(
    "ffmpeg",
    [
      "-v",
      "error",
      "-y",
      "-f",
      "lavfi",
      "-i",
      "sine=frequency=440:duration=2",
      ".artifacts/listening-contract.wav",
    ],
    { stdio: "pipe" },
  );
  const audio = await upload(
      "/admin/listening/media",
      await readFile(".artifacts/listening-contract.wav"),
      "contract.wav",
      "audio/wav",
      { kind: "audio" },
    ),
    options = { as: admin, role: "ADMIN" },
    source = await api("/admin/listening/tests/" + listeningID, options),
    input = materialInput("listening", source);
  input.slug = "transcribe-contract";
  input.revision = 0;
  input.parts = input.parts.map((p) => ({ ...p, audioAssetId: audio.id }));
  const created = await api("/admin/listening/import", {
      ...options,
      method: "POST",
      body: input,
      status: 201,
    }),
    transcribed = await api(`/admin/listening/tests/${created.id}/transcribe`, {
      ...options,
      method: "POST",
      body: {},
    });
  assert.equal(
    transcribed.parts[0].transcript,
    "A synthetic native transcription.",
  );
  assert.equal(transcribed.parts[0].transcriptSegments.length, 1);
});
test("legacy full-mock admin records retain detail and revision archive contracts", async () => {
  const id = randomUUID(),
    reading = (
      await sql(
        "SELECT id FROM reading_materials WHERE material_kind='TEST' LIMIT 1",
      )
    )[0].id,
    writing = (await sql("SELECT id FROM writing_materials LIMIT 1"))[0].id;
  await sql(
    "INSERT INTO full_mock_tests(id,slug,status,exam_type,title,listening_material_id,reading_material_id,writing_material_id,speaking_material_id,published_at) VALUES(?,?,'PUBLISHED','academic','Contract mock',?,?,?,?,?)",
    [
      id,
      "legacy-contract-mock",
      listeningID,
      reading,
      writing,
      speakingID,
      "2026-10-01T00:00:00.000000000Z",
    ],
  );
  const options = { as: admin, role: "ADMIN" },
    mock = await api("/admin/full-mocks/" + id, options);
  assert.equal(mock.id, id);
  await api(`/admin/full-mocks/${id}/archive`, {
    ...options,
    method: "POST",
    body: { revision: mock.revision },
  });
});

test("Durable Object reset keeps persisted identities and reattaches a live Wasm runtime", async () => {
  const before = await api("/profile");
  try {
    const response = await call("/__test", {
      method: "POST",
      body: JSON.stringify({ sql: "__reset" }),
    });
    await response.text();
    assert.ok(response.status >= 500);
  } catch (e) {
    assert.ok(e);
  }
  const after = await api("/profile");
  assert.equal(after.userId ?? after.id, before.userId ?? before.id);
  assert.equal(after.displayName, before.displayName);
});

test("a real graded attempt produces an executable review assignment and durable completion", async () => {
  const learner = randomUUID();
  await seedUser(learner, "STUDENT");
  const row = (
      await sql(
        "SELECT id FROM listening_tests WHERE status='PUBLISHED' AND NOT EXISTS(SELECT 1 FROM guest_mock_materials g WHERE g.material_id=listening_tests.id) ORDER BY created_at LIMIT 1",
      )
    )[0],
    material = await api("/listening/tests/" + row.id, { as: learner }),
    started = await api(`/listening/tests/${row.id}/attempts`, {
      method: "POST",
      as: learner,
      status: 201,
    });
  const questions = material.parts.flatMap((p) =>
    p.groups.flatMap((g) => g.questions),
  );
  await api(`/attempts/${started.attempt.id}/submit`, {
    method: "POST",
    as: learner,
    body: {
      answers: questions.map((q, i) => ({
        questionId: q.id,
        answer: { value: i === 0 ? "wrong" : "source" },
      })),
    },
  });
  const yesterday = new Date(Date.now() - 3 * 86400000)
    .toISOString()
    .replace(/\.(\d{3})Z$/, ".$1000000Z");
  await sql("UPDATE attempts SET started_at=?,submitted_at=? WHERE id=?", [
    yesterday,
    yesterday,
    started.attempt.id,
  ]);
  let plan = await api("/study-plan", {
      method: "POST",
      as: learner,
      body: {
        dailyMinutes: 90,
        studyDays: [1, 2, 3, 4, 5, 6, 7],
        weeklyMockEnabled: false,
      },
    }),
    review = plan.plan.days
      .find((d) => d.date === plan.today)
      .assignments.find((a) => a.kind === "REVIEW");
  assert.ok(review);
  const target = await api(`/study-plan/assignments/${review.id}/start`, {
    method: "POST",
    as: learner,
    body: { expectedRevision: plan.plan.revision },
  });
  assert.equal(target.attemptId, started.attempt.id);
  plan = await api("/study-plan", { as: learner });
  const completed = await api(`/study-plan/assignments/${review.id}/review`, {
    method: "POST",
    as: learner,
    body: { expectedRevision: plan.plan.revision },
  });
  assert.equal(
    completed.plan.days
      .flatMap((d) => d.assignments)
      .find((a) => a.id === review.id).status,
    "COMPLETED",
  );
});
test("a pending writer application can be rejected without granting writer privileges", async () => {
  const applicant = randomUUID();
  await seedUser(applicant, "STUDENT");
  const certificate = await upload(
      "/blog/media",
      png,
      "certificate.png",
      "image/png",
      { kind: "certificate" },
      applicant,
      "STUDENT",
    ),
    application = await api("/writers/applications", {
      method: "POST",
      as: applicant,
      status: 201,
      body: {
        overallBand: 8,
        readingBand: 8,
        writingBand: 8,
        speakingBand: 8,
        trfNumber: "SYNTHETIC123",
        bio: "Technical fixture",
        certificateMediaId: certificate.id,
      },
    });
  const rejected = await api(
    `/admin/writers/applications/${application.id}/reject`,
    {
      as: admin,
      role: "ADMIN",
      method: "POST",
      body: { notes: "Synthetic rejection contract" },
    },
  );
  assert.equal(rejected.status, "REJECTED");
  assert.equal(
    (await sql("SELECT role FROM users WHERE id=?", [applicant]))[0].role,
    "STUDENT",
  );
});

test("valid JPEG with large metadata is checked through bounded R2 reads and retains all bytes", async () => {
  execFileSync(
    "ffmpeg",
    [
      "-v",
      "error",
      "-y",
      "-f",
      "lavfi",
      "-i",
      "color=red:s=32x32",
      "-frames:v",
      "1",
      ".artifacts/jpeg-contract.jpg",
    ],
    { stdio: "pipe" },
  );
  const jpeg = await readFile(".artifacts/jpeg-contract.jpg"),
    app = Buffer.alloc(65537, 0);
  app[0] = 0xff;
  app[1] = 0xe1;
  app.writeUInt16BE(65535, 2);
  const bytes = Buffer.concat([
      jpeg.subarray(0, 2),
      ...Array.from({ length: 96 }, () => app),
      jpeg.subarray(2),
    ]),
    before = await sql("__counts"),
    media = await upload(
      "/admin/writing/media",
      bytes,
      "large-metadata.jpg",
      "image/jpeg",
    ),
    after = await sql("__counts");
  assert.ok((after.r2range ?? 0) - (before.r2range ?? 0) < 20);
  const response = await call("/api/v1/writing/media/" + media.id, {
    headers: { authorization: "Bearer " + token(admin, "ADMIN") },
  });
  assert.equal(response.status, 200);
  assert.equal(
    createHash("sha256")
      .update(Buffer.from(await response.arrayBuffer()))
      .digest("hex"),
    createHash("sha256").update(bytes).digest("hex"),
  );
});

test("HEAD keeps the original method status without queuing an unread body", async () => {
  const response = await call("/health/live", { method: "HEAD" });
  assert.equal(response.status, 405);
  assert.equal((await response.arrayBuffer()).byteLength, 0);
  const next = await call("/health/live");
  assert.equal(next.status, 200);
  await next.text();
});

test("derived grading cache is rebuilt natively; Prometheus stays on a private binding", async () => {
  const row = (
    await sql(
      "SELECT id FROM attempts WHERE user_id=? AND material_type='reading' AND status='SUBMITTED' ORDER BY started_at LIMIT 1",
      [uid],
    )
  )[0];
  await api(`/attempts/${row.id}/mistakes`);
  assert.ok(
    (
      await sql(
        "SELECT count(*) n FROM cf_coordination WHERE key LIKE 'iac:grading:v1:%'",
      )
    )[0].n > 0,
  );
  const response = await call("/__test", {
    method: "POST",
    body: JSON.stringify({ sql: "__metrics" }),
  });
  assert.equal(response.status, 200);
  const text = await response.text();
  assert.match(text, /iac_jobs_scrape_up 1/);
  assert.match(text, /iac_http_requests_total/);
  const publicMetrics = await call("/metrics");
  assert.equal(publicMetrics.status, 404);
  await publicMetrics.text();
});
