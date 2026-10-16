-- Run on staging first. Ownership belongs to a POSITION calculation, not to
-- the tender creator or last editor. No automatic claim of legacy unknowns.
BEGIN;

CREATE TABLE IF NOT EXISTS public.position_pricing_owners (
 client_position_id uuid PRIMARY KEY,
 owner_id uuid REFERENCES public.users(id) ON DELETE SET NULL,
 origin text NOT NULL,
 assigned_at timestamptz NOT NULL DEFAULT now()
);
-- No FK to client_positions: retain the owner after deletion for safe restore.
CREATE INDEX IF NOT EXISTS position_pricing_owners_owner_idx ON public.position_pricing_owners(owner_id);
CREATE INDEX IF NOT EXISTS boq_items_first_insert_actor_idx
 ON public.boq_items_audit(boq_item_id,changed_at,id) WHERE operation_type='INSERT';

WITH first_insert AS (
 SELECT DISTINCT ON (boq_item_id) boq_item_id,changed_by
 FROM public.boq_items_audit WHERE operation_type='INSERT'
 ORDER BY boq_item_id,changed_at,id
), evidence AS (
 SELECT b.client_position_id,COALESCE(u.id,iu.id) AS original_actor
 FROM public.boq_items b
 LEFT JOIN first_insert f ON f.boq_item_id=b.id
 LEFT JOIN public.users u ON u.id=f.changed_by
 LEFT JOIN public.import_sessions i ON i.id=b.import_session_id
 LEFT JOIN public.users iu ON iu.id=i.user_id
), per_position AS (
 SELECT client_position_id,
   CASE WHEN count(*)=count(original_actor) AND count(DISTINCT original_actor)=1
     THEN min(original_actor::text)::uuid ELSE NULL END AS owner_id,
   CASE WHEN count(*)<>count(original_actor) THEN 'legacy-unassigned'
     WHEN count(DISTINCT original_actor)>1 THEN 'legacy-mixed'
     ELSE 'legacy-creation-evidence' END AS origin
 FROM evidence GROUP BY client_position_id
)
INSERT INTO public.position_pricing_owners(client_position_id,owner_id,origin)
 SELECT client_position_id,owner_id,origin FROM per_position
 ON CONFLICT(client_position_id) DO NOTHING;

-- Retain ownership for calculations already deleted before this migration.
-- A deletion/last edit does not prove authorship: use the original INSERT.
WITH first_insert AS (
 SELECT DISTINCT ON (boq_item_id) boq_item_id,changed_by
 FROM public.boq_items_audit WHERE operation_type='INSERT' ORDER BY boq_item_id,changed_at,id
), history AS (
 SELECT DISTINCT a.boq_item_id,cp.id AS client_position_id
 FROM public.boq_items_audit a JOIN public.client_positions cp
 ON cp.id::text=COALESCE(a.new_data->>'client_position_id',a.old_data->>'client_position_id')
), evidence AS (
 SELECT h.client_position_id,u.id AS original_actor FROM history h
 LEFT JOIN first_insert f ON f.boq_item_id=h.boq_item_id
 LEFT JOIN public.users u ON u.id=f.changed_by
)
INSERT INTO public.position_pricing_owners(client_position_id,owner_id,origin)
 SELECT client_position_id,
 CASE WHEN count(*)=count(original_actor) AND count(DISTINCT original_actor)=1 THEN min(original_actor::text)::uuid ELSE NULL END,
 CASE WHEN count(*)=count(original_actor) AND count(DISTINCT original_actor)=1 THEN 'legacy-deleted-creation-evidence' ELSE 'legacy-unassigned-history' END
 FROM evidence GROUP BY client_position_id ON CONFLICT(client_position_id) DO NOTHING;

