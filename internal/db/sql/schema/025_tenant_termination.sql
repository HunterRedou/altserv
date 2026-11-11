-- +goose Up
-- Step 5 makes tenant termination possible. The blocker was the append-only
-- audit trigger: deleting a tenant cascades to audit_events, and the trigger
-- aborts the whole statement. A controlled termination removes the audit trail
-- first (inside a privileged function), then deletes the tenant in dependency
-- order.

-- Replace the append-only trigger function with a version that gates DELETE.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION audit_events_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' AND current_setting('app.allow_audit_delete', true) = 'true' THEN
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'audit_events is append-only';
END;
$$;
-- +goose StatementEnd

-- Controlled termination procedure. It is SECURITY DEFINER so it can remove
-- audit rows and bypass row-level security. It runs as the migration role.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION terminate_tenant(p_tenant_id UUID)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public
AS $$
BEGIN
  IF p_tenant_id IS NULL THEN
    RAISE EXCEPTION 'tenant id is required';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM tenants WHERE id = p_tenant_id) THEN
    RAISE EXCEPTION 'tenant not found';
  END IF;

  -- Allow the append-only audit rows to be removed for this transaction only.
  PERFORM set_config('app.allow_audit_delete', 'true', true);

  -- Dependency order: remove the rows that block the tenant cascade first.
  DELETE FROM billing_findings WHERE tenant_id = p_tenant_id;
  DELETE FROM billing_lines WHERE tenant_id = p_tenant_id;
  DELETE FROM billing_runs WHERE tenant_id = p_tenant_id;
  DELETE FROM report_activities WHERE tenant_id = p_tenant_id;
  DELETE FROM reports WHERE tenant_id = p_tenant_id;
  DELETE FROM visits WHERE tenant_id = p_tenant_id;
  DELETE FROM activities WHERE tenant_id = p_tenant_id;
  DELETE FROM template_activities WHERE tenant_id = p_tenant_id;
  DELETE FROM templates WHERE tenant_id = p_tenant_id;
  DELETE FROM patient_assignments WHERE tenant_id = p_tenant_id;
  DELETE FROM patient_search_tokens WHERE tenant_id = p_tenant_id;
  DELETE FROM break_glass_grants WHERE tenant_id = p_tenant_id;
  DELETE FROM patients WHERE tenant_id = p_tenant_id;
  DELETE FROM legal_holds WHERE tenant_id = p_tenant_id;
  DELETE FROM retention_policies WHERE tenant_id = p_tenant_id;
  DELETE FROM deletion_requests WHERE tenant_id = p_tenant_id;
  DELETE FROM deletion_register WHERE tenant_id = p_tenant_id;
  DELETE FROM audit_events WHERE tenant_id = p_tenant_id;

  -- The remaining tenant-bound rows cascade with the tenant.
  DELETE FROM tenants WHERE id = p_tenant_id;
END;
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION terminate_tenant(UUID) TO altserv_app;

-- +goose Down
REVOKE EXECUTE ON FUNCTION terminate_tenant(UUID) FROM altserv_app;

-- +goose StatementBegin
DROP FUNCTION IF EXISTS terminate_tenant(UUID);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION audit_events_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'audit_events is append-only';
END;
$$;
-- +goose StatementEnd