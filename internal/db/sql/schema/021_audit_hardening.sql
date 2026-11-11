-- +goose Up
-- Point 15 hardens the audit log against privileged manipulation.
--
-- Row-level DELETE triggers do not fire for TRUNCATE, so the append-only guard
-- from migration 013 needs a statement-level TRUNCATE trigger as well. The
-- application role keeps read and append rights only; it can never rewrite or
-- remove an audit row.
CREATE TRIGGER audit_events_no_truncate
  BEFORE TRUNCATE ON audit_events
  FOR EACH STATEMENT EXECUTE FUNCTION audit_events_append_only();

REVOKE UPDATE, DELETE, TRUNCATE ON audit_events FROM altserv_app;
GRANT SELECT, INSERT ON audit_events TO altserv_app;

-- The read API filters by action and time. The existing tenant/time index
-- already serves the unfiltered list; this one serves an action filter.
CREATE INDEX audit_events_tenant_action_idx
  ON audit_events(tenant_id, action, occurred_at DESC);

-- +goose Down
DROP INDEX IF EXISTS audit_events_tenant_action_idx;
DROP TRIGGER IF EXISTS audit_events_no_truncate ON audit_events;
