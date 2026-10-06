# Verification and acceptance gates

Every command must pass on the colleague's final checkout.

## Static/build gates

```bash
cd backend
go vet ./...
go test -p 1 ./...
go build ./cmd/server
cd ..
npm ci
npm run lint
npm run typecheck
VITE_API_URL=https://staging.example npm run build
```

Run `scripts/mcp/preflight.ps1` on Windows or `scripts/mcp/preflight.sh` on
Linux before and after applying the patch.

## Database gates

```bash
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 \
  -f scripts/mcp/verify_migration.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 \
  -f scripts/mcp/verify_direct_migration.sql
```

Expected: `MCP_MIGRATION_OK` and `MCP_DIRECT_MIGRATION_OK`. No production
data is changed.

## HTTP/MCP gates

1. Run `scripts/mcp/smoke.ps1 -BaseUrl https://staging.example` without a token.
   Discovery must be public and `/mcp` must return 401 with `WWW-Authenticate`.
2. Complete OAuth in the browser and rerun with `-AccessToken`.
3. `server/discover` and `tools/list` must return JSON-RPC results.
4. Confirm 15 tools, including `tenderhub_list_cost_categories`, `tenderhub_list_boq_items`,
   `tenderhub_get_boq_item`, `tenderhub_get_direct_pricing_receipt`, and
   `tenderhub_price_boq_item`. No draft tool
   may be advertised. Every tool has input/output schema and annotations.
5. Revoke the grant in `/settings/agents`; the same access token must receive
   401 immediately.

## Pricing UAT

- Exact archived item: rate/currency/source date and quote link are preserved.
- USD/EUR/CNY source: target total uses the target tender FX; source historical
  RUB is display evidence only.
- Press-compactor search never returns `Монтаж мебели` as an eligible match.
- A source older than 180 days and a >50% median outlier are visibly warned.
- Unit mismatch, zero source rate, missing target FX, stale financial revision,
  and stale item ETag block a direct write before commit.
- A source-backed write adds/updates its BOQ row, audit and provenance; a
  quantity/conversion-only write preserves price provenance. Each command bumps
  the tender financial input revision once. Work updates also audit every
  recalculated child in the same transaction.
- Repeating the same request key and identical inputs returns the original
  result without a second BOQ row or revision bump. Reusing the key with
  different inputs fails.
- Linked material creation rejects any explicit `quantity`; server quantity is
  parent quantity × conversion × stored consumption. Unknown consumption/base
  quantity inputs must be rejected by the MCP schema.
- Existing linked material repricing preserves parent, conversion and consumption.
- Changing conversion recalculates that material; changing work quantity
  recalculates at least two linked materials and position totals atomically.
- Test different units: a new link requires explicit conversion; same-unit links
  can default to 1. Missing/foreign/material parents are rejected.
- A failed child calculation rolls back work, all children, audit, provenance,
  receipt and revision together. Repeat the same request after correcting the
  cause, and ensure exactly one committed command.
- Two commands with different keys and the same financial revision must produce
  one successful write and one stale conflict, with no FK-lock deadlock.
- A portal-locked target/parent/child/source must produce an actionable busy
  conflict without waiting in a lock-order cycle or keeping a receipt.
- A selected source version must be rejected if currency, units, delivery,
  consumption or quote evidence changes before service validation or DB commit.
- Search `source_version` must match the commit lookup token. Changing the source
  requires a fresh selection; use `expected_source_version` for archive/library.
- Repricing must replace quote link/date/expiry together, clearing missing fields.
- Receipt replay must restore invalidation/enqueue after a missed post-commit
  callback, without another BOQ change or financial input revision.
- Engineer receives 403 for shared template mutations; leading engineer,
  administrator, and developer can proceed only after elicited confirmation.
- QA direct total equals a fresh TenderHUB reread; every directly priced row has an
  append-only `boq_item_pricing_sources` record.

## Performance targets

On a production-like staging snapshot:

- archive search p95 <= 2 seconds;
- pricing state p95 <= 3 seconds;
- measure direct write p95 on a production-like staging snapshot before rollout.

For a disposable benchmark DB, apply `evaluation_fixture.sql` and
`performance_fixture.sql`, then run the integration performance test with
`RUN_MCP_PERF_TESTS=1`. Remove both fixtures afterward; never seed production.

Do not deploy when any gate is red. Record commands, timestamps, commit SHA,
migration checksum, and actual results in the handoff verification report.
