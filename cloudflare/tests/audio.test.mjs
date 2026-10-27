import { fileURLToPath } from "node:url";
process.chdir(fileURLToPath(new URL("..", import.meta.url)));
import { test, after } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFile, readdir, mkdir } from "node:fs/promises";
import { Miniflare, convertV4MiniflareOptions } from "miniflare";
const dir = ".artifacts/audio-test";
execFileSync(
  process.execPath,
  [
    "node_modules/wrangler/bin/wrangler.js",
    "deploy",
    "--dry-run",
    "--config",
    "wrangler.audio.jsonc",
    "--outdir",
    dir,
    "tests/audio-entry.mjs",
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
const mf = new Miniflare(
  convertV4MiniflareOptions({
    modules: [
      { type: "ESModule", path: dir + "/audio-entry.js" },
      ...(await readdir(dir))
        .filter((f) => f.endsWith(".wasm"))
        .map((f) => ({ type: "CompiledWasm", path: dir + "/" + f })),
    ],
    modulesRoot: dir,
    compatibilityDate: "2026-10-19",
    durableObjects: { IAC_AUDIO: { className: "IACAudio", useSQLite: true } },
    r2Buckets: ["MEDIA"],
  }),
);
after(() => mf.dispose());
const bucket = await mf.getR2Bucket("MEDIA");
for (const [ext, codec, duration] of [
  ["webm", "libopus", 800],
  ["ogg", "libopus", 2],
  ["m4a", "aac", 2],
])
  test(
    "real workerd decodes " + ext + " into bounded 16kHz mono WAV",
    async () => {
      const file = ".artifacts/tone." + ext;
      execFileSync(
        "ffmpeg",
        [
          "-v",
          "error",
          "-y",
          "-f",
          "lavfi",
          "-i",
          `sine=frequency=440:duration=${duration}`,
          "-ac",
          "2",
          "-c:a",
          codec,
          file,
        ],
        { stdio: "pipe" },
      );
      const bytes = await readFile(file);
      await bucket.put("contract/" + ext, bytes);
      const response = await mf.dispatchFetch("https://audio.test", {
        method: "POST",
        body: "contract/" + ext,
      });
      assert.equal(response.status, 200, await response.clone().text());
      const result = await response.json();
      assert.ok(result.memory < 100 * 1024 * 1024);
      assert.ok(result.memory > 0);
      const converted = await bucket.get(result.key),
        wav = Buffer.from(await converted.arrayBuffer());
      assert.equal(wav.toString("ascii", 0, 4), "RIFF");
      assert.equal(wav.readUInt32LE(24), 16000);
      assert.equal(wav.readUInt16LE(22), 1);
      assert.equal(wav.length, result.size);
      assert.ok(
        Math.abs((wav.length - 44) / 32000 - duration) < 0.1,
        `duration ${(wav.length - 44) / 32000} vs ${duration}`,
      );
      let dataAt = 12;
      while (wav.toString("ascii", dataAt, dataAt + 4) !== "data") {
        const size = wav.readUInt32LE(dataAt + 4);
        dataAt += 8 + size + (size % 2);
        assert.ok(dataAt < wav.length);
      }
      dataAt += 8;
      execFileSync(
        "ffmpeg",
        [
          "-v",
          "error",
          "-y",
          "-i",
          file,
          "-f",
          "s16le",
          "-ar",
          "16000",
          "-ac",
          "1",
          file + ".pcm",
        ],
        { stdio: "pipe" },
      );
      const reference = await readFile(file + ".pcm");
      if (ext === "m4a")
        assert.ok(
          Math.abs(wav.length - dataAt - reference.length) <= 2048,
          "AAC decoder padding exceeds one frame",
        );
      else assert.equal(wav.length - dataAt, reference.length);
      let energy = 0;
      for (let i = dataAt; i < wav.length; i += 2)
        energy += wav.readInt16LE(i) ** 2;
      assert.ok(Math.sqrt(energy / ((wav.length - dataAt) / 2)) > 1000);
      console.log(
        ext,
        "input",
        bytes.length,
        "WAV",
        wav.length,
        "Wasm memory",
        result.memory,
      );
    },
  );
