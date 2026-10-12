\set ON_ERROR_STOP on
DO $$
DECLARE missing text[] := ARRAY[]::text[];
DECLARE required_col text;
BEGIN
  IF to_regclass('public.mcp_direct_pricing_requests') IS NULL THEN
    missing := array_append(missing, 'public.mcp_direct_pricing_requests');
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                 WHERE table_schema='public' AND table_name='boq_item_pricing_sources'
                   AND column_name='source_library_id') THEN
    missing := array_append(missing, 'boq_item_pricing_sources.source_library_id');
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                 WHERE table_schema='public' AND table_name='boq_item_pricing_sources'
                   AND column_name='direct_request_key') THEN
    missing := array_append(missing, 'boq_item_pricing_sources.direct_request_key');
  END IF;
  FOREACH required_col IN ARRAY ARRAY['direct_match_level','direct_confidence','direct_warnings'] LOOP
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_schema='public' AND table_name='boq_item_pricing_sources'
                     AND column_name=required_col) THEN
      missing := array_append(missing, 'boq_item_pricing_sources.'||required_col);
    END IF;
  END LOOP;
  IF EXISTS (SELECT 1 FROM pg_attribute
             WHERE attrelid='public.boq_item_pricing_sources'::regclass
               AND attname='draft_operation_id' AND attnotnull) THEN
    missing := array_append(missing, 'draft_operation_id must be nullable');
  END IF;
  IF to_regclass('public.boq_item_pricing_sources_direct_request_idx') IS NULL THEN
    missing := array_append(missing, 'direct request provenance index');
  END IF;
  IF cardinality(missing)>0 THEN
    RAISE EXCEPTION 'MCP direct-pricing migration incomplete: %',array_to_string(missing,', ');
  END IF;
END $$;
SELECT 'MCP_DIRECT_MIGRATION_OK' AS status;
