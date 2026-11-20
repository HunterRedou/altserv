-- Database login roles for altserv (WP-04).
--
-- Run this file as the RDS master user (or another role with CREATEROLE) on
-- the altserv database, before goose. It is idempotent: run it again to
-- rotate the passwords or to repair the role attributes.
--
--   psql "$MASTER_DB_URL" \
--     -v migrator_password="$MIGRATOR_PASSWORD" \
--     -v runtime_password="$RUNTIME_PASSWORD" \
--     -f deployments/sql/roles.sql
--
-- A password variable can be a plain password or a SCRAM-SHA-256 verifier.
-- A verifier keeps the plain password out of the server log.
--
-- Roles:
--   altserv_app       NOLOGIN. Holds the table grants. Migration 012 creates
--                     it too; this file creates it first so that the
--                     migrator does not need CREATEROLE.
--   altserv_migrator  LOGIN. Runs goose and owns the schema objects.
--   altserv_runtime   LOGIN. The application connects as this role. It is a
--                     member of altserv_app and has no other privilege.

\set ON_ERROR_STOP on

\if :{?migrator_password}
\else
  \warn 'roles.sql: set -v migrator_password=...'
  SELECT 1 / 0 AS migrator_password_missing;
\endif
\if :{?runtime_password}
\else
  \warn 'roles.sql: set -v runtime_password=...'
  SELECT 1 / 0 AS runtime_password_missing;
\endif

BEGIN;

-- Create each role only when it does not exist yet.
SELECT 'CREATE ROLE altserv_app NOLOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION'
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'altserv_app')
\gexec

SELECT 'CREATE ROLE altserv_migrator LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION'
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'altserv_migrator')
\gexec

SELECT 'CREATE ROLE altserv_runtime LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION'
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'altserv_runtime')
\gexec

-- Repair the attributes of existing roles. SUPERUSER, BYPASSRLS and
-- REPLICATION can only be changed by a superuser, so they are checked below
-- instead of set here.
ALTER ROLE altserv_app NOLOGIN NOCREATEDB NOCREATEROLE INHERIT;
ALTER ROLE altserv_migrator LOGIN NOCREATEDB NOCREATEROLE INHERIT;
ALTER ROLE altserv_runtime LOGIN NOCREATEDB NOCREATEROLE INHERIT;

-- \gexec does not echo the statement, so the password is not printed.
SELECT format('ALTER ROLE altserv_migrator PASSWORD %L', :'migrator_password')
\gexec
SELECT format('ALTER ROLE altserv_runtime PASSWORD %L', :'runtime_password')
\gexec

-- The runtime role gets the table grants of altserv_app and can run
-- SET LOCAL ROLE altserv_app (internal/db/tenant.go).
GRANT altserv_app TO altserv_runtime;

-- Migration 012 runs GRANT altserv_app TO CURRENT_USER. That needs the ADMIN
-- option on altserv_app.
GRANT altserv_app TO altserv_migrator WITH ADMIN OPTION;

-- Only the migrator creates objects. PostgreSQL 14 and older let PUBLIC
-- create objects in the public schema; this removes that.
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE, CREATE ON SCHEMA public TO altserv_migrator;

-- Stop when a role has an attribute that this file cannot remove.
DO $$
DECLARE
  bad text;
BEGIN
  SELECT string_agg(rolname, ', ' ORDER BY rolname) INTO bad
  FROM pg_roles
  WHERE rolname IN ('altserv_app', 'altserv_migrator', 'altserv_runtime')
    AND (rolsuper OR rolbypassrls OR rolreplication OR rolcreatedb OR rolcreaterole);
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'roles.sql: % must be NOSUPERUSER NOBYPASSRLS NOREPLICATION NOCREATEDB NOCREATEROLE; a superuser must fix this', bad;
  END IF;

  -- The runtime role must not be a member of any role except altserv_app.
  SELECT string_agg(r.rolname, ', ' ORDER BY r.rolname) INTO bad
  FROM pg_auth_members m
  JOIN pg_roles r ON r.oid = m.roleid
  WHERE m.member = 'altserv_runtime'::regrole
    AND r.rolname <> 'altserv_app';
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'roles.sql: altserv_runtime must be a member of altserv_app only, not of %', bad;
  END IF;
END
$$;

COMMIT;
