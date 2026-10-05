# Rollback

## Immediate kill switch

For a direct-pricing problem, set `MCP_WRITE_ENABLED=false` and restart the
BFF; read-only MCP remains available. To disable MCP entirely, set:

```dotenv
MCP_ENABLED=false
MCP_WRITE_ENABLED=false
MCP_TEMPLATE_WRITE_ENABLED=false
MCP_DCR_ENABLED=false
```

This removes OAuth/MCP routes and stops all new MCP writes. Existing portal
functionality remains available.

## Revoke access

Users can revoke individual clients in `/settings/agents`. Administrators may
also mark grants and refresh tokens revoked in `app_auth` using an approved
incident runbook. Never print raw tokens; only hashes are stored.

## Code rollback

Revert the MCP commit, rebuild backend/frontend, run the ordinary smoke suite,
and restore the previous nginx vhost after `nginx -t`.

## Database rollback policy

The migrations are additive. In production, leave their tables/indexes in place
when disabling MCP so audit/provenance is retained. Do not drop
`boq_item_pricing_sources`, direct request keys, historical draft events, or
OAuth grants after real use. A direct BOQ update can be reverted through the
existing VOR audit rollback after checking for later edits; a newly created
item can be deleted through the normal VOR action with its current ETag.

Dropping MCP tables is permitted only on an empty/disposable staging database
after confirming there are no applied drafts. There is intentionally no
automated destructive down script in this package.
