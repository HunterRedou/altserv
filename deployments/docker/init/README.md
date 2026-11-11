# Database init scripts

This directory is mounted into `docker-entrypoint-initdb.d` for a fresh
PostgreSQL container. It is intentionally empty.

Do not put schema DDL here. The schema is versioned with goose migrations in
`internal/db/sql/schema` and applied by the `migrate` service or by the
deployment pipeline. Keeping one migration source avoids a container that only
works on first start.
