import { analyticsSQL } from "./analytics-sql.mjs";
import { NOW, bindValue } from "./sql.mjs";
const data = (rows = [], columns = [], changes = 0) => ({
  rows,
  columns,
  changes,
});
export function specialSQL(storage, source, input, execute) {
  const s = source.replace(/\s+/g, " ").trim(),
    a = input.map(bindValue),
    sql = storage.sql;
  const analytics = analyticsSQL(storage, source, a);
  if (analytics !== undefined) return analytics;
  if (
    s.includes(
      "concat_ws(' ',u.email,u.phone,p.display_name,u.first_name,u.last_name) ILIKE",
    )
  ) {
    const needle = String(a[0])
        .replace(/\\([\\%_])/g, "$1")
        .toLowerCase(),
      role = a[1],
      offset = Number(a[2] ?? 0),
      ids = [];
    let count = 0;
    for (const row of sql.exec(
      "SELECT u.id,u.email,u.phone,p.display_name,u.first_name,u.last_name,u.role FROM users u LEFT JOIN user_profiles p ON p.user_id=u.id ORDER BY u.created_at DESC,u.id",
    )) {
      const text = [
        row.email,
        row.phone,
        row.display_name,
        row.first_name,
        row.last_name,
      ]
        .filter((x) => x !== null)
        .join(" ")
        .toLowerCase();
      if ((!needle || text.includes(needle)) && (!role || row.role === role)) {
        if (count >= offset && count < offset + 30) ids.push(row.id);
        count++;
      }
    }
    if (/^SELECT count\(\*\)/i.test(s)) return data([[count]], ["count"]);
    const query = source.replace(
      /WHERE \(\$1='' OR concat_ws[\s\S]*?AND \(\$2='' OR u.role=\$2\)/,
      "WHERE u.id IN(SELECT value FROM json_each($1))",
    );
    return execute(query, [{ $array: ids }, role, 0]);
  }
  if (
    s.startsWith("WITH locked AS MATERIALIZED") &&
    s.includes("jsonb_to_recordset")
  )
    return storage.transactionSync(() => {
      const attempt = sql
        .exec("SELECT status FROM attempts WHERE id=?", a[0])
        .toArray()[0];
      if (!attempt) return data([], ["status"]);
      if (attempt.status === "IN_PROGRESS")
        for (const answer of JSON.parse(
          typeof a[1] === "string" ? a[1] : new TextDecoder().decode(a[1]),
        ))
          sql.exec(
            "INSERT INTO attempt_answers(attempt_id,question_id,answer) VALUES(?,?,?) ON CONFLICT(attempt_id,question_id) DO UPDATE SET answer=excluded.answer",
            a[0],
            answer.question_id,
            JSON.stringify(answer.answer),
          );
      return data([[attempt.status]], ["status"]);
    });
  if (s.startsWith("WITH session AS") && s.includes("replacement AS"))
    return storage.transactionSync(() => {
      const old = sql
        .exec("SELECT * FROM refresh_sessions WHERE token_hash=?", a[0])
        .toArray()[0];
      if (!old || old.revoked_at !== null || old.expires_at <= a[6])
        return data([], ["user_id"]);
      sql.exec(
        "INSERT INTO refresh_sessions(id,user_id,token_hash,expires_at,user_agent,ip_address) VALUES(?,?,?,?,NULLIF(?,''),NULLIF(?,''))",
        a[1],
        old.user_id,
        a[2],
        a[3],
        a[4],
        a[5],
      );
      sql.exec(
        "UPDATE refresh_sessions SET revoked_at=?,replaced_by=? WHERE id=?",
        a[6],
        a[1],
        old.id,
      );
      return data([[old.user_id]], ["user_id"]);
    });
  if (s.startsWith("WITH opened AS"))
    return storage.transactionSync(() => {
      const row = sql
        .exec(
          "SELECT * FROM full_mock_session_sections WHERE session_id=? AND position=?",
          a[0],
          a[1],
        )
        .toArray()[0];
      if (
        !row ||
        !sql
          .exec(
            "SELECT id FROM attempts WHERE id=? AND status='IN_PROGRESS'",
            row.attempt_id,
          )
          .toArray().length
      )
        return data();
      const now = new Date(),
        started = row.started_at ?? now.toISOString(),
        deadline =
          row.deadline_at ??
          new Date(
            row.remaining_milliseconds !== null
              ? now.getTime() + row.remaining_milliseconds
              : new Date(started).getTime() + a[2] * 60000,
          ).toISOString();
      sql.exec(
        "UPDATE full_mock_session_sections SET started_at=?,deadline_at=?,remaining_milliseconds=NULL WHERE session_id=? AND position=?",
        normalizeDate(started),
        normalizeDate(deadline),
        a[0],
        a[1],
      );
      sql.exec(
        "UPDATE attempts SET started_at=? WHERE id=?",
        normalizeDate(started),
        row.attempt_id,
      );
      return data([], [], 1);
    });
  if (s.startsWith("WITH guests AS")) {
    const aggregate = `LEFT JOIN (SELECT gg.user_id,MIN(s.started_at) AS started_at,MIN(s.submitted_at) FILTER(WHERE s.status='SUBMITTED' AND s.submitted_at>=s.started_at AND s.submitted_at<=$4) AS completed_at FROM guest_trials gg JOIN full_mock_sessions s ON s.user_id=gg.user_id OR s.id=gg.claimed_session_id WHERE s.started_at>=gg.created_at AND s.started_at<=$4 GROUP BY gg.user_id) m ON m.user_id=g.user_id`;
    let query = source.replace(
      /LEFT JOIN LATERAL \([\s\S]*?\) m ON true/i,
      aggregate,
    );
    query = query
      .replace(
        "WITH guests AS",
        "WITH RECURSIVE cf_days(day) AS(SELECT $5 UNION ALL SELECT date(day,'+1 day') FROM cf_days WHERE day<$2), guests AS",
      )
      .replace(
        /generate_series\(\$5::date, \$2::date, interval '1 day'\) d\(day\)/i,
        "cf_days d",
      );
    return execute(query, input);
  }
  if (s.includes("JOIN LATERAL (") && s.includes(") metadata ON TRUE")) {
    let q = source.replace(
      /\s*LEFT JOIN LATERAL \([\s\S]*?\) metadata ON TRUE/i,
      "",
    );
    for (const field of ["title", "slug"]) {
      const select = `CASE a.material_type ${["listening", "reading", "writing", "speaking"].map((skill) => `WHEN '${skill}' THEN (SELECT ${field} FROM ${skill === "listening" ? "listening_test" + (field === "title" ? "_versions" : "s") : skill === "reading" ? "reading_material" + (field === "title" ? "_versions" : "s") : skill + "_material" + (field === "title" ? "_versions" : "s")} WHERE id=a.${field === "title" ? "material_version_id" : "material_id"})`).join(" ")} END`;
      q = q.replaceAll("metadata." + field, `(${select})`);
    }
    return execute(q, input);
  }
  if (s.includes("JOIN LATERAL (") && s.includes(") q ON true")) {
    const performance = s.includes("q.source_id");
    const common = `SELECT 'reading' skill,r.id question_id,g.question_type,v.id version_id,v.material_id source_id,v.title,v.body stimulus,r.prompt,r.answer,r.explanation,r.content,NULL audio FROM reading_questions r JOIN reading_question_groups g ON g.id=r.group_id JOIN reading_material_versions v ON v.id=g.material_version_id UNION ALL SELECT 'listening',r.id,g.question_type,v.id,v.test_id,v.title,${performance ? "COALESCE((SELECT CASE WHEN count(*)=count(lp.audio_asset_id) THEN group_concat(lp.audio_asset_id,',' ORDER BY lp.position) END FROM listening_parts lp WHERE lp.test_version_id=v.id),'')" : "g.context"},r.prompt,r.answer,r.explanation,r.content,CASE WHEN p.audio_asset_id IS NOT NULL THEN '/listening/media/'||p.audio_asset_id END FROM listening_questions r JOIN listening_question_groups g ON g.id=r.group_id JOIN listening_parts p ON p.id=g.part_id JOIN listening_test_versions v ON v.id=p.test_version_id`;
    const on = `q ON q.question_id=aa.question_id AND q.skill=a.material_type AND ((q.skill='reading' AND (q.version_id=a.material_version_id OR EXISTS(SELECT 1 FROM reading_test_passages tp WHERE tp.test_material_version_id=a.material_version_id AND tp.passage_material_version_id=q.version_id AND tp.passage_material_id=q.source_id))) OR (q.skill='listening' AND q.version_id=a.material_version_id AND q.source_id=a.material_id))`;
    return execute(
      source.replace(
        /JOIN LATERAL \([\s\S]*?\) q ON true/i,
        `JOIN (${common}) ${on}`,
      ),
      input,
    );
  }
  if (s.includes("SELECT DISTINCT ON (skill)"))
    return execute(
      source
        .replace(
          "SELECT DISTINCT ON (skill) skill,material_id,version_id FROM eligible",
          "SELECT skill,material_id,version_id FROM (SELECT *,row_number() OVER(PARTITION BY skill ORDER BY material_id) AS cf_rank FROM eligible) WHERE cf_rank=1 AND",
        )
        .replace(/WHERE cf_rank=1 AND\s+WHERE/, "WHERE cf_rank=1 AND"),
      input,
    );
  return undefined;
}
export function normalizeDate(s) {
  const d = new Date(s);
  return d.toISOString().replace(/\.(\d{3})Z$/, ".$1000000Z");
}
