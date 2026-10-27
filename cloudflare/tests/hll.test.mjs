import { test } from "node:test";
import assert from "node:assert/strict";
import { spawn, execFileSync } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { emptyHLL, addHLL, countHLL, importHLL } from "../runtime/hll.mjs";
test("native HLL reproduces an actual isolated Redis oracle, including sparse/dense import and future additions", async () => {
  const dir = mkdtempSync(join(tmpdir(), "iac-hll-")),
    socket = join(dir, "redis.sock");
  const redis = spawn(
    "redis-server",
    ["--port", "0", "--unixsocket", socket, "--save", "", "--appendonly", "no"],
    { stdio: ["ignore", "pipe", "pipe"] },
  );
  const closed = new Promise((resolve) => redis.once("close", resolve));
  try {
    await new Promise((resolve, reject) => {
      const timer = setTimeout(
        () => reject(Error("Isolated Redis oracle did not start")),
        10000,
      );
      redis.on("error", (e) => {
        clearTimeout(timer);
        reject(e);
      });
      redis.once("exit", () => {
        clearTimeout(timer);
        reject(Error("Isolated Redis oracle exited before startup"));
      });
      redis.stdout.on("data", (b) => {
        if (b.toString().includes("Ready to accept connections")) {
          clearTimeout(timer);
          resolve();
        }
      });
    });
    const cli = (...args) =>
      execFileSync("redis-cli", ["-s", socket, "--raw", ...args]);
    let state = emptyHLL();
    for (const n of [10, 500, 5000]) {
      const values = Array.from(
        { length: n },
        (_, i) => "contract-" + n + "-" + i,
      );
      cli("PFADD", "test", ...values);
      state = addHLL(state, values).state;
      assert.equal(
        countHLL([state]),
        Number(cli("PFCOUNT", "test").toString()),
      );
      const raw = cli("GET", "test").subarray(0, -1),
        imported = importHLL(raw.toString("base64"));
      assert.deepEqual(imported, state);
      assert.equal(
        addHLL(imported, ["future"]).state.hll,
        addHLL(state, ["future"]).state.hll,
      );
    }
  } finally {
    redis.kill("SIGTERM");
    await closed;
    rmSync(dir, { recursive: true, force: true });
  }
});
