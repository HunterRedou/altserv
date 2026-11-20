-- +goose Up
-- WP-05: an operator can suspend a tenant without deleting it. A suspended
-- tenant keeps all its data, but login and session lookup accept only 'valid'.
-- The existing values stay: 'pending' (registered, not active), 'valid'
-- (active) and 'invalid' (VIES rejected; such a tenant is deleted today).
ALTER TABLE tenants DROP CONSTRAINT tenants_status_check;
ALTER TABLE tenants ADD CONSTRAINT tenants_status_check
  CHECK (status IN ('pending', 'valid', 'invalid', 'suspended'));

-- +goose Down
-- This fails while a tenant is suspended. Activate or review those tenants
-- first; do not rewrite them to 'pending', because the VIES sweep would then
-- re-check and possibly delete them.
ALTER TABLE tenants DROP CONSTRAINT tenants_status_check;
ALTER TABLE tenants ADD CONSTRAINT tenants_status_check
  CHECK (status IN ('pending', 'valid', 'invalid'));
