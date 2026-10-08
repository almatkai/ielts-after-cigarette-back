-- Run as the DB administrator AFTER enabling shared_preload_libraries and
-- restarting PostgreSQL. Query text may contain sensitive application data;
-- do not expose this view over a public API.
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;

SELECT queryid, calls,
       round(total_exec_time::numeric, 2) AS total_ms,
       round(mean_exec_time::numeric, 2) AS mean_ms,
       rows, shared_blks_read, shared_blks_hit,
       left(query, 500) AS query
FROM pg_stat_statements
WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
ORDER BY total_exec_time DESC
LIMIT 20;
