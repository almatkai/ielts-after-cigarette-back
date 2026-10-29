import { test } from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { DatabaseSync } from "node:sqlite";
import { applySchemaUpgrade, applySchemaRevert } from "../runtime/schema-upgrade.mjs";
import schema from "../schema/sqlite.json" with { type: "json" };
import deployed from "../schema/history/sqlite-9f195b8e3a83f584953e70de4456a96ab6912a20e8f8c20481024e6e6b9e7ed6.json" with {
  type: "json",
};

// Minimal stand-in for Durable Object SQL storage backed by node:sqlite.
function storage(db) {
  return {
    sql: {
      exec(statement, ...args) {
        const rows = db.prepare(statement).all(...args);
        return { toArray: () => rows };
      },
    },
    transactionSync(fn) {
      db.exec("BEGIN");
      try {
        fn();
        db.exec("COMMIT");
      } catch (error) {
        db.exec("ROLLBACK");
        throw error;
      }
    },
  };
}

function install(db, source) {
  db.exec("CREATE TABLE cf_schema(version TEXT PRIMARY KEY)");
  for (const statement of source.statements) db.exec(statement);
  for (const seed of source.seeds)
    for (const row of seed.rows) {
      const columns = Object.keys(row);
      db.prepare(
        `INSERT INTO ${seed.table}(${columns.join(",")}) VALUES(${columns.map(() => "?").join(",")})`,
      ).run(...Object.values(row));
    }
  db.exec(`INSERT INTO cf_schema VALUES('${source.version}')`);
}

function schemaObjects(db) {
  return db
    .prepare(
      "SELECT type,name,sql FROM sqlite_master WHERE sql IS NOT NULL AND name NOT LIKE 'cf_%' AND name NOT LIKE 'sqlite_%' ORDER BY type,name",
    )
    .all()
    .map((row) => ({ type: row.type, name: row.name, sql: row.sql }));
}

test("the deployed DEV schema upgrades additively without losing imported rows", () => {
  const live = new DatabaseSync(":memory:");
  try {
    live.exec("PRAGMA foreign_keys=ON");
    install(live, deployed);
    live.exec(
      "CREATE TABLE cf_coordination(key TEXT PRIMARY KEY,value TEXT NOT NULL,expires INTEGER)",
    );
    live.exec("INSERT INTO cf_coordination VALUES('k','v',1)");
    live.exec(
      "INSERT INTO users(id,email,role) VALUES('u1','learner@example.test','STUDENT')",
    );
    live.exec(
      "INSERT INTO user_profiles(user_id,display_name) VALUES('u1','Existing learner')",
    );
    live.exec("UPDATE system_daily_limits SET assistant_limit=42 WHERE id=1");

    assert.equal(
      live.prepare("SELECT version FROM cf_schema").get().version,
      deployed.version,
    );
    assert.equal(
      live
        .prepare("SELECT count(*) AS n FROM sqlite_master WHERE name='billing_plans'")
        .get().n,
      0,
    );

    assert.ok(applySchemaUpgrade(storage(live), schema) > 0);

    // Imported application data and untouched coordination state survive.
    assert.equal(
      live
        .prepare("SELECT display_name FROM user_profiles WHERE user_id='u1'")
        .get().display_name,
      "Existing learner",
    );
    assert.equal(live.prepare("SELECT count(*) AS n FROM users").get().n, 1);
    assert.equal(
      live.prepare("SELECT count(*) AS n FROM cf_coordination").get().n,
      1,
    );
    // Existing seed rows are never rewritten by an upgrade.
    assert.equal(
      live
        .prepare("SELECT assistant_limit FROM system_daily_limits WHERE id=1")
        .get().assistant_limit,
      42,
    );
    // New seed rows are installed exactly as a fresh target would have them.
    assert.deepEqual(
      live
        .prepare(
          "SELECT code,list_price_kzt,sale_price_kzt FROM billing_plans ORDER BY sort_order",
        )
        .all()
        .map((row) => [row.code, row.list_price_kzt, row.sale_price_kzt]),
      [
        ["self", 9990, 4990],
        ["group", 24990, 14990],
        ["personal", 54990, 34990],
      ],
    );
    assert.equal(
      live
        .prepare("SELECT free_reading FROM billing_settings WHERE id=1")
        .get().free_reading,
      3,
    );
    assert.equal(
      live.prepare("SELECT version FROM cf_schema").get().version,
      schema.version,
    );

    // The upgraded database is identical in schema terms to a fresh install.
    const fresh = new DatabaseSync(":memory:");
    try {
      install(fresh, schema);
      assert.deepEqual(schemaObjects(live), schemaObjects(fresh));
    } finally {
      fresh.close();
    }

    // The deployed schema really is the committed history snapshot.
    assert.equal(
      createHash("sha256")
        .update(JSON.stringify([deployed.statements, deployed.seeds]))
        .digest("hex"),
      deployed.version,
    );
  } finally {
    live.close();
  }
});

test("a divergent live object refuses instead of rebuilding the store", () => {
  const live = new DatabaseSync(":memory:");
  try {
    install(live, deployed);
    live.exec("ALTER TABLE users ADD COLUMN surprise TEXT");
    assert.throws(
      () => applySchemaUpgrade(storage(live), schema),
      /explicit data-preserving migration is required/,
    );
    assert.equal(
      live.prepare("SELECT version FROM cf_schema").get().version,
      deployed.version,
    );
  } finally {
    live.close();
  }
});

test("an upgrade can be reverted to the deployed schema without losing rows", () => {
  const live = new DatabaseSync(":memory:");
  try {
    install(live, deployed);
    live.exec(
      "INSERT INTO users(id,email,role) VALUES('u1','learner@example.test','STUDENT')",
    );
    applySchemaUpgrade(storage(live), schema);
    live.exec(
      "INSERT INTO billing_orders(id,user_id,plan_code,list_price_kzt,amount_kzt,provider) VALUES('o1','u1','self',9990,4990,'fake')",
    );

    assert.ok(applySchemaRevert(storage(live), deployed) > 0);
    assert.equal(
      live.prepare("SELECT version FROM cf_schema").get().version,
      deployed.version,
    );
    assert.equal(
      live
        .prepare("SELECT count(*) AS n FROM sqlite_master WHERE name='billing_plans'")
        .get().n,
      0,
    );
    assert.equal(
      live.prepare("SELECT email FROM users WHERE id='u1'").get().email,
      "learner@example.test",
    );

    const baseline = new DatabaseSync(":memory:");
    try {
      install(baseline, deployed);
      assert.deepEqual(schemaObjects(live), schemaObjects(baseline));
    } finally {
      baseline.close();
    }
  } finally {
    live.close();
  }
});

test("a revert refuses when the live database no longer matches the baseline", () => {
  const live = new DatabaseSync(":memory:");
  try {
    install(live, deployed);
    live.exec("ALTER TABLE users ADD COLUMN surprise TEXT");
    assert.throws(
      () => applySchemaRevert(storage(live), deployed),
      /cannot be reverted/,
    );
  } finally {
    live.close();
  }
});
