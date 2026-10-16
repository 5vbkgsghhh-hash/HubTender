-- Read-only structural verification. Run as the BFF runtime role after rollout.
BEGIN READ ONLY;
DO $$
BEGIN
 IF to_regclass('public.position_pricing_owners') IS NULL
 OR to_regclass('public.client_position_pricing_audit') IS NULL
 OR to_regprocedure('public.assert_position_pricing_owner(uuid,boolean)') IS NULL THEN
  RAISE EXCEPTION 'MCP_PRICING_OWNERSHIP_MISSING';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='public.boq_items'::regclass
   AND tgname='guard_boq_pricing_owner' AND tgenabled='O')
 OR NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='public.client_positions'::regclass
   AND tgname='guard_position_pricing_owner' AND tgenabled='O') THEN
  RAISE EXCEPTION 'MCP_PRICING_OWNERSHIP_GUARD_MISSING';
 END IF;
END $$;
SELECT origin,count(*) AS positions,count(*) FILTER(WHERE owner_id IS NULL) AS unresolved
 FROM public.position_pricing_owners GROUP BY origin ORDER BY origin;
SELECT count(*) AS current_positions_missing_owner FROM (
 SELECT DISTINCT b.client_position_id FROM public.boq_items b
 LEFT JOIN public.position_pricing_owners o ON o.client_position_id=b.client_position_id
 WHERE o.client_position_id IS NULL
) missing;
SELECT count(*) AS gp_audit_records FROM public.client_position_pricing_audit;
SELECT 'MCP_PRICING_OWNERSHIP_SCHEMA_OK' AS result;
COMMIT;
