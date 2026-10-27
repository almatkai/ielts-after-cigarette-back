// Small SQL expression walker: quotes and nested calls are preserved; values stay bound.
export function splitArguments(text) {
  const out = [];
  let depth = 0,
    quote = false,
    start = 0;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (c === "'") {
      if (quote && text[i + 1] === "'") {
        i++;
        continue;
      }
      quote = !quote;
    }
    if (quote) continue;
    if (c === "(" || c === "[") depth++;
    else if (c === ")" || c === "]") depth--;
    else if (c === "," && depth === 0) {
      out.push(text.slice(start, i).trim());
      start = i + 1;
    }
  }
  out.push(text.slice(start).trim());
  return out;
}
export function closeParen(s, open) {
  let depth = 1,
    quote = false;
  for (let i = open + 1; i < s.length; i++) {
    const c = s[i];
    if (c === "'") {
      if (quote && s[i + 1] === "'") {
        i++;
        continue;
      }
      quote = !quote;
    }
    if (quote) continue;
    if (c === "(") depth++;
    else if (c === ")" && !--depth) return i;
  }
  throw Error("Unbalanced application SQL");
}
export function functions(sql, fn) {
  let out = "",
    quote = false;
  for (let i = 0; i < sql.length; ) {
    if (sql[i] === "'") {
      let end = i + 1;
      while (end < sql.length) {
        if (sql[end] === "'") {
          if (sql[end + 1] === "'") {
            end += 2;
            continue;
          }
          end++;
          break;
        }
        end++;
      }
      out += sql.slice(i, end);
      i = end;
      continue;
    }
    const m = /^([a-z_][a-z_0-9]*)\s*\(/i.exec(sql.slice(i));
    if (m) {
      const open = i + m[0].length - 1,
        end = closeParen(sql, open),
        args = splitArguments(sql.slice(open + 1, end)).map((s) =>
          functions(s, fn),
        );
      out += fn(m[1], args, sql.slice(end + 1)) ?? `${m[1]}(${args.join(",")})`;
      i = end + 1;
    } else {
      out += sql[i];
      i++;
    }
  }
  return out;
}
export function mergeJSON(sql) {
  let s = sql;
  for (let i = 0; i < s.length; i++) {
    const first = /^json_(?:object|set|patch)\(/.exec(s.slice(i));
    if (!first) continue;
    const end = closeParen(s, i + first[0].length - 1),
      next = /^\s*\|\|\s*json_object\(/.exec(s.slice(end + 1));
    if (!next) continue;
    const open = end + next[0].length,
      last = closeParen(s, open),
      args = splitArguments(s.slice(open + 1, last));
    let object = s.slice(i, end + 1);
    // json_set preserves RHS nulls (unlike RFC7396 json_patch), matching jsonb ||.
    for (let at = 0; at < args.length; at += 30) {
      const values = args
        .slice(at, at + 30)
        .map((v, j) => (j % 2 === 0 ? "'$.' || " + v : v));
      object = `json_set(${object},${values.join(",")})`;
    }
    s = s.slice(0, i) + object + s.slice(last + 1);
    i = -1;
  }
  return s;
}
