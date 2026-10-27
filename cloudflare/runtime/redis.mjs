import { emptyHLL, addHLL, countHLL } from "./hll.mjs";
// Application-compatible operations over transactional native SQLite, without Redis.
export class Coordination {
  constructor(storage) {
    this.storage = storage;
    this.sql = storage.sql;
    this.sql.exec(
      `CREATE TABLE IF NOT EXISTS cf_coordination(key TEXT PRIMARY KEY,value TEXT NOT NULL,expires INTEGER)`,
    );
  }
  get(key) {
    const r = this.sql
      .exec("SELECT value,expires FROM cf_coordination WHERE key=?", key)
      .toArray()[0];
    if (!r) return null;
    if (r.expires && r.expires <= Date.now()) {
      this.sql.exec("DELETE FROM cf_coordination WHERE key=?", key);
      return null;
    }
    return JSON.parse(r.value);
  }
  set(key, value, ttl = 0) {
    this.sql.exec(
      "INSERT INTO cf_coordination VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,expires=excluded.expires",
      key,
      JSON.stringify(value),
      ttl ? Date.now() + ttl : null,
    );
  }
  update(key, value) {
    const expires = this.sql
      .exec("SELECT expires FROM cf_coordination WHERE key=?", key)
      .toArray()[0]?.expires;
    this.sql.exec(
      "INSERT INTO cf_coordination VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
      key,
      JSON.stringify(value),
      expires ?? null,
    );
  }
  run(op, a) {
    return this.storage.transactionSync(() => this.operation(op, a));
  }
  operation(op, a) {
    const [key] = a;
    switch (op) {
      case "Ping":
        return "PONG";
      case "Get":
        return this.get(key);
      case "MGet":
        return key.map((k) => this.get(k));
      case "Set":
        this.set(key, a[1], a[2]);
        return "OK";
      case "SetNX":
        if (this.get(key) !== null) return false;
        this.set(key, a[1], a[2]);
        return true;
      case "Del": {
        let n = 0;
        for (const k of key) {
          n += this.get(k) !== null ? 1 : 0;
          this.sql.exec("DELETE FROM cf_coordination WHERE key=?", k);
        }
        return n;
      }
      case "Expire":
        if (this.get(key) === null) return false;
        this.sql.exec(
          "UPDATE cf_coordination SET expires=? WHERE key=?",
          Date.now() + a[1],
          key,
        );
        return true;
      case "Incr": {
        const n = Number(this.get(key) ?? 0) + 1;
        this.update(key, String(n));
        return n;
      }
      case "script": {
        const [script, keys, args] = a;
        const k = keys[0],
          n = Number(this.get(k) ?? 0);
        if (script.includes("current >= limit")) {
          if (n >= args[0]) return [0, n];
          if (n === 0) this.set(k, String(n + 1), args[1]);
          else this.update(k, String(n + 1));
          return [1, n + 1];
        }
        if (!script.includes("PEXPIRE") || !script.includes("INCR"))
          throw Error("Unmapped script");
        if (n === 0) this.set(k, "1", args[0]);
        else this.update(k, String(n + 1));
        return n + 1;
      }
      case "ZAdd": {
        const m = this.get(key) ?? {};
        let n = 0;
        for (const z of a[1]) {
          if (!(z.Member in m)) n++;
          m[z.Member] = z.Score;
        }
        this.update(key, m);
        return n;
      }
      case "ZIncrBy": {
        const m = this.get(key) ?? {};
        const n = (m[a[2]] ?? 0) + a[1];
        m[a[2]] = n;
        this.update(key, m);
        return n;
      }
      case "ZRem": {
        const m = this.get(key) ?? {};
        let n = 0;
        for (const k of a[1])
          if (k in m) {
            delete m[k];
            n++;
          }
        this.update(key, m);
        return n;
      }
      case "ZRemRangeByScore": {
        const m = this.get(key) ?? {},
          min = number(a[1]),
          max = number(a[2]);
        let n = 0;
        for (const [k, v] of Object.entries(m))
          if (v >= min && v <= max) {
            delete m[k];
            n++;
          }
        this.update(key, m);
        return n;
      }
      case "ZRangeByScore":
      case "ZCount": {
        const r = op === "ZCount" ? { Min: a[1], Max: a[2] } : a[1];
        const rows = Object.entries(this.get(key) ?? {})
          .filter(([, v]) => v >= number(r.Min) && v <= number(r.Max))
          .sort((a, b) => a[1] - b[1] || a[0].localeCompare(b[0]));
        return op === "ZCount" ? rows.length : rows.map((x) => x[0]);
      }
      case "ZRevRangeWithScores":
        return Object.entries(this.get(key) ?? {})
          .sort((a, b) => b[1] - a[1] || b[0].localeCompare(a[0]))
          .slice(a[1], a[2] + 1)
          .map(([Member, Score]) => ({ Member, Score }));
      case "HSet": {
        const m = this.get(key) ?? {};
        let n = 0;
        for (let i = 0; i < a[1].length; i += 2) {
          const k = a[1][i];
          if (!(k in m)) n++;
          m[k] = a[1][i + 1];
        }
        this.update(key, m);
        return n;
      }
      case "HDel": {
        const m = this.get(key) ?? {};
        let n = 0;
        for (const k of a[1])
          if (k in m) {
            n++;
            delete m[k];
          }
        this.update(key, m);
        return n;
      }
      case "HMGet": {
        const m = this.get(key) ?? {};
        return a[1].map((k) => m[k] ?? null);
      }
      case "PFAdd": {
        const r = addHLL(this.get(key) ?? emptyHLL(), a[1]);
        this.update(key, r.state);
        return r.changed ? 1 : 0;
      }
      case "PFCount":
        return countHLL(key.map((k) => this.get(k)).filter(Boolean));
      case "LPush": {
        const m = this.get(key) ?? [];
        for (const v of a[1]) m.unshift(String(v));
        this.update(key, m);
        return m.length;
      }
      case "BRPop": {
        for (const k of key) {
          const m = this.get(k) ?? [];
          if (m.length) {
            const v = m.pop();
            this.update(k, m);
            return [k, v];
          }
        }
        return null;
      }
      case "Scan": {
        const pattern = key
          .replace(/[.+?^${}()|[\]\\]/g, "\\$&")
          .replaceAll("*", ".*");
        const re = new RegExp("^" + pattern + "$");
        return this.sql
          .exec("SELECT key FROM cf_coordination ORDER BY key")
          .toArray()
          .map((x) => x.key)
          .filter((k) => re.test(k) && this.get(k) !== null);
      }
      default:
        throw Error(`Unmapped coordination command ${op}`);
    }
  }
}
function number(s) {
  return s === "-inf" ? -Infinity : s === "+inf" ? Infinity : Number(s);
}
