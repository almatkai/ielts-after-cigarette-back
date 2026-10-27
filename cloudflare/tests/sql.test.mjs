import { test } from "node:test";
import assert from "node:assert/strict";
import { DatabaseSync } from "node:sqlite";
import { translate } from "../runtime/sql.mjs";

test("PostgreSQL extrema skip NULLs and remain scalar for one argument", () => {
  const db = new DatabaseSync(":memory:");
  try {
    for (const [source, args, expected] of [
      ["GREATEST($1,$2,$3)", [null, 4, 9], 9],
      ["LEAST($1,$2,$3)", [4, null, -9], -9],
      ["GREATEST($1,$2)", [null, null], null],
      ["LEAST($1,$2)", [null, null], null],
      ["GREATEST($1)", [7], 7],
      ["LEAST($1)", [null], null],
      ["GREATEST($1,LEAST($2,$3))", [null, 9, 4], 4],
      ["GREATEST($1,$2)", ["2026-10-01", null], "2026-10-01"],
      ["LEAST($1,$2)", ["2026-10-01", "2026-09-30"], "2026-09-30"],
    ]) {
      const query = translate(`SELECT ${source} AS result`, args);
      assert.equal(db.prepare(query.sql).get(...query.args).result, expected);
    }
    const query = translate(
      "SELECT GREATEST(n) AS result FROM (SELECT 1 AS n UNION ALL SELECT 2)",
    );
    assert.deepEqual(
      db
        .prepare(query.sql)
        .all()
        .map((row) => row.result),
      [1, 2],
    );
  } finally {
    db.close();
  }
});
