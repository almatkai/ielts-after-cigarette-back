import { test } from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { readFileSync } from "node:fs";
import { Miniflare, convertV4MiniflareOptions } from "miniflare";
import { SqlReadProfile } from "../runtime/sql-read-profile.mjs";

const root = fileURLToPath(new URL("..", import.meta.url));

test("disabled profiling preserves storage identity; bounded snapshots never expose SQL or values", () => {
  const storage = {
    sql: {
      exec() {
        return { rowsRead: 0, rowsWritten: 0 };
      },
    },
  };
  const disabled = new SqlReadProfile();
  assert.equal(disabled.wrapStorage(storage), storage);
  const profile = new SqlReadProfile(true, 3);
  const db = profile.wrapStorage(storage);
  for (let i = 0; i < 20; i++)
    db.sql.exec(
      `SELECT private_column_${String.fromCharCode(97 + i)} FROM private_table WHERE token='TOP_SECRET'`,
      "BOUND_SECRET",
    );
  const snapshot = profile.snapshot();
  assert.equal(snapshot.queries.length, 3);
  assert.equal(snapshot.overflow.calls, 17);
  const encoded = JSON.stringify(snapshot) + profile.prometheus();
  assert.doesNotMatch(
    encoded,
    /TOP_SECRET|BOUND_SECRET|private_column|private_table|SELECT/,
  );
  assert.equal(
    profile.fingerprint("SELECT 123, 'alice' /* secret */"),
    profile.fingerprint("select 456, 'bob' /* other */"),
  );
  snapshot.queries[0].calls = 1000;
  assert.equal(profile.snapshot().queries[0].calls, 1);
});

test("many startup statements cannot hide later expensive queries", () => {
  const profile = new SqlReadProfile(true, 3);
  const db = profile.wrapStorage({
    sql: {
      exec(statement, reads = 0) {
        return { rowsRead: reads, rowsWritten: 0, toArray: () => [] };
      },
    },
  });
  for (let i = 0; i < 20; i++)
    db.sql.exec(`CREATE TABLE table_${String.fromCharCode(97 + i)}(id TEXT)`);
  db.sql.exec("SELECT id FROM expensive_table", 91600).toArray();
  for (let i = 0; i < 20; i++)
    db.sql
      .exec(`SELECT id FROM cheap_${String.fromCharCode(97 + i)}`, 1)
      .toArray();
  const snapshot = profile.snapshot();
  assert.equal(snapshot.queries.length, 3);
  const hot = snapshot.queries.find(
    (q) =>
      q.fingerprint === profile.fingerprint("SELECT id FROM expensive_table"),
  );
  assert.equal(hot.rowsRead, 91600);
  assert.equal(hot.calls, 1);
  assert.equal(
    snapshot.queries.reduce((sum, q) => sum + q.calls, snapshot.overflow.calls),
    41,
  );
  assert.equal(
    snapshot.queries.reduce(
      (sum, q) => sum + q.rowsRead,
      snapshot.overflow.rowsRead,
    ),
    91620,
  );
});

