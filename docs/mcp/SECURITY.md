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
- Existing portal library edit/delete and template mutations retain their senior
  role gates. Explicitly approved MCP catalog creation additionally allows the
  current `engineer` role with `/library`, separate create-scopes, both write
  flags and confirmation. It never edits/deletes an existing shared record.
- Direct pricing requires `pricing:write`, the `MCP_WRITE_ENABLED` gate, a
  matching position calculation owner (unknown/mixed legacy owners fail closed),
  current financial-input revision, and (for updates) the item's ETag. The MCP
  tool requires explicit confirmation before the transaction, including all
  linked children. Clients that advertise form elicitation on protocol
  `>= 2026-07-28` receive `InputRequests` with `mode=form` and a boolean
  `requestedSchema`. Clients without elicitation (including Cursor on
  Stateless Streamable HTTP, where server→client `elicitation/create` cannot
  complete) receive `CONFIRMATION_REQUIRED` with `confirmation_state` and must
  retry with `confirmation_state` + `user_confirmed=true` only after human
  approval. The canonical Go calculation kernel computes the total.
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
- Position ownership is also enforced by database triggers for authenticated
  portal/API writes, including import, clearing, restore and cascading deletion.
  A senior role does not bypass it; journal visibility does not permit cancelling
  another user's import. Verified context identity overrides helper/body values.
- Nomenclature names/units are locked and included in confirmation along with
  the effective target snapshot and previous GP. Changed names invalidate consent.
- The privileged database/BFF and background jobs without a delegated actor
  remain trusted. Do not expose the runtime database credentials to clients.
