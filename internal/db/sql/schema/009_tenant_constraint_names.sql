-- +goose Up
ALTER TABLE tenants RENAME CONSTRAINT firms_pkey TO tenants_pkey;
ALTER TABLE tenants RENAME CONSTRAINT firms_status_check TO tenants_status_check;

-- +goose Down
ALTER TABLE tenants RENAME CONSTRAINT tenants_pkey TO firms_pkey;
ALTER TABLE tenants RENAME CONSTRAINT tenants_status_check TO firms_status_check;
