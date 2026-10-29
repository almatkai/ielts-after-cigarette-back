// Optional, isolate-local counters. No SQL/parameters/rows are retained or logged.
// Synchronous scopes sample native cursors after consumption, with no per-row hook.
export class SqlReadProfile {
  constructor(enabled = false, limit = 128) {
    this.enabled = enabled;
    this.limit = Math.max(1, Math.min(128, limit));
    this.entries = new Map();
    this.overflow = { calls: 0, errors: 0, rowsRead: 0, rowsWritten: 0 };
    this.samplers = new WeakMap();
    this.scope = null;
    this.scopeSamplesDropped = 0;
  }
  fingerprint(statement) {
    const normalized = String(statement)
      .replace(/'(?:''|[^'])*'|--[^\n]*|\/\*[\s\S]*?\*\//g, "?")
      .replace(/\b\d+(?:\.\d+)?\b/g, "?")
      .replace(/\s+/g, " ")
      .trim()
      .toLowerCase();
    let hash = 2166136261;
    for (let i = 0; i < normalized.length; i++)
      hash = Math.imul(hash ^ normalized.charCodeAt(i), 16777619);
    return (hash >>> 0).toString(16).padStart(8, "0");
  }
  exec(sql, statement, args) {
    const fingerprint = this.fingerprint(statement);
    let entry = this.entries.get(fingerprint);
    if (!entry) {
      // Startup DDL must not occupy every slot before application queries run.
      // Evict the cheapest retained fingerprint, preserving its totals in overflow.
      if (this.entries.size >= this.limit) {
        let cheapest;
        for (const candidate of this.entries.values())
          if (
            !cheapest ||
            candidate.rowsRead < cheapest.rowsRead ||
            (candidate.rowsRead === cheapest.rowsRead &&
              candidate.calls < cheapest.calls)
          )
            cheapest = candidate;
        this.entries.delete(cheapest.fingerprint);
        cheapest.retained = false;
        for (const field of ["calls", "errors", "rowsRead", "rowsWritten"])
          this.overflow[field] += cheapest[field];
      }
      entry = {
        fingerprint,
        calls: 0,
        errors: 0,
        rowsRead: 0,
        rowsWritten: 0,
        retained: true,
      };
      this.entries.set(fingerprint, entry);
    }
    entry.calls++;
    let cursor;
    try {
      cursor = sql.exec(statement, ...args);
    } catch (error) {
      entry.errors++;
      throw error;
    }
    let read = 0,
      written = 0;
    const sample = () => {
      // Metrics must never change application behavior, even on an invalid cursor.
      try {
        const nextRead = Number(cursor.rowsRead ?? 0);
        const nextWritten = Number(cursor.rowsWritten ?? 0);
        const deltaRead = Math.max(0, nextRead - read);
        const deltaWritten = Math.max(0, nextWritten - written);
        entry.rowsRead += deltaRead;
        entry.rowsWritten += deltaWritten;
        if (!entry.retained) {
          this.overflow.rowsRead += deltaRead;
          this.overflow.rowsWritten += deltaWritten;
        }
        read = nextRead;
        written = nextWritten;
      } catch {}
    };
    sample();
    if (this.scope) {
      if (this.scope.size < 512) this.scope.add(sample);
      else this.scopeSamplesDropped++;
    }
    const observed = new Proxy(cursor, {
      get(target, key) {
        const value = Reflect.get(target, key, target);
        if (key === "toArray" || key === "one")
          return (...args) => {
            try {
              return value.apply(target, args);
            } finally {
              sample();
            }
          };
        return typeof value === "function" ? value.bind(target) : value;
      },
    });
    this.samplers.set(observed, sample);
    return observed;
  }
  sample(cursor) {
    this.samplers.get(cursor)?.();
  }
  observe(fn) {
    if (!this.enabled || this.scope) return fn();
    const scope = (this.scope = new Set());
    try {
      return fn();
    } finally {
      this.scope = null;
      for (const sample of scope) sample();
    }
  }
  wrapStorage(storage) {
    if (!this.enabled) return storage;
    const profile = this;
    const sql = new Proxy(storage.sql, {
      get(target, key) {
        if (key === "exec")
          return (statement, ...args) => profile.exec(target, statement, args);
        const value = Reflect.get(target, key, target);
        return typeof value === "function" ? value.bind(target) : value;
      },
    });
    return new Proxy(storage, {
      get(target, key) {
        if (key === "sql") return sql;
        const value = Reflect.get(target, key, target);
        return typeof value === "function" ? value.bind(target) : value;
      },
    });
  }
  snapshot() {
    return {
      enabled: this.enabled,
      scopeSamplesDropped: this.scopeSamplesDropped,
      queries: [...this.entries.values()]
        .map(({ fingerprint, calls, errors, rowsRead, rowsWritten }) => ({
          fingerprint,
          calls,
          errors,
          rowsRead,
          rowsWritten,
        }))
        .sort((a, b) => b.rowsRead - a.rowsRead || b.calls - a.calls),
      overflow: { ...this.overflow },
    };
  }
  prometheus() {
    if (!this.enabled) return "";
    const entries = [
      ...this.entries.values(),
      { fingerprint: "overflow", ...this.overflow },
    ];
    return [
      "# Isolate-local SQL profile; resets on eviction; hash labels contain no SQL or parameters.",
      ...["calls", "errors", "rowsRead", "rowsWritten"].flatMap((field) =>
        entries.map(
          (e) =>
            `iac_sql_profile_${field.replace(/[A-Z]/g, (c) => "_" + c.toLowerCase())}_total{query="${e.fingerprint}"} ${e[field]}`,
        ),
      ),
      `iac_sql_profile_scope_samples_dropped_total ${this.scopeSamplesDropped}`,
      "",
    ].join("\n");
  }
}