test("native workerd profiling measures consumed rows across raw cursors, nested special SQL, rollback and errors", async () => {
  const script = `
import { DurableObject } from 'cloudflare:workers';
import { SqlReadProfile } from '../runtime/sql-read-profile.mjs';
import { specialSQL } from '../runtime/special-sql.mjs';
export class ProfileTest extends DurableObject {
 async fetch() {
  const profile = new SqlReadProfile(true);
  const db = profile.wrapStorage(this.ctx.storage);
  db.sql.exec('CREATE TABLE items(id INTEGER PRIMARY KEY, value TEXT)');
  db.transactionSync(()=>{for(let i=0;i<68;i++) db.sql.exec('INSERT INTO items VALUES(?,?)', i, 'PRIVATE_BOUND_VALUE')});
  const before = profile.snapshot();
  let raw;
  profile.observe(()=>{profile.observe(()=>{raw=Array.from(db.sql.exec('SELECT id FROM items').raw())})});
  const after = profile.snapshot();
  const readHash=profile.fingerprint('SELECT id FROM items');
  const rawEntry=after.queries.find(q=>q.fingerprint===readHash);
  const count=db.sql.exec('SELECT count(*) AS n FROM items').one().n;
  let forOf=0;profile.observe(()=>{for(const row of db.sql.exec('SELECT value FROM items'))forOf+=row.value.length>0});
  const forOfEntry=profile.snapshot().queries.find(q=>q.fingerprint===profile.fingerprint('SELECT value FROM items'));
  const batch=db.sql.exec('SELECT id FROM items ORDER BY id');const iterator=batch.raw();
  for(let i=0;i<10;i++) iterator.next();profile.sample(batch);
  for(let i=0;i<58;i++) iterator.next();profile.sample(batch);
  iterator.next();profile.sample(batch);
  const batchEntry=profile.snapshot().queries.find(q=>q.fingerprint===profile.fingerprint('SELECT id FROM items ORDER BY id'));
  let rollback=false;try{db.transactionSync(()=>{db.sql.exec('INSERT INTO items VALUES(100,?)','ROLLBACK_SECRET');throw Error('rollback')})}catch{rollback=true}
  const afterRollback=db.sql.exec('SELECT count(*) AS n FROM items').one().n;
  let thrown=false;try{profile.observe(()=>{Array.from(db.sql.exec('SELECT id FROM items').raw());throw Error('after-consumption')})}catch{thrown=true}
  let invalid=false;try{db.sql.exec('SELECT * FROM missing_table')}catch{invalid=true}
  db.sql.exec('CREATE TABLE attempts(id TEXT PRIMARY KEY,status TEXT)');
  db.sql.exec('CREATE TABLE attempt_answers(attempt_id TEXT,question_id TEXT,answer TEXT,PRIMARY KEY(attempt_id,question_id))');
  db.sql.exec("INSERT INTO attempts VALUES('a','IN_PROGRESS')");
  const special = profile.observe(()=>specialSQL(db,'WITH locked AS MATERIALIZED jsonb_to_recordset',[ 'a', JSON.stringify([{question_id:'q',answer:{value:'PRIVATE_ANSWER'}}])],()=>{throw Error('unexpected fallback')}));
  const final=profile.snapshot();
  const errorEntry=final.queries.find(q=>q.fingerprint===profile.fingerprint('SELECT * FROM missing_table'));
  return Response.json({before,raw,rawEntry,forOf,forOfEntry,batchEntry,count,rollback,afterRollback,thrown,invalid,errorEntry,special,final,prometheus:profile.prometheus()});
 }
}
export default {fetch(r,e){return e.P.get(e.P.idFromName('profile')).fetch(r)}};
`;
  const modules = [
    {
      type: "ESModule",
      path: root + "/tests/sql-profile-fixture.mjs",
      contents: script,
    },
    ...[
      "sql-read-profile",
      "special-sql",
      "analytics-sql",
      "sql",
      "sql-functions",
    ].map((name) => ({
      type: "ESModule",
      path: root + `/runtime/${name}.mjs`,
      // workerd's unbundled modules do not accept JSON import attributes;
      // inline the exact schema value as Wrangler does in the deployed bundle.
      ...(name === "sql"
        ? {
            contents: readFileSync(root + "/runtime/sql.mjs", "utf8").replace(
              /^import types[^\n]+;\n/,
              "const types = " +
                readFileSync(root + "/schema/types.json", "utf8") +
                ";\n",
            ),
          }
        : {}),
    })),
  ];
  const mf = new Miniflare(
    convertV4MiniflareOptions({
      modules,
      modulesRoot: root,
      compatibilityDate: "2026-10-19",
      durableObjects: { P: { className: "ProfileTest", useSQLite: true } },
    }),
  );
  try {
    const response = await mf.dispatchFetch("https://profile.test");
    assert.equal(response.status, 200);
    const result = await response.json();
    assert.equal(result.raw.length, 68);
    assert.equal(result.rawEntry.calls, 1, "nested scopes do not double-count");
    assert.equal(result.rawEntry.rowsRead, 68);
    assert.equal(result.forOf, 68);
    assert.equal(result.forOfEntry.rowsRead, 68);
    assert.equal(result.batchEntry.calls, 1);
    assert.equal(
      result.batchEntry.rowsRead,
      68,
      "batch samples count only deltas",
    );
    assert.equal(result.count, 68);
    assert.equal(result.afterRollback, 68);
    assert.ok(result.rollback && result.thrown && result.invalid);
    assert.equal(result.errorEntry.errors, 1);
    assert.deepEqual(result.special.rows, [["IN_PROGRESS"]]);
    const encoded = JSON.stringify(result.final) + result.prometheus;
    assert.doesNotMatch(
      encoded,
      /PRIVATE_BOUND_VALUE|ROLLBACK_SECRET|PRIVATE_ANSWER|attempt_answers|SELECT|INSERT/,
    );
    assert.equal(result.final.scopeSamplesDropped, 0);
    assert.ok(
      result.final.queries.find(
        (q) =>
          q.fingerprint ===
          new SqlReadProfile().fingerprint("SELECT id FROM items"),
      ).rowsRead === 136,
      "throwing scope still samples its cursor",
    );
  } finally {
    await mf.dispose();
  }
});
