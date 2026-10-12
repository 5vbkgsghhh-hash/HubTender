\set ON_ERROR_STOP on
DO $$ BEGIN
  IF to_regclass('public.mcp_catalog_creation_requests') IS NULL
     OR to_regclass('public.mcp_material_names_normalized_idx') IS NULL
     OR to_regclass('public.mcp_work_names_normalized_idx') IS NULL THEN
    RAISE EXCEPTION 'MCP catalog creation migration is incomplete';
  END IF;
END $$;
SELECT 'MCP_CATALOG_MIGRATION_OK' AS status;
