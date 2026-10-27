// Offline derivation from all migrations of the immutable selected DEV snapshot.
import { PGlite } from "@electric-sql/pglite";
import { readFile, readdir, writeFile, mkdir } from "node:fs/promises";
import { createHash } from "node:crypto";
const sha = "40ed0dc5fca0b9df989a29d421a81476e7df0f50";
const base = new URL(
  `../.artifacts/${sha}/source/migrations/`,
  import.meta.url,
);
const pg = new PGlite();
const migrations = [];
for (const file of (await readdir(base))
  .filter((n) => n.endsWith(".up.sql"))
  .sort()) {
  const sql = await readFile(new URL(file, base), "utf8");
  try {
    await pg.exec(sql);
  } catch (e) {
    throw new Error(`${file}: ${e.message}`);
  }
  migrations.push({
    file,
    sha256: createHash("sha256").update(sql).digest("hex"),
  });
}
const tables = (
  await pg.query(
    `SELECT tablename AS name FROM pg_tables WHERE schemaname='public' ORDER BY tablename`,
  )
).rows;
for (const table of tables) {
  table.columns = (
    await pg.query(
      `SELECT a.attname AS name,format_type(a.atttypid,a.atttypmod) AS type,a.attnotnull AS required,pg_get_expr(d.adbin,d.adrelid) AS "default" FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE c.relname=$1 AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum`,
      [table.name],
    )
  ).rows;
  table.constraints = (
    await pg.query(
      `SELECT conname AS name,contype AS type,pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conrelid=$1::regclass ORDER BY conname`,
      [table.name],
    )
  ).rows;
  table.indexes = (
    await pg.query(
      `SELECT indexname AS name,indexdef AS definition FROM pg_indexes WHERE schemaname='public' AND tablename=$1 ORDER BY indexname`,
      [table.name],
    )
  ).rows;
  table.seed = (
    await pg.query(`SELECT * FROM \"${table.name}\" ORDER BY 1`)
  ).rows.map((row) =>
    Object.fromEntries(
      Object.entries(row).filter(
        ([key]) =>
          !table.columns.some(
            (c) => c.name === key && c.default === "CURRENT_TIMESTAMP",
          ),
      ),
    ),
  );
  table.triggers = (
    await pg.query(
      `SELECT tgname AS name,pg_get_triggerdef(oid) AS definition FROM pg_trigger WHERE tgrelid=$1::regclass AND NOT tgisinternal ORDER BY tgname`,
      [table.name],
    )
  ).rows;
}
await mkdir(new URL("../schema/", import.meta.url), { recursive: true });
await writeFile(
  new URL("../schema/postgres.json", import.meta.url),
  JSON.stringify({ revision: sha, migrations, tables }, null, 2) + "\n",
);
await pg.close();
console.log(
  `Derived ${tables.length} tables from ${migrations.length} migrations; no live data read`,
);
