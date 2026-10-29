import { test } from "node:test";
import assert from "node:assert/strict";
import { Miniflare, convertV4MiniflareOptions } from "miniflare";
import { fileURLToPath } from "node:url";

test("existing coordination and blob leases retain results while expiry reads stay bounded", async () => {
  const mf = new Miniflare(
    convertV4MiniflareOptions({
      modules: [
        "./read-budget-entry.mjs",
        "../runtime/redis.mjs",
        "../runtime/hll.mjs",
        "../runtime/blob-worker.mjs",
        "../runtime/blob-store.mjs",
      ].map((path) => ({
        type: "ESModule",
        path: fileURLToPath(new URL(path, import.meta.url)),
      })),
      modulesRoot: fileURLToPath(new URL("../", import.meta.url)),
      compatibilityDate: "2026-10-19",
      durableObjects: {
        PROBE: { className: "ReadBudgetProbe", useSQLite: true },
      },
    }),
  );
  try {
    for (const count of [68, 10000]) {
      const response = await mf.dispatchFetch(
        `https://probe.test/?count=${count}`,
      );
      assert.equal(response.status, 200);
      const { before, after } = await response.json();
      for (const key of [
        "coordination",
        "leases",
        "expired",
        "temporaryObjects",
      ]) {
        assert.deepEqual(after[key].rows, before[key].rows, key);
        assert.ok(
          before[key].rowsRead >= count,
          `${key}: baseline must expose a full scan`,
        );
      }
      assert.ok(
        after.coordination.rowsRead <= 2,
        "coordination minimum is indexed",
      );
      assert.ok(after.leases.rowsRead <= 2, "lease minimum is indexed");
      assert.ok(
        after.expired.rowsRead <= 20,
        "expiry selection reads only matching rows",
      );
      assert.ok(
        after.temporaryObjects.rowsRead <= 20,
        "temporary cleanup skips permanent objects",
      );
      console.log(
        JSON.stringify({
          count,
          before: Object.fromEntries(
            Object.entries(before).map(([k, v]) => [k, v.rowsRead]),
          ),
          after: Object.fromEntries(
            Object.entries(after).map(([k, v]) => [k, v.rowsRead]),
          ),
        }),
      );
    }
  } finally {
    await mf.dispose();
  }
});
