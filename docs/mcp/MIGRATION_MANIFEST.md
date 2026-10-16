# Migration manifest

Apply after the existing Yandex baseline/incrementals and before enabling MCP:

```text
db/yandex/incremental/2026_09_tenderhub_mcp_v1.sql
db/yandex/incremental/2026_10_tenderhub_direct_pricing.sql
db/yandex/incremental/2026_10_tenderhub_mcp_catalog_creation.sql
db/yandex/incremental/2026_10_mcp_pricing_ownership.sql
```

Objects:

- `app_auth.oauth_clients`
- `app_auth.oauth_authorization_codes`
- `app_auth.oauth_refresh_tokens`
- `app_auth.oauth_grants`
- `public.pricing_drafts`
- `public.pricing_draft_operations`
- `public.pricing_draft_events`
- `public.boq_item_pricing_sources`
- nullable legacy `draft_operation_id` plus a direct request key, source
  library ID, match level, confidence, and warnings
- `public.mcp_direct_pricing_requests`: immutable committed-command receipt
  keyed by actor and request key for retry safety
- `public.mcp_catalog_creation_requests`: confirmed catalog inputs, actor/client,
  creation/reuse result and retry receipt; normalized-name non-unique indexes
- `pg_trgm` indexes on work/material/position names
- `public.position_pricing_owners`: persistent position calculation ownership,
  including tombstones for safe restoration
- `public.client_position_pricing_audit`: old/new GP and note, actor/request key
- ownership guards on BOQ inputs and position GP/note/deletion

The migrations are additive and idempotent. Apply the direct-pricing migration
before deploying the new BFF. `CREATE EXTENSION pg_trgm` is a hard gate for
the first migration. Production rollback disables write access but retains
audit/provenance rows and historical drafts.

Ownership requires a coordinated BFF/migration rollout with portal writes
paused; see `OWNERSHIP.md`. Verify with `scripts/mcp/verify_pricing_ownership.sql`.
