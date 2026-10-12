-- Direct MCP pricing writes commit to BOQ without creating a pricing draft.
-- Existing draft-backed provenance remains readable for historical records.
ALTER TABLE public.boq_item_pricing_sources
    ALTER COLUMN draft_operation_id DROP NOT NULL,
    ADD COLUMN IF NOT EXISTS source_library_id uuid,
    ADD COLUMN IF NOT EXISTS direct_request_key text,
    ADD COLUMN IF NOT EXISTS direct_match_level text,
    ADD COLUMN IF NOT EXISTS direct_confidence numeric,
    ADD COLUMN IF NOT EXISTS direct_warnings jsonb;

CREATE UNIQUE INDEX IF NOT EXISTS boq_item_pricing_sources_direct_request_idx
    ON public.boq_item_pricing_sources (applied_by, direct_request_key)
    WHERE direct_request_key IS NOT NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'boq_item_pricing_sources_origin_check'
          AND conrelid = 'public.boq_item_pricing_sources'::regclass
    ) THEN
        ALTER TABLE public.boq_item_pricing_sources
            ADD CONSTRAINT boq_item_pricing_sources_origin_check
            CHECK (
                (draft_operation_id IS NOT NULL AND direct_request_key IS NULL)
                OR (draft_operation_id IS NULL AND direct_request_key IS NOT NULL
                    AND direct_match_level IS NOT NULL AND direct_confidence IS NOT NULL
                    AND direct_confidence >= 0 AND direct_confidence <= 1
                    AND direct_warnings IS NOT NULL)
            );
    END IF;
END $$;

-- A committed command receipt survives later BOQ edits/deletion. It is not a
-- proposal or draft: the placeholder and BOQ mutation commit in one tx.
CREATE TABLE IF NOT EXISTS public.mcp_direct_pricing_requests (
    actor_id uuid NOT NULL REFERENCES public.users(id),
    request_key text NOT NULL,
    request_hash text NOT NULL,
    tender_id uuid NOT NULL REFERENCES public.tenders(id),
    result jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (actor_id, request_key)
);
