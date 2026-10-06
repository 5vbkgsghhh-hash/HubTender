# Security model

- MCP uses a dedicated JWT audience (`hubtender-mcp`); portal JWTs are rejected.
- OAuth Authorization Code requires PKCE S256, exact registered redirects,
  state round-trip, issuer echo, hashed single-use codes, rotating hashed
  refresh tokens, and reuse-family revocation.
- DCR accepts public clients only, validates HTTPS/loopback redirect URIs and is
  rate-limited. CIMD is HTTPS-only, host-allowlisted, IP-pinned, size-limited,
  redirect-free, and rejects private/link-local addresses.
- Every MCP request rechecks the active grant and current TenderHUB user status.
- Tool handlers also enforce OAuth scope, portal page access, and role.
- Shared library/template writes are server-gated to `veduschiy_inzhener`,
  `administrator`, and `developer`; UI visibility is not trusted.
- Direct pricing requires `pricing:write`, the `MCP_WRITE_ENABLED` gate, a
  current financial-input revision, and (for updates) the item's ETag. The MCP
  tool elicits explicit confirmation before the transaction, including all linked children. The
  canonical Go calculation kernel computes the total.
- One actor/request key has one committed-command receipt. Repeating identical
  inputs returns that receipt; reusing the key for different inputs is rejected.
  Audit and source provenance commit with the BOQ item.
- Logs contain user/client/tool/status/duration only, never bearer tokens,
  arguments, full payloads, quote documents, or secrets.
- The handoff ZIP must not contain `.env`, credentials, database exports,
  customer BOQ data, OAuth tokens, or private keys.

- Linked quantity protection is enforced by the service **and** commit boundary.
  Quantity is derived from the locked parent; consumption is copied only for
  creation and then preserved. MCP accepts only conversion edits for linked
  materials. The MCP schema rejects unknown consumption/base/unlink fields.
- Full source versions are rechecked under row locks in the commit transaction.
  The command result includes actual quantity and all recalculated children.
- Retired draft/apply scopes are no longer accepted for new authorization.
  Existing tokens never acquire `pricing:write` implicitly; reconnect explicitly.
- Receipt FK checks use tender KEY SHARE; revision serialization uses NO KEY
  UPDATE to avoid mutually blocked lock upgrades between simultaneous receipts.
  Source/position/item/parent/child locks use NOWAIT for MCP writes, so portal
  lock ordering returns a conflict rather than forming a wait cycle.
