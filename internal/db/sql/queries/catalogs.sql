-- name: ListServiceCatalogs :many
SELECT * FROM service_catalogs
WHERE sqlc.arg(include_future)::boolean
   OR (
     valid_from <= clock_timestamp()
     AND (valid_until IS NULL OR valid_until > clock_timestamp())
   )
ORDER BY valid_from DESC, name;

-- name: GetServiceCatalog :one
SELECT * FROM service_catalogs
WHERE id = $1;

-- name: GetServiceDefinition :one
SELECT * FROM service_definitions
WHERE id = $1;

-- name: SearchServiceDefinitions :many
SELECT d.* FROM service_definitions d
JOIN service_catalogs c ON c.id = d.catalog_id
WHERE (
    sqlc.arg(catalog_id)::uuid = '00000000-0000-0000-0000-000000000000'::uuid
    OR d.catalog_id = sqlc.arg(catalog_id)
  )
  AND (sqlc.arg(legal_area)::text = '' OR d.legal_area = sqlc.arg(legal_area))
  AND d.valid_from <= clock_timestamp()
  AND (d.valid_until IS NULL OR d.valid_until > clock_timestamp())
  AND c.valid_from <= clock_timestamp()
  AND (c.valid_until IS NULL OR c.valid_until > clock_timestamp())
  AND (
    sqlc.arg(query)::text = ''
    OR d.name ILIKE '%' || sqlc.arg(query) || '%'
    OR d.code ILIKE '%' || sqlc.arg(query) || '%'
  )
ORDER BY
  CASE
    WHEN sqlc.arg(query)::text <> '' AND d.code ILIKE sqlc.arg(query) || '%' THEN 0
    WHEN sqlc.arg(query)::text <> '' AND d.name ILIKE sqlc.arg(query) || '%' THEN 1
    WHEN sqlc.arg(query)::text <> '' AND d.name ILIKE '%' || sqlc.arg(query) || '%' THEN 2
    ELSE 3
  END,
  d.name,
  d.valid_from DESC
LIMIT sqlc.arg(max_results);
