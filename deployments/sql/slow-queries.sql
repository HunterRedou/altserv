-- Slow-query investigation helpers.
--
-- The application exports altserv_db_slow_queries from pg_stat_activity. This
-- file adds statement-level detail when the pg_stat_statements extension is
-- enabled. Enable it with the provider or a superuser; it is not required for
-- the basic slow-query gauge.
--
-- These statements return query text and must be treated as internal data.
-- Do not copy raw SQL that contains literals into tickets or chat.

-- 1. Statements running longer than the application threshold right now.
SELECT
  pid,
  usename,
  state,
  clock_timestamp() - query_start AS runtime,
  wait_event_type,
  wait_event,
  left(query, 200) AS query_preview
FROM pg_stat_activity
WHERE datname = current_database()
  AND state = 'active'
  AND query_start IS NOT NULL
  AND clock_timestamp() - query_start > interval '1 second'
ORDER BY runtime DESC;

-- 2. Blocking sessions.
SELECT
  blocked.pid AS blocked_pid,
  blocking.pid AS blocking_pid,
  clock_timestamp() - blocked.query_start AS blocked_runtime,
  left(blocked.query, 120) AS blocked_query,
  left(blocking.query, 120) AS blocking_query
FROM pg_stat_activity AS blocked
JOIN pg_stat_activity AS blocking
  ON blocking.pid = ANY(pg_blocking_pids(blocked.pid))
WHERE blocked.datname = current_database();

-- 3. Top statements by total execution time (requires pg_stat_statements).
SELECT
  calls,
  round(total_exec_time::numeric, 1) AS total_ms,
  round(mean_exec_time::numeric, 1) AS mean_ms,
  rows,
  left(query, 200) AS query_preview
FROM pg_stat_statements
WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
ORDER BY total_exec_time DESC
LIMIT 20;

-- 4. Tables with stale or missing statistics.
SELECT
  schemaname,
  relname,
  n_live_tup,
  n_dead_tup,
  last_analyze,
  last_autoanalyze
FROM pg_stat_user_tables
WHERE schemaname = 'public'
ORDER BY n_dead_tup DESC;

-- 5. Index usage for the application tables.
SELECT
  relname,
  indexrelname,
  idx_scan,
  idx_tup_read,
  idx_tup_fetch
FROM pg_stat_user_indexes
WHERE schemaname = 'public'
ORDER BY idx_scan ASC;
