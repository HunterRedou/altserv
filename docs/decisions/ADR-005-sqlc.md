# ADR-005: sqlc

## Problem
Choose how Go code talks to PostgreSQL.

## Alternatives
- Raw `database/sql`
- `sqlx`
- GORM
- `sqlc`

## Decision
`sqlc`: SQL files in, typed Go methods out.

## Reasons
- SQL stays explicit and reviewable.
- Generated structs and parameters are type-checked at compile time.
- No ORM query magic or hidden N+1 behavior.
- Easy to keep queries close to the schema.

## Consequences
- Run `sqlc generate` after every query or schema change.
- Generated files in `internal/db` are never edited by hand.
- A schema change and its queries must land together.

## Risks
- Schema parser support for some PostgreSQL features is limited.
- The generated code is tied to the sqlc version (`v1.31.1` today).

## Follow-up
- Migrate the driver from `lib/pq` to `pgx/v5` (doc 16).
