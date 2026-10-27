import { createHash } from "node:crypto";
import { readFile, writeFile } from "node:fs/promises";
import { DatabaseSync } from "node:sqlite";
import { expression, NOW } from "../runtime/sql.mjs";
const pg = JSON.parse(
  await readFile(new URL("../schema/postgres.json", import.meta.url), "utf8"),
);
const quote = (s) => '"' + s.replaceAll('"', '""') + '"';
const statements = [],
  mapping = [];
for (const t of pg.tables) {
  const definitions = t.columns.map((c) => {
    const type = /bool|smallint|integer|bigint/.test(c.type)
      ? "INTEGER"
      : /numeric|double|real/.test(c.type)
        ? "REAL"
        : c.type === "bytea"
          ? "BLOB"
          : "TEXT";
    // bigserial PK must be INTEGER PRIMARY KEY for SQLite-generated review IDs.
    const serial = c.default?.startsWith("nextval(");
    let d =
      quote(c.name) +
      " " +
      type +
      (serial ? " PRIMARY KEY AUTOINCREMENT" : "") +
      (c.required ? " NOT NULL" : "");
    if (c.default && !serial) {
      let value = expression(c.default);
      if (c.type.endsWith("[]")) {
        const raw = expression(c.default);
        let array;
        if (raw === "'{}'") array = [];
        else if (/^ARRAY\[(?:'[^']*'(?:,\s*)?)*\]$/.test(raw))
          array = [...raw.matchAll(/'([^']*)'/g)].map((v) => v[1]);
        else throw Error("Unknown PostgreSQL array default: " + raw);
        value = "'" + JSON.stringify(array).replaceAll("'", "''") + "'";
      }
      d += " DEFAULT (" + value + ")";
    }
    const name = quote(c.name),
      checks = [];
    const varchar = c.type.match(/^character varying\((\d+)\)$/);
    if (varchar) checks.push(`length(${name})<=${Number(varchar[1])}`);
    if (c.type === "boolean") checks.push(`${name} IN(0,1)`);
    if (["smallint", "integer", "bigint"].includes(c.type)) {
      checks.push(`typeof(${name})='integer'`);
      if (c.type !== "bigint")
        checks.push(
          `${name} BETWEEN ${c.type === "smallint" ? "-32768 AND 32767" : "-2147483648 AND 2147483647"}`,
        );
    }
    if (c.type === "jsonb" || c.type.endsWith("[]"))
      checks.push(`json_valid(${name})`);
    if (c.type.endsWith("[]")) checks.push(`json_type(${name})='array'`);
    for (const check of checks) d += ` CHECK(${name} IS NULL OR (${check}))`;
    return d;
  });
  for (const c of t.constraints) {
    if (
      c.type === "p" &&
      t.columns.some((x) => x.default?.startsWith("nextval("))
    )
      continue;
    let def = expression(c.definition);
    if (def.includes("<@")) {
      const match = def.match(/(\w+)\s*<@\s*ARRAY\[([^\]]+)\]/);
      if (!match) throw Error("Unknown subset " + def);
      const [, column, allowed] = match;
      def = def.replace(match[0], "1");
      for (const operation of ["INSERT", "UPDATE"])
        statements.push({
          after: true,
          sql: `CREATE TRIGGER ${quote(c.name + "_" + operation)} BEFORE ${operation} ON ${quote(t.name)} WHEN EXISTS(SELECT 1 FROM json_each(NEW.${quote(column)}) WHERE value NOT IN (${allowed})) BEGIN SELECT RAISE(ABORT, '${c.name}'); END`,
        });
    }
    if (def.includes(" ~ ")) {
      if (def.includes("phone"))
        def = def.replace(
          /\(?phone\)?\s*~\s*'[^']*'/,
          "(length(phone) BETWEEN 9 AND 16 AND substr(phone,1,1)='+' AND substr(phone,2,1) GLOB '[1-9]' AND substr(phone,2) NOT GLOB '*[^0-9]*')",
        );
      else if (def.includes("slug"))
        def = def.replace(
          /\(?slug\)?\s*~\s*'[^']*'/,
          "(length(slug)>0 AND slug NOT GLOB '*[^a-z0-9-]*' AND substr(slug,1,1)!='-' AND substr(slug,-1)!='-' AND instr(slug,'--')=0)",
        );
      else throw Error("Unknown regex " + def);
    }
    mapping.push({
      table: t.name,
      name: c.name,
      postgres: c.definition,
      sqlite: def,
    });
    definitions.push("CONSTRAINT " + quote(c.name) + " " + def);
  }
  statements.push({
    sql: `CREATE TABLE ${quote(t.name)} (${definitions.join(",\n")})`,
  });
  for (const index of t.indexes) {
    if (
      t.constraints.some(
        (c) => ["p", "u"].includes(c.type) && c.name === index.name,
      )
    )
      continue;
    const sql = expression(index.definition)
      .replace(/public\./g, "")
      .replace(/ USING btree /gi, " ");
    statements.push({ after: true, sql });
  }
  for (const trigger of t.triggers) {
    if (!trigger.definition.includes("set_updated_at()"))
      throw Error("Unknown trigger " + trigger.definition);
    statements.push({
      after: true,
      sql: `CREATE TRIGGER ${quote(trigger.name)} AFTER UPDATE ON ${quote(t.name)} BEGIN UPDATE ${quote(t.name)} SET updated_at=${NOW} WHERE rowid=NEW.rowid; END`,
    });
  }
}
statements.sort((a, b) => Number(!!a.after) - Number(!!b.after));
const db = new DatabaseSync(":memory:");
db.exec("PRAGMA foreign_keys=ON");
for (const s of statements)
  try {
    db.exec(s.sql);
  } catch (e) {
    console.error(s.sql);
    throw e;
  }
await writeFile(
  new URL("../schema/sqlite.json", import.meta.url),
  JSON.stringify(
    {
      revision: pg.revision,
      statements: statements.map((x) => x.sql),
      seeds: pg.tables
        .filter((t) => t.seed.length)
        .map((t) => ({
          table: t.name,
          rows: t.seed.map((row) =>
            Object.fromEntries(
              Object.entries(row).map(([k, v]) => [
                k,
                typeof v === "boolean"
                  ? Number(v)
                  : v !== null && typeof v === "object"
                    ? JSON.stringify(v)
                    : v,
              ]),
            ),
          ),
        })),
      constraints: mapping,
    },
    null,
    2,
  ) + "\n",
);
console.log(
  `Validated SQLite schema: ${pg.tables.length} tables, ${mapping.length} constraints, ${statements.length} statements`,
);

await writeFile(
  new URL("../schema/types.json", import.meta.url),
  JSON.stringify(
    Object.fromEntries(
      pg.tables.map((t) => [
        t.name,
        Object.fromEntries(t.columns.map((c) => [c.name, c.type])),
      ]),
    ),
    null,
    2,
  ) + "\n",
);

const nativePath = new URL("../schema/sqlite.json", import.meta.url),
  native = JSON.parse(await readFile(nativePath, "utf8"));
native.version = createHash("sha256")
  .update(JSON.stringify([native.statements, native.seeds]))
  .digest("hex");
await writeFile(nativePath, JSON.stringify(native, null, 2) + "\n");
