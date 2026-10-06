# TenderHUB MCP: direct pricing in VOR

The Go BFF exposes stateless Streamable HTTP at `POST /mcp`.
MCP v2 writes directly to the existing VOR/BOQ. There is no separate draft
section, draft tool, draft REST endpoint or draft lifecycle in the runtime.
The old `/pricing-drafts` URL redirects to `/positions`.

## Workflow

1. Read an imported tender, its `financial_input_revision`, and target BOQ rows.
2. Select an archive/library source; inspect units, source date, rate and warnings.
   Copy its `source_version` into `expected_source_version`. Currency, delivery,
   consumption, units or quote changes invalidate the selection even if the
   numeric rate stays the same. Search again after a stale-source rejection.
3. Call `tenderhub_price_boq_item` with a unique `request_key` and current revision.
   Updates also require the item ETag. Confirm the requested direct change.
4. Read the changed row, linked materials and QA back. An identical retry
   returns the committed receipt without applying the command again.
   Receipt replay also repairs cache invalidation and background enqueue if
   the earlier process stopped after database commit.

| Operation | Inputs |
| --- | --- |
| Create work or standalone material | `source_kind=archive/library`, source ID/rate, positive `quantity`; library also needs kind/category |
| Create linked material | Archive/library source, `parent_work_item_id`; omit `quantity`. Supply `conversion_coefficient` when units differ |
| Reprice existing item | Archive/library source, target item ID/ETag; binding and consumption are preserved |
| Change work or standalone volume | `source_kind=current`, target item ID/ETag, `quantity`; omit source ID/rate |
| Change linked material unit conversion | `source_kind=current`, target item ID/ETag, `conversion_coefficient`; omit `quantity` and source ID/rate |

For `current`, also omit `expected_source_version`. A busy portal/agent row
returns `DIRECT_PRICING_BUSY` without a partial write; reload and retry after
the other edit finishes. Repricing replaces quote evidence with the new source;
managed-library prices clear the preceding source's quote link and dates.

### Linked quantities

`material.quantity = work.quantity × conversion_coefficient × consumption_coefficient`

Missing coefficients mean 1. All explicit inputs and derived quantities must
be finite and positive. A linked quantity is server-owned: sending `quantity`
fails with `LINKED_MATERIAL_QUANTITY_READ_ONLY`. MCP exposes no consumption
edit, raw base quantity, unlinking or arbitrary-rate input. An existing target
recipe is preserved when importing a new price; a newly added material uses
the selected source's consumption. A work change recalculates **all** linked
materials, amounts and position totals in the same transaction as work, audit
and the one financial revision bump. A calculation failure rolls back all rows.

Templates remain in the existing VOR UI. MCP can read a template and add its
selected library works/materials directly, linking materials to the resulting
work IDs. Each confirmed write returns the next revision; no draft is created.

## Migration and access

Apply `2026_10_tenderhub_direct_pricing.sql` after the v1 MCP migration.
Historical draft tables are retained solely to preserve existing provenance;
they are not exposed as an active workflow. Do not delete business/audit data.
Direct writes require `pricing:write` and `MCP_WRITE_ENABLED=true`; the retired
`pricing:draft`/`pricing:apply` scopes do not grant direct access. Register or
reconnect a client with the new scopes. Template editing retains its separate gate.

## Documents

- [INSTALL.md](INSTALL.md) — build, additive migration and reconnect.
- [VERIFY.md](VERIFY.md) — local and staging acceptance scenarios.
- [TEST_REPORT.md](TEST_REPORT.md) — evidence from this revision.
- [ROLLBACK.md](ROLLBACK.md) — disable writes and restore BOQ via VOR audit.
- [SECURITY.md](SECURITY.md) — scopes, receipts, transaction and source rules.
- [MIGRATION_MANIFEST.md](MIGRATION_MANIFEST.md) — database object changes.
