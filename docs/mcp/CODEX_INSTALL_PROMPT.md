# Direct VOR pricing deployment brief

Work from a clean checkout of the authoritative HubTender repository. Review
the direct-pricing branch against the current `main`; do not reuse the old v1
handoff patch. Apply `2026_10_tenderhub_direct_pricing.sql` to a backed-up
staging database after the v1 migration. Run Go tests/build, frontend
typecheck/build, OAuth/MCP smoke, and the direct pricing UAT in `VERIFY.md`.

Keep `MCP_WRITE_ENABLED=false` until staging passes. Then enable it only for
the pilot, leave template writes disabled, and reauthorize the MCP client with
`pricing:write`. Use imported non-production tenders. Confirm that a direct
source-backed write creates audit/provenance, recalculates linked materials, bumps the
financial-input revision once, and survives a response retry without another
write. Verify stale revision/ETag and a declined confirmation leave all data
unchanged. Read the changed row and QA report back in TenderHUB VOR.

Do not deploy production while migration, exact source/rate, rollback, and
two-engineer UAT gates remain unverified. Report commit SHA, migration
checksum, commands, timestamps, and remaining blockers.

Confirm that the draft page, menu, permissions and REST/MCP lifecycle are gone.
Test work with two linked materials: manual material quantity is rejected,
conversion changes are accepted, stored consumption is preserved, and changing
the work quantity recalculates all children and position totals atomically.
Deploy backend and frontend together; old write scopes require new OAuth consent.
