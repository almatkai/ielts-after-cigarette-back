import { normalizeDate } from "./special-sql.mjs";
const result = (rows) => ({ rows, columns: [], changes: 0 });
const percentile = (values, p) => {
  const a = values.filter((x) => x !== null).sort((a, b) => a - b);
  if (!a.length) return null;
  const n = (a.length - 1) * p,
    i = Math.floor(n);
  return a[i] + (a[Math.ceil(n)] - a[i]) * (n - i);
};
export function analyticsSQL(storage, source, args) {
  const sql = storage.sql,
    s = source.replace(/\s+/g, " ").trim();
  if (s.startsWith("SELECT provider_id,(array_agg")) {
    const rows = sql
        .exec(
          "SELECT provider_id,provider_name,model,purpose,outcome,won,started_at,duration_ms,first_token_ms,first_response_ms FROM ai_provider_calls WHERE started_at>=? ORDER BY started_at DESC",
          normalizeDate(new Date(Date.now() - args[0] * 864e5)),
        )
        .toArray(),
      groups = new Map();
    for (const r of rows) {
      const key = JSON.stringify([r.provider_id, r.model, r.purpose]);
      if (!groups.has(key)) groups.set(key, []);
      groups.get(key).push(r);
    }
    return result(
      [...groups.values()]
        .sort(
          (a, b) =>
            a[0].purpose.localeCompare(b[0].purpose) ||
            a[0].model.localeCompare(b[0].model) ||
            (a[0].provider_id ?? "").localeCompare(b[0].provider_id ?? ""),
        )
        .slice(0, 200)
        .map((g) => {
          const r = g[0],
            success = g.filter((r) => r.outcome === "success"),
            count = (outcomes) =>
              g.filter((r) => outcomes.includes(r.outcome)).length;
          return [
            r.provider_id,
            r.provider_name,
            r.model,
            r.purpose,
            g.length,
            count(["success"]),
            count(["failure", "timeout"]),
            count(["timeout"]),
            count(["cancelled"]),
            g.filter((r) => r.won).length,
            percentile(
              success.map((r) => r.duration_ms),
              0.5,
            ),
            percentile(
              success.map((r) => r.duration_ms),
              0.95,
            ),
            percentile(
              success.map((r) => r.first_token_ms),
              0.5,
            ),
            percentile(
              success.map((r) => r.first_response_ms),
              0.5,
            ),
          ];
        }),
    );
  }
  if (s.startsWith("SELECT material_type,") && s.includes("PERCENTILE_CONT")) {
    const rows = sql
        .exec(
          "SELECT material_type,user_id,status,band,started_at,submitted_at FROM attempts WHERE started_at>=? ORDER BY material_type",
          args[0],
        )
        .toArray(),
      groups = new Map();
    for (const r of rows) {
      if (!groups.has(r.material_type)) groups.set(r.material_type, []);
      groups.get(r.material_type).push(r);
    }
    return result(
      [...groups.entries()].map(([skill, g]) => {
        const done = g.filter((r) => r.status === "SUBMITTED"),
          bands = done.map((r) => r.band).filter((x) => x !== null);
        return [
          skill,
          g.length,
          done.length,
          g.filter((r) => r.status === "ABANDONED").length,
          new Set(g.map((r) => r.user_id)).size,
          bands.length
            ? Math.round(
                (bands.reduce((a, b) => a + b, 0) / bands.length) * 100,
              ) / 100
            : null,
          percentile(
            done.map(
              (r) => (new Date(r.submitted_at) - new Date(r.started_at)) / 1000,
            ),
            0.5,
          ),
        ];
      }),
    );
  }
  return undefined;
}