CREATE TABLE IF NOT EXISTS public.client_position_pricing_audit (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 client_position_id uuid NOT NULL,
 tender_id uuid NOT NULL,
 actor_id uuid REFERENCES public.users(id) ON DELETE SET NULL,
 request_key text,
 old_manual_volume numeric,new_manual_volume numeric,
 old_manual_note text,new_manual_note text,
 changed_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS client_position_pricing_audit_position_idx
 ON public.client_position_pricing_audit(client_position_id,changed_at);

CREATE OR REPLACE FUNCTION public.assert_position_pricing_owner(p_position uuid,p_claim boolean DEFAULT false)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE actor uuid:=auth.uid(); recorded_owner uuid; present boolean;
BEGIN
 IF actor IS NULL THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='PRICING_ACTOR_REQUIRED';
 END IF;
 -- Row lock also serialises first claim with concurrent inserts. NOWAIT
 -- avoids a cycle with existing portal row-before-position write paths.
 PERFORM id FROM public.client_positions WHERE id=p_position FOR UPDATE NOWAIT;
 SELECT owner_id INTO recorded_owner FROM public.position_pricing_owners
  WHERE client_position_id=p_position FOR UPDATE NOWAIT;
 present:=FOUND;
 IF NOT present THEN
  IF EXISTS(SELECT 1 FROM public.boq_items WHERE client_position_id=p_position) THEN
   RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='PRICING_OWNER_UNASSIGNED: calculation author must be resolved before writing';
  END IF;
  IF NOT p_claim THEN RETURN; END IF;
  INSERT INTO public.position_pricing_owners(client_position_id,owner_id,origin)
   VALUES(p_position,actor,'authenticated-first-write');
  recorded_owner:=actor;
 END IF;
 IF recorded_owner IS NULL THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='PRICING_OWNER_UNASSIGNED: unknown or mixed legacy authors';
 END IF;
 IF recorded_owner<>actor THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='PRICING_OWNER_MISMATCH: only the calculation owner may change or delete it';
 END IF;
END $$;

CREATE OR REPLACE FUNCTION public.guard_boq_pricing_owner()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 -- Background calculations/privileged maintenance have no delegated actor.
 -- Every HTTP write sets the verified actor transaction-locally in the BFF.
 IF auth.uid() IS NULL THEN
  IF TG_OP='DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='INSERT' THEN
  PERFORM public.assert_position_pricing_owner(NEW.client_position_id,true);
 ELSIF TG_OP='DELETE' THEN
  PERFORM public.assert_position_pricing_owner(OLD.client_position_id,false);
 ELSE
  -- Derived costs are recalculated by the server across the tender. They
  -- neither transfer ownership nor authorise changing another user's inputs.
  IF (to_jsonb(NEW)-ARRAY['updated_at','total_amount','commercial_markup',
      'total_commercial_material_cost','total_commercial_work_cost']) IS DISTINCT FROM
     (to_jsonb(OLD)-ARRAY['updated_at','total_amount','commercial_markup',
      'total_commercial_material_cost','total_commercial_work_cost']) THEN
   PERFORM public.assert_position_pricing_owner(OLD.client_position_id,false);
   IF NEW.client_position_id<>OLD.client_position_id THEN
    PERFORM public.assert_position_pricing_owner(NEW.client_position_id,true);
   END IF;
  END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS guard_boq_pricing_owner ON public.boq_items;
CREATE TRIGGER guard_boq_pricing_owner BEFORE INSERT OR UPDATE OR DELETE ON public.boq_items
 FOR EACH ROW EXECUTE FUNCTION public.guard_boq_pricing_owner();

CREATE OR REPLACE FUNCTION public.guard_position_pricing_owner()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF auth.uid() IS NULL THEN
  IF TG_OP='DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='DELETE' THEN
  PERFORM public.assert_position_pricing_owner(OLD.id,false);
  RETURN OLD;
 END IF;
 IF NEW.manual_volume IS DISTINCT FROM OLD.manual_volume OR NEW.manual_note IS DISTINCT FROM OLD.manual_note THEN
  PERFORM public.assert_position_pricing_owner(OLD.id,true);
  INSERT INTO public.client_position_pricing_audit(client_position_id,tender_id,actor_id,request_key,
   old_manual_volume,new_manual_volume,old_manual_note,new_manual_note)
   VALUES(OLD.id,OLD.tender_id,auth.uid(),NULLIF(current_setting('app.pricing_request_key',true),''),
    OLD.manual_volume,NEW.manual_volume,OLD.manual_note,NEW.manual_note);
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS guard_position_pricing_owner ON public.client_positions;
CREATE TRIGGER guard_position_pricing_owner BEFORE UPDATE OR DELETE ON public.client_positions
 FOR EACH ROW EXECUTE FUNCTION public.guard_position_pricing_owner();
COMMIT;
