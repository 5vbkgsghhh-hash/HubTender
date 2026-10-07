-- Catalog creation is separate from BOQ writes. Every committed command stores
-- its confirmed input, client, actor and result for audit and safe recovery.
CREATE TABLE IF NOT EXISTS public.mcp_catalog_creation_requests (
    actor_id uuid NOT NULL REFERENCES public.users(id),
    request_key text NOT NULL,
    request_hash text NOT NULL,
    input jsonb NOT NULL,
    result jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (actor_id, request_key)
);

-- Non-unique indexes preserve any historical duplicates. Creation returns one
-- exact match or an explicit ambiguity; it never merges existing business data.
CREATE INDEX IF NOT EXISTS mcp_material_names_normalized_idx ON public.material_names
    (lower(regexp_replace(btrim(name), '[[:space:]]+', ' ', 'g')), unit);
CREATE INDEX IF NOT EXISTS mcp_work_names_normalized_idx ON public.work_names
    (lower(regexp_replace(btrim(name), '[[:space:]]+', ' ', 'g')), unit);
