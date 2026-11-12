-- +goose Up
-- Catalog administration (next step 1).
--
-- Global reference data (service_catalogs, service_definitions) stays
-- read-only for the application role. Two reviewed SECURITY DEFINER functions
-- are the only write path. They validate the row, and they require a source
-- note so an unverified catalog cannot be loaded by accident (doc 9).
--
-- The functions are owned by the migration role. The application role may only
-- execute them. It never gets INSERT, UPDATE or DELETE on the global tables.

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION create_service_catalog(
  p_code TEXT,
  p_name TEXT,
  p_region TEXT,
  p_source_note TEXT,
  p_valid_from TIMESTAMP,
  p_valid_until TIMESTAMP
) RETURNS SETOF service_catalogs
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
  v_code TEXT := btrim(coalesce(p_code, ''));
  v_name TEXT := btrim(coalesce(p_name, ''));
  v_region TEXT := btrim(coalesce(p_region, ''));
  v_source_note TEXT := btrim(coalesce(p_source_note, ''));
BEGIN
  IF v_code = '' THEN
    RAISE EXCEPTION 'catalog code is required' USING ERRCODE = '22023';
  END IF;
  IF length(v_code) > 100 THEN
    RAISE EXCEPTION 'catalog code is too long' USING ERRCODE = '22023';
  END IF;
  IF v_name = '' THEN
    RAISE EXCEPTION 'catalog name is required' USING ERRCODE = '22023';
  END IF;
  IF length(v_name) > 300 THEN
    RAISE EXCEPTION 'catalog name is too long' USING ERRCODE = '22023';
  END IF;
  IF v_region = '' THEN
    v_region := 'brandenburg';
  END IF;
  IF length(v_region) > 100 THEN
    RAISE EXCEPTION 'catalog region is too long' USING ERRCODE = '22023';
  END IF;
  IF v_source_note = '' THEN
    RAISE EXCEPTION 'catalog source note is required' USING ERRCODE = '22023';
  END IF;
  IF length(v_source_note) > 1000 THEN
    RAISE EXCEPTION 'catalog source note is too long' USING ERRCODE = '22023';
  END IF;
  IF p_valid_from IS NULL THEN
    RAISE EXCEPTION 'catalog valid_from is required' USING ERRCODE = '22023';
  END IF;
  IF p_valid_until IS NOT NULL AND p_valid_until <= p_valid_from THEN
    RAISE EXCEPTION 'catalog valid_until must be after valid_from' USING ERRCODE = '22023';
  END IF;

  RETURN QUERY
    INSERT INTO service_catalogs (
      code, name, region, source_note, valid_from, valid_until
    )
    VALUES (v_code, v_name, v_region, v_source_note, p_valid_from, p_valid_until)
    RETURNING *;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION create_service_definition(
  p_catalog_id UUID,
  p_code TEXT,
  p_name TEXT,
  p_description TEXT,
  p_legal_area TEXT,
  p_unit TEXT,
  p_valid_from TIMESTAMP,
  p_valid_until TIMESTAMP
) RETURNS SETOF service_definitions
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
  v_code TEXT := btrim(coalesce(p_code, ''));
  v_name TEXT := btrim(coalesce(p_name, ''));
  v_description TEXT := btrim(coalesce(p_description, ''));
  v_legal_area TEXT := btrim(coalesce(p_legal_area, ''));
  v_unit TEXT := btrim(coalesce(p_unit, ''));
  v_catalog service_catalogs%ROWTYPE;
BEGIN
  IF p_catalog_id IS NULL THEN
    RAISE EXCEPTION 'catalog is required' USING ERRCODE = '22023';
  END IF;
  IF v_code = '' THEN
    RAISE EXCEPTION 'service code is required' USING ERRCODE = '22023';
  END IF;
  IF length(v_code) > 100 THEN
    RAISE EXCEPTION 'service code is too long' USING ERRCODE = '22023';
  END IF;
  IF v_name = '' THEN
    RAISE EXCEPTION 'service name is required' USING ERRCODE = '22023';
  END IF;
  IF length(v_name) > 300 THEN
    RAISE EXCEPTION 'service name is too long' USING ERRCODE = '22023';
  END IF;
  IF length(v_description) > 2000 THEN
    RAISE EXCEPTION 'service description is too long' USING ERRCODE = '22023';
  END IF;
  IF v_legal_area NOT IN ('sgb_xi', 'sgb_v') THEN
    RAISE EXCEPTION 'legal area must be sgb_xi or sgb_v' USING ERRCODE = '22023';
  END IF;
  IF length(v_unit) > 50 THEN
    RAISE EXCEPTION 'service unit is too long' USING ERRCODE = '22023';
  END IF;
  IF p_valid_from IS NULL THEN
    RAISE EXCEPTION 'service valid_from is required' USING ERRCODE = '22023';
  END IF;
  IF p_valid_until IS NOT NULL AND p_valid_until <= p_valid_from THEN
    RAISE EXCEPTION 'service valid_until must be after valid_from' USING ERRCODE = '22023';
  END IF;

  SELECT * INTO v_catalog FROM service_catalogs WHERE id = p_catalog_id;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'catalog not found' USING ERRCODE = '23503';
  END IF;
  IF p_valid_from < v_catalog.valid_from THEN
    RAISE EXCEPTION 'service starts before the catalog' USING ERRCODE = '22023';
  END IF;
  IF v_catalog.valid_until IS NOT NULL
     AND (p_valid_until IS NULL OR p_valid_until > v_catalog.valid_until) THEN
    RAISE EXCEPTION 'service ends after the catalog' USING ERRCODE = '22023';
  END IF;

  RETURN QUERY
    INSERT INTO service_definitions (
      catalog_id, code, name, description, legal_area, unit,
      valid_from, valid_until
    )
    VALUES (
      p_catalog_id, v_code, v_name, v_description, v_legal_area, v_unit,
      p_valid_from, p_valid_until
    )
    RETURNING *;
END;
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION
  create_service_catalog(TEXT, TEXT, TEXT, TEXT, TIMESTAMP, TIMESTAMP)
  TO altserv_app;

GRANT EXECUTE ON FUNCTION
  create_service_definition(UUID, TEXT, TEXT, TEXT, TEXT, TEXT, TIMESTAMP, TIMESTAMP)
  TO altserv_app;

-- +goose Down
DROP FUNCTION IF EXISTS
  create_service_definition(UUID, TEXT, TEXT, TEXT, TEXT, TEXT, TIMESTAMP, TIMESTAMP);
DROP FUNCTION IF EXISTS
  create_service_catalog(TEXT, TEXT, TEXT, TEXT, TIMESTAMP, TIMESTAMP);
