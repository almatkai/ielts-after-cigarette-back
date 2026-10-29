// Data-preserving native SQLite upgrade for an already imported DEV database.
// Only additive differences are applied; any changed or removed object aborts so
// that an explicit migration is written instead of silently rebuilding the store.

const SYSTEM = /^(?:cf_|sqlite_)/;

function identity(statement) {
  const match =
    /^CREATE\s+(?:UNIQUE\s+)?(TABLE|INDEX|TRIGGER)\s+(?:"([^"]+)"|([A-Za-z_]\w*))/i.exec(
      statement.trim(),
    );
  if (!match) throw Error("Unrecognized native schema statement");
  return { type: match[1].toUpperCase(), name: match[2] ?? match[3] };
}

function liveObjects(sql) {
  const objects = new Map();
  for (const row of sql
    .exec("SELECT type,name,sql FROM sqlite_master WHERE sql IS NOT NULL")
    .toArray()) {
    if (SYSTEM.test(row.name)) continue;
    objects.set(row.name, {
      type: String(row.type).toUpperCase(),
      sql: String(row.sql).trim(),
    });
  }
  return objects;
}

// Returns the objects a fresh install would have that the live database lacks.
// Throws when the live database holds anything the current schema does not
// describe exactly, rather than dropping or rewriting existing data.
export function planSchemaUpgrade(storage, schema) {
  const live = liveObjects(storage.sql);
  const target = new Map();
  for (const statement of schema.statements) {
    const id = identity(statement);
    target.set(id.name, { ...id, sql: statement.trim(), statement });
  }
  for (const [name, object] of live) {
    const expected = target.get(name);
    if (!expected || expected.type !== object.type || expected.sql !== object.sql)
      throw Error(
        `Native schema changed; an explicit data-preserving migration is required (${name})`,
      );
  }
  const missing = [...target.values()].filter((o) => !live.has(o.name));
  return {
    createdTables: new Set(
      missing.filter((o) => o.type === "TABLE").map((o) => o.name),
    ),
    statements: missing.map((o) => o.statement),
    seeds: schema.seeds.filter((seed) => !live.has(seed.table)),
  };
}

// Emergency counterpart: drops objects the baseline does not describe and puts
// the recorded version back, so the previous worker build can start again.
// Refuses when the baseline itself is missing or was altered on the live box.
export function planSchemaRevert(storage, baseline) {
  const live = liveObjects(storage.sql);
  const target = new Map();
  for (const statement of baseline.statements) {
    const id = identity(statement);
    target.set(id.name, { ...id, sql: statement.trim() });
  }
  for (const [name, object] of target) {
    const actual = live.get(name);
    if (!actual || actual.type !== object.type || actual.sql !== object.sql)
      throw Error(`Native schema cannot be reverted; ${name} differs`, {
        cause: object,
      });
  }
  const extra = [...live.entries()].filter(([name]) => !target.has(name));
  const tables = new Set(
    extra.filter(([, o]) => o.type === "TABLE").map(([name]) => name),
  );
  return {
    statements: extra
      .filter(([, o]) => o.type !== "TABLE")
      .map(([name, o]) => `DROP ${o.type} "${name}"`)
      .concat([...tables].map((name) => `DROP TABLE "${name}"`)),
    version: baseline.version,
  };
}

export function applySchemaRevert(storage, baseline) {
  const plan = planSchemaRevert(storage, baseline);
  storage.transactionSync(() => {
    for (const statement of plan.statements) storage.sql.exec(statement);
    storage.sql.exec(
      "UPDATE cf_schema SET version=? WHERE version<>?",
      plan.version,
      plan.version,
    );
  });
  return plan.statements.length;
}

export function applySchemaUpgrade(storage, schema) {
  const plan = planSchemaUpgrade(storage, schema);
  storage.transactionSync(() => {
    for (const statement of plan.statements) storage.sql.exec(statement);
    for (const seed of plan.seeds) {
      if (!plan.createdTables.has(seed.table)) continue;
      for (const row of seed.rows) {
        const columns = Object.keys(row);
        storage.sql.exec(
          `INSERT INTO ${seed.table}(${columns.join(",")}) VALUES(${columns.map(() => "?").join(",")})`,
          ...Object.values(row),
        );
      }
    }
    storage.sql.exec(
      "UPDATE cf_schema SET version=? WHERE version<>?",
      schema.version,
      schema.version,
    );
  });
  return plan.statements.length;
}
