# Runbook: slow queries

## Signal
`altserv_db_slow_queries` is greater than zero for five minutes, or p95 latency
is above one second without a traffic spike. The `AltservSlowQueries` and
`AltservHighLatency` alerts fire.

The application samples `pg_stat_activity` every 30 seconds and exports the
number of statements in the current database that have run longer than
`SLOW_QUERY_THRESHOLD` (default 1s). It stores only a count, never statement
text or patient data.

## 1. Confirm
```sql
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
```
`deployments/sql/slow-queries.sql` contains this query plus blocking sessions,
`pg_stat_statements` top statements, table statistics and index usage.

Treat the output as internal data. Do not paste query text that contains
literals, names or notes into a ticket or chat.

## 2. Classify
- **Lock wait**: `wait_event_type = 'Lock'`. Find the blocker with
  `pg_blocking_pids(pid)`. Do not kill the blocker before checking what it is.
- **I/O wait**: `wait_event_type = 'IO'`. Check disk latency and the plan.
- **CPU**: the statement is running, no wait event. Check the plan for a large
  scan or a bad join.
- **Idle in transaction**: a client opened a transaction and stopped. Check the
  application code and the pool. This holds locks and blocks vacuum.
- **Long report or billing export**: expected to be slower. Confirm the duration
  against the baseline before treating it as an incident.

## 3. Get the plan
Run `deployments/sql/explain-analyze.sql` against a copy of production data.
The file wraps every statement in a transaction and rolls back. It selects only
ids and timestamps.

Look for:
- `Seq Scan` on a large table where an index should be used
- a nested loop with a high actual row count
- a sort or hash that spills to disk
- estimated rows far from actual rows (stale statistics)
- a filter that removes most rows after reading them

## 4. Fix
Choose the smallest safe change:
1. `ANALYZE` the table if statistics are stale.
2. Add a missing index for the exact predicate and order. Check the plan again.
3. Change the query to use an existing index, or to filter before joining.
4. Add pagination or a tighter time window.
5. Only then add CPU, memory or a read replica.

Do not add an index without checking write cost and without a migration. An
index that is never used still slows inserts.

## 5. Cancel or terminate
Cancelling is a production action. Confirm the statement, the owner and the
runtime first.
```sql
-- Ask the statement to stop. It can still roll back cleanly.
SELECT pg_cancel_backend(<pid>);

-- Last resort. This ends the session and can leave a client in an error state.
SELECT pg_terminate_backend(<pid>);
```
Never terminate a migration, a billing approval or a report correction without
the incident commander's approval. Check `pg_stat_activity.xact_start` first: a
long transaction may be doing legitimate work.

## 6. Verify
- `altserv_db_slow_queries` returns to zero.
- p95 latency returns to the baseline.
- The pool in-use gauge falls.
- The changed query has a plan with the expected index and row count.

## 7. Record
Add a short note: query, plan before and after, change, migration number and
result. If the fix needs a migration, add it to the schema and run the
integration tests.

## Known gaps
- The slow-query gauge is a snapshot, not a histogram. `pg_stat_statements` is
  needed for per-statement totals and is not enabled by the application.
- The threshold is global. A report export and a login share the same limit.
