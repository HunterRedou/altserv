# ADR-002: PostgreSQL

## Problem
Choose the durable data store.

## Alternatives
- MySQL / MariaDB
- SQLite
- MongoDB
- PostgreSQL

## Decision
PostgreSQL.

## Reasons
- Strong constraints: foreign keys, CHECK, unique, composite keys.
- Row Level Security for tenant isolation (doc 3).
- Transactions and `SET LOCAL` for per-request tenant context.
- Mature backup and point-in-time recovery tooling.
- `sqlc` and `pgx` support it well.

## Consequences
- Schema changes go through versioned migrations.
- Separate database roles for the application and migrations.
- Backups and restore tests are required (doc 20).

## Risks
- Operational work: roles, backups, connection pooling.
- RLS mistakes can either leak data or block legitimate queries.
