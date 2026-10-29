import { DurableObject } from "cloudflare:workers";
import { Coordination } from "../runtime/redis.mjs";
import { IACBlobChunks, IACBlobIndex } from "../runtime/blob-worker.mjs";

export class ReadBudgetProbe extends DurableObject {
  async fetch(request) {
    const sql = this.ctx.storage.sql;
    const count = Number(new URL(request.url).searchParams.get("count"));
    sql.exec(
      "CREATE TABLE cf_coordination(key TEXT PRIMARY KEY,value TEXT NOT NULL,expires INTEGER)",
    );
    sql.exec("CREATE TABLE leases(upload TEXT PRIMARY KEY,expires INTEGER)");
    sql.exec(
      "CREATE TABLE objects(key TEXT PRIMARY KEY,meta TEXT NOT NULL); CREATE TABLE uploads(id TEXT PRIMARY KEY,key TEXT NOT NULL,parent TEXT,fields TEXT NOT NULL,part TEXT,expires INTEGER); CREATE TABLE multipart(id TEXT PRIMARY KEY,key TEXT NOT NULL,fields TEXT NOT NULL,parts TEXT NOT NULL,expires INTEGER NOT NULL)",
    );
    this.ctx.storage.transactionSync(() => {
      for (let i = 0; i < count; i++) {
        sql.exec(
          "INSERT INTO cf_coordination VALUES(?,?,?)",
          String(i),
          "{}",
          i % 2 ? 100000 + i : null,
        );
        sql.exec("INSERT INTO leases VALUES(?,?)", String(i), 100000 + i);
        sql.exec(
          "INSERT INTO objects VALUES(?,?)",
          (i < 10 ? "_cf-temp/" : "saved/") + i,
          JSON.stringify({ uploaded: 100000 + i }),
        );
      }
    });
    const read = (query, ...args) => {
      const cursor = sql.exec(query, ...args);
      const rows = cursor.toArray();
      return { rows, rowsRead: cursor.rowsRead };
    };
    const sample = () => ({
      coordination: read(
        "SELECT MIN(expires) next FROM cf_coordination WHERE expires IS NOT NULL",
      ),
      leases: read("SELECT min(expires) AS next FROM leases"),
      expired: read(
        "SELECT upload FROM leases WHERE expires<=? LIMIT 20",
        100010,
      ),
      temporaryObjects: read(
        "SELECT min(expires) AS next FROM (SELECT expires FROM uploads WHERE expires IS NOT NULL UNION ALL SELECT expires FROM multipart UNION ALL SELECT json_extract(meta,'$.uploaded')+? AS expires FROM objects WHERE substr(key,1,9)='_cf-temp/')",
        86400000,
      ),
    });
    const before = sample();
    // Exercise real constructors against existing data, as on a DEV upgrade.
    new Coordination(this.ctx.storage);
    new IACBlobChunks(this.ctx, this.env);
    new IACBlobIndex(this.ctx, this.env);
    const after = sample();
    return Response.json({ count, before, after });
  }
}
export default {
  fetch(request, env) {
    return env.PROBE.get(
      env.PROBE.idFromName(new URL(request.url).search),
    ).fetch(request);
  },
};
