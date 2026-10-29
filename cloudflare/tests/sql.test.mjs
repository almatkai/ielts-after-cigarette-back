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

test("PostgreSQL billing expressions run on native SQLite", () => {
  const db = new DatabaseSync(":memory:");
  try {
    // clock_timestamp() is the transaction-clock spelling used by billing.
    const clock = translate("SELECT clock_timestamp() AS value");
    assert.equal(clock.sql, "SELECT strftime('%Y-%m-%dT%H:%M:%f000000Z','now') AS value");
    assert.match(db.prepare(clock.sql).get().value, /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{9}Z$/);

    // jsonb_set(payload,'{notification}',...) becomes a native JSON patch.
    const patch = translate(
      "SELECT jsonb_set($1,'{notification}',$2::jsonb) AS value",
      [JSON.stringify({ kept: true }), JSON.stringify({ status: "PAID" })],
    );
    assert.deepEqual(JSON.parse(db.prepare(patch.sql).get(...patch.args).value), {
      kept: true,
      notification: { status: "PAID" },
    });

    // Escape string newline separators keep their meaning in concat_ws.
    const note = translate(
      "SELECT concat_ws(E'\\n',NULLIF($1,''),$2) AS value",
      ["first", "second"],
    );
    assert.equal(db.prepare(note.sql).get(...note.args).value, "first\nsecond");
    const empty = translate(
      "SELECT concat_ws(E'\\n',NULLIF($1,''),$2) AS value",
      ["", "second"],
    );
    assert.equal(db.prepare(empty.sql).get(...empty.args).value, "second");
  } finally {
    db.close();
  }
});
