import types from "../schema/types.json" with { type: "json" };
import { functions, mergeJSON } from "./sql-functions.mjs";
export const NOW = "strftime('%Y-%m-%dT%H:%M:%f000000Z','now')";
// All input is application-owned SQL. Bind values never enter the SQL text.
export function expression(sql) {
  let s = sql.replace(
    /::(?:character varying|double precision|timestamp with time zone|timestamp without time zone|[a-z_][a-z_0-9]*)(?:\[\])?(?:\(\d+(?:,\d+)?\))?/gi,
    "",
  );
  s = s
    .replace(/=\s*ANY\s*\(\s*\(ARRAY\[([^\]]*)\]\)\s*\)/gi, "IN ($1)")
    .replace(/=\s*ANY\s*\(\s*ARRAY\[([^\]]*)\]\s*\)/gi, "IN ($1)");
  s = s
    .replace(/\bBTRIM\s*\(/gi, "trim(")
    .replace(/\boctet_length\s*\(/gi, "length(")
    .replace(/\bchar_length\s*\(/gi, "length(");
  s = s
    .replace(/\bjsonb_typeof\s*\(/gi, "json_type(")
    .replace(/\bjsonb_array_length\s*\(/gi, "json_array_length(")
    .replace(/\bcardinality\s*\(/gi, "json_array_length(");
  s = s.replace(/\bCURRENT_TIMESTAMP\b|\bnow\(\)|\bclock_timestamp\(\)/gi, NOW);
  s = s.replace(/E'\\n'/g, "char(10)");
  s = s.replace(/'\{\}'(?=\s*(?:[,)]|$))/g, "'{}'");
  return s;
}
export function translate(sql, args = []) {
  let s = sql.replace(
    /\bjsonb_set\(\s*([A-Za-z_][\w.]*|\$\d+)\s*,\s*'\{([^}']+)\}'\s*,\s*(\$(\d+))(?:[^,()]*)\s*\)/gi,
    (_, document, path, value, position) =>
      `json_set(${document},'$${path
        .split(",")
        .map((key) => "." + key)
        .join("")}',json(${value}))`,
  );
  s = s.replace(/(\$\d+)::jsonb/gi, "CAST($1 AS TEXT)");
  // Parameterized ANY accepts a single JSON array and stays below 100 bind slots.
  s = s.replace(
    /=\s*ANY\s*\(\s*(\$\d+)(?:::[a-z]+\[\])?\s*\)/gi,
    "IN (SELECT value FROM json_each($1))",
  );
  s = s.replace(
    /\bFOR\s+(?:NO KEY\s+)?UPDATE(?:\s+OF\s+\w+(?:,\s*\w+)*)?(?:\s+SKIP LOCKED)?/gi,
    "",
  );
  s = s.replace(/\bILIKE\b/gi, "LIKE");
  s = s.replace(
    /(?:CURRENT_TIMESTAMP|now\(\))\s*-\s*INTERVAL\s*'(\d+) (seconds?|minutes?|days?)'/gi,
    (_, n, u) => `strftime('%Y-%m-%dT%H:%M:%f000000Z','now','-${n} ${u}')`,
  );
  s = s.replace(
    /CURRENT_TIMESTAMP\s*\+\s*INTERVAL\s*'(\d+) (seconds?|minutes?)'/gi,
    (_, n, u) => `strftime('%Y-%m-%dT%H:%M:%f000000Z','now','+${n} ${u}')`,
  );
  s = s.replace(
    /CURRENT_TIMESTAMP\s*\+\s*CASE WHEN attempts <= 1 THEN INTERVAL '10 seconds' ELSE INTERVAL '60 seconds' END/gi,
    `strftime('%Y-%m-%dT%H:%M:%f000000Z','now',CASE WHEN attempts <= 1 THEN '+10 seconds' ELSE '+60 seconds' END)`,
  );
  s = s.replace(
    /EXTRACT\(EPOCH FROM \(sec.deadline_at-CURRENT_TIMESTAMP\)\)/gi,
    "((julianday(sec.deadline_at)-julianday('now'))*86400.0)",
  );
  s = s.replace(/UPDATE (\w+) (sec|a)\s+SET/gi, "UPDATE $1 AS $2 SET");
  s = s.replace(
    /now\(\)\s*-\s*\(\$(\d+)::int \* interval '1 day'\)/gi,
    (_, n) => `strftime('%Y-%m-%dT%H:%M:%f000000Z','now','-'||$${n}||' days')`,
  );
  s = s.replace(
    /\$(\d+)::date\s*-\s*(\d+)/gi,
    (_, n, d) => `date($${n},'-${d} days')`,
  );
  s = s.replace(
    /c.reg_day \+ \$(\d+)::int \* 7/gi,
    (_, n) => `date(c.reg_day,'+'||($${n}*7)||' days')`,
  );
  s = s.replace(
    /\(a.day - c.reg_day - 1\) \/ 7 \+ 1/gi,
    "CAST((julianday(a.day)-julianday(c.reg_day)-1)/7 AS INTEGER)+1",
  );
  s = s
    .replace(
      /SELECT generate_series\(\$1::date, \$2::date, interval '1 day'\)::date AS day/gi,
      "SELECT $1 AS day UNION ALL SELECT date(day,'+1 day') FROM days WHERE day<$2",
    )
    .replace(/^\s*WITH days AS/i, "WITH RECURSIVE days AS");
  s = s.replace(
    /EXTRACT\(ISODOW FROM started_at AT TIME ZONE \$2\)::int/gi,
    "((CAST(strftime('%w',started_at,'+5 hours') AS INTEGER)+6)%7+1)",
  );
  s = s.replace(
    /EXTRACT\(HOUR FROM started_at AT TIME ZONE \$2\)::int/gi,
    "CAST(strftime('%H',started_at,'+5 hours') AS INTEGER)",
  );
  s = s.replace(
    /EXTRACT\(EPOCH FROM submitted_at - started_at\)::bigint/gi,
    "ROUND((julianday(submitted_at)-julianday(started_at))*86400)",
  );
  s = s.replace(
    /\((COALESCE\([^()]+\)|\w+(?:\.\w+)?) AT TIME ZONE \$[23]\)::date/gi,
    "date($1,'+5 hours')",
  );
  s = s.replace(
    /date_trunc\('week',\s*(\w+(?:\.\w+)?)\)::date/gi,
    "date($1,'weekday 0','-6 days')",
  );
  s = s.replace(/to_char\((\w+\.\w+), 'YYYY-MM-DD'\)/gi, "$1");
  s = s.replace(/\)\/60\)/g, ")/60.0)");
  s = s.replace(
    /EXTRACT\(EPOCH FROM \(CURRENT_TIMESTAMP - min\(created_at\)\)\)/gi,
    "((julianday('now')-julianday(min(created_at)))*86400.0)",
  );
  s = expression(s);

  s = s
    .replace(/\bjsonb_build_object\(/gi, "json_object(")
    .replace(/\bjsonb_object_agg\(/gi, "json_group_object(")
    .replace(/\bjsonb_agg\(/gi, "json_group_array(");
  s = s.replace(
    /\bjsonb_array_elements\(([^()]+)\)\s+(\w+)/gi,
    "json_each($1) $2",
  );
  const aliases = [...s.matchAll(/json_each\([^()]+\)\s+(\w+)/gi)].map(
    (x) => x[1],
  );
  for (const alias of aliases)
    s = s.replace(new RegExp(`\\b${alias}(?=\\s*->)`, "g"), `${alias}.value`);
  s = s.replace(/CROSS\s+JOIN\s+LATERAL\s+json_each/gi, "CROSS JOIN json_each");
  s = s.replace(/to_jsonb\((\w+)\)/gi, (match, alias, offset) => {
    const candidates = [
      ...s
        .slice(offset)
        .matchAll(new RegExp(`(?:FROM|JOIN)\\s+(\\w+)\\s+${alias}\\b`, "gi")),
    ];
    const table = candidates[0]?.[1];
    if (!types[table]) throw Error("Unknown row-to-JSON source");
    return `json_object(${Object.entries(types[table])
      .flatMap(([column, type]) => [
        `'${column}'`,
        type === "boolean"
          ? `json(CASE WHEN ${alias}.${column} IS NULL THEN 'null' WHEN ${alias}.${column} THEN 'true' ELSE 'false' END)`
          : type === "jsonb" || type.endsWith("[]")
            ? `json(${alias}.${column})`
            : `${alias}.${column}`,
      ])
      .join(",")})`;
  });
  s = functions(s, (name, a) => {
    if (/^(greatest|least)$/i.test(name)) {
      // PostgreSQL ignores NULL arguments; SQLite's scalar max/min do not.
      // Aggregate over a local rowset so NULLs are skipped without evaluating
      // arguments twice or converting their numeric/text values through JSON.
      const aggregate = name.toLowerCase() === "greatest" ? "max" : "min";
      return `(SELECT ${aggregate}(cf_extremum_value) FROM (${a
        .map(
          (value, i) =>
            `SELECT ${value}${i === 0 ? " AS cf_extremum_value" : ""}`,
        )
        .join(" UNION ALL ")}))`;
    }
    if (name.toLowerCase() === "split_part" && a[1] === "'@'" && a[2] === "1")
      return `(CASE WHEN instr(${a[0]},'@')>0 THEN substr(${a[0]},1,instr(${a[0]},'@')-1) ELSE ${a[0]} END)`;
    if (name.toLowerCase() === "concat_ws")
      return `(SELECT group_concat(value,${a[0]}) FROM json_each(json_array(${a.slice(1).join(",")})) WHERE value IS NOT NULL)`;
    if (name.toLowerCase() === "json_group_array" && /^row(?:\s|$)/i.test(a[0]))
      return `json_group_array(${a[0].replace(/^row/, "json(row)")})`;
    if (name.toLowerCase() === "json_object") {
      const values = a.map((v, i) =>
        i % 2 === 0
          ? v
          : /^(?:'(?:googleConnected|hasPassword|blocked|enabled|won|correct|isCorrect|from_env|fromEnv)')$/.test(
                a[i - 1],
              ) && !/^json\(/i.test(v)
            ? `json(CASE WHEN ${v} IS NULL THEN 'null' WHEN ${v} THEN 'true' ELSE 'false' END)`
            : /^(?:'(?:skills|activityDays|sessions)')$/.test(a[i - 1])
              ? `json(${v})`
              : /^(?:COALESCE\()?\w+\.(?:admin_permissions|answer|content|feedback|body_json|parts|tasks)\b/i.test(
                    v,
                  )
                ? `json(${v})`
                : v,
      );
      if (values.length <= 32) return `json_object(${values.join(",")})`;
      let object = "'{}'";
      for (let i = 0; i < values.length; i += 30) {
        const group = values
          .slice(i, i + 30)
          .map((v, j) => (j % 2 === 0 ? "'$." + "' || " + v : v));
        object = `json_set(${object},${group.join(",")})`;
      }
      return object;
    }
    if (name.toLowerCase() === "json_group_object")
      return `json_group_object(${a[0]},json(${a[1]}))`;
  });
  s = mergeJSON(s);
  const bind = [];
  s = s.replace(/\$(\d+)/g, (_, n) => {
    bind.push(bindValue(args[Number(n) - 1]));
    return "?";
  });
  // Parameters are positional, including repeated occurrences.
  return { sql: s, args: bind };
}
export function bindValue(v) {
  if (v === null || v === undefined) return null;
  if (typeof v === "boolean") return v ? 1 : 0;
  if (typeof v === "object") {
    if ("$bytes" in v)
      return Uint8Array.from(atob(v.$bytes), (c) => c.charCodeAt(0)).buffer;
    if ("$array" in v) return JSON.stringify(v.$array);
    if ("$json" in v) return v.$json;
    return JSON.stringify(v);
  }
  return v;
}
