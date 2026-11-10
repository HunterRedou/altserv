-- name: CreateTemplate :one
INSERT INTO templates (tenant_id, patient_id, name, created_by, version, status)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: CreateTemplateActivity :one
INSERT INTO template_activities (tenant_id, template_id, activity_id, default_quantity, sort_order)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetTemplate :one
SELECT * FROM templates
WHERE id = $1
  AND tenant_id = $2;

-- name: GetActiveTemplate :one
SELECT * FROM templates
WHERE id = $1
  AND tenant_id = $2
  AND status = 'active';

-- name: GetActiveTemplateByName :one
SELECT * FROM templates
WHERE tenant_id = $1
  AND patient_id = $2
  AND name = $3
  AND status = 'active';

-- name: ListTemplatesByPatient :many
SELECT * FROM templates
WHERE tenant_id = $1
  AND patient_id = $2
  AND status = 'active'
ORDER BY name;

-- name: GetLatestTemplateVersion :one
SELECT COALESCE(MAX(version), 0)::int AS version
FROM templates
WHERE tenant_id = $1
  AND patient_id = $2
  AND name = $3;

-- name: SupersedeTemplateVersion :exec
UPDATE templates
SET status = 'superseded', updated_at = NOW()
WHERE id = $1
  AND tenant_id = $2
  AND status = 'active';

-- name: ArchiveTemplate :execrows
UPDATE templates
SET status = 'archived', updated_at = NOW()
WHERE id = $1
  AND tenant_id = $2
  AND status = 'active';

-- name: ListTemplateActivities :many
SELECT ta.id, ta.tenant_id, ta.template_id, ta.activity_id,
       ta.default_quantity, ta.sort_order,
       sd.code AS service_code, sd.name AS service_name,
       sd.legal_area, sd.unit
FROM template_activities ta
JOIN service_definitions sd ON sd.id = ta.activity_id
WHERE ta.tenant_id = $1
  AND ta.template_id = $2
ORDER BY ta.sort_order, ta.created_at;
