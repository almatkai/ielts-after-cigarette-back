import schema from "../schema/sqlite.json" with { type: "json" };
const unique = new Map();
for (const c of schema.constraints) {
  const match = /^(?:PRIMARY KEY|UNIQUE) \(([^)]+)\)/.exec(c.sqlite);
  if (match)
    unique.set(
      match[1]
        .split(",")
        .map((x) => c.table + "." + x.trim().replaceAll('"', ""))
        .join(", "),
      c.name,
    );
}
for (const s of schema.statements) {
  const match = /CREATE UNIQUE INDEX (\w+) ON (\w+) \((\w+)\)/.exec(s);
  if (match) unique.set(match[2] + "." + match[3], match[1]);
}
export function storageError(error) {
  let code = error.code ?? "",
    constraint = error.constraint ?? "";
  const text = String(error.message ?? "");
  if (text.includes("UNIQUE constraint failed:")) {
    code = "23505";
    const columns = text
      .split("UNIQUE constraint failed:")[1]
      .split(": SQLITE_CONSTRAINT")[0]
      .trim();
    constraint =
      /index ['"]([^'"]+)/.exec(columns)?.[1] ??
      unique.get(columns) ??
      "unique_constraint";
  } else if (text.includes("FOREIGN KEY constraint failed")) code = "23503";
  else if (text.includes("CHECK constraint failed:")) {
    code = "23514";
    constraint = /CHECK constraint failed: (\w+)/.exec(text)?.[1] ?? "";
  } else if (text.includes("NOT NULL constraint failed")) code = "23502";
  return JSON.stringify({ error: "Storage adapter failure", code, constraint });
}
