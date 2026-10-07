# MCP v2 direct VOR verification

Dates: initial verification 2026-10-05; self-review 2026-10-06;
catalog creation verification 2026-10-07, Europe/Moscow.
Base: `baldmaxim/HubTender@2b2e8674b90e7d3f522db6f5d608ecc630360ffe`.
All write tests used a disposable local PostgreSQL 17 instance, not production.

## Passed

- Go unit tests for MCP, services, repository, OAuth, calc, handlers and server.
- Full `go test -p 1 ./...`: all runnable packages passed. Windows denied the
  generated `apikey.test.exe` filename; that same package was compiled with
  `go test -c -o .../mcp-credential-tests.exe ./internal/apikey` and executed: PASS.
- `go vet ./...`.
- Backend build: `go build -buildvcs=false ./cmd/server`. Host VCS discovery
  picks the user-profile repository instead of this linked worktree; disabling
  stamping addresses that host issue. Use normal build in the final checkout.
- Frontend TypeScript/Vite/PWA production build; ESLint with zero warnings.
  Vite retains existing chunk-size/eval/import warnings outside this change.
- Full Yandex baseline and incremental migrations through October; v1 MCP
  migration and direct-write migration; repeated direct migration application.
  Verification returns `MCP_MIGRATION_OK` and `MCP_DIRECT_MIGRATION_OK`.
- Real PostgreSQL direct-write tests:
  - source-backed creation/update commits BOQ, audit, provenance and revision;
  - no draft rows are created;
  - identical retry returns the original receipt; changed inputs with the same
    key fail; receipt replay works with the write gate off;
  - new writes fail with the gate off;
  - stale revision, source rate and ETag fail;
  - archive and library linked materials derive quantities from the work and
    conversion/consumption coefficients; consumption is not applied twice;
  - explicit linked quantity fails in the service and transaction boundary;
  - repricing from either source preserves the target's stored consumption;
  - conversion-only change recalculates the linked material;
  - work-volume change recalculates two materials and position totals, with
    one financial revision and an audit for each affected row;
  - missing FX on one child rolls back work, children, revision and receipt;
  - rejected operations leave no receipt or draft behind.
  - material, foreign-tender and missing parents are rejected;
  - different units require explicit conversion; an explicit conversion derives
    the quantity using the same parent/consumption formula.
- Real HTTP MCP SDK round trip: 21 typed tools, confirmation, write/retry,
  committed receipt output validation, archive search and grant revocation.
- MCP schema rejects an attempted `consumption_coefficient` input before the
  handler/confirmation. Retired draft/apply or read scopes do not grant writes.
- OAuth DCR/PKCE, code/token handling, token reuse and revoked grants.
- Existing repository integration regression: archive composition, linked
  quantity scaling, template insertion/rollback, and position totals.

## Self-review corrections (2026-10-06)

See [REVIEW_2026_10_06.md](REVIEW_2026_10_06.md) for reproduced defects and fixes.
Five initially failing tests demonstrated receipt-FK deadlock, portal row wait,
source-currency drift, obsolete quote evidence and missed cache invalidation.
After correction, direct-write/linked-quantity/HTTP/OAuth tests pass; the two
concurrency/busy tests passed ten repetitions. Source-search version parity,
currency/delivery/consumption drift and quote date replacement also pass.
The additive database migration is unchanged by the review.

## Catalog creation (2026-10-07)

Full catalog review: [REVIEW_CATALOG_2026_10_07.md](REVIEW_CATALOG_2026_10_07.md).
Confirmed/fixed exact-price consent formatting and HTTP replay after a later
nomenclature edit. Negative checks cover inactive units, ambiguous historical
names, receipt actor isolation, global gate and current page-access removal.

- Engineer creates a new unit, work/material nomenclature and library cards,
  then inserts work and linked material into VOR using returned versions: PASS.
- Linked quantity is server-derived (10 × 2 × 1.2 = 24), including delivery in
  total; attempted manual linked quantity remains rejected: PASS.
- Exact catalog/card duplicates are reused without overwriting existing records;
  four simultaneous requests create one nomenclature record: PASS.
- Identical replay, changed-input key reuse rejection, disabled create gate,
  read-only scope denial and actual-role validation: PASS.
- Nomenclature version change between selection and card creation rolls back
  the receipt/card; price source and confirmed input are recorded: PASS.
- Real HTTP MCP SDK creates/reuses unit, nomenclature and card, validates nested
  typed results, retries and reads receipt as an engineer: PASS.
- OAuth consent/PKCE issues the explicit create-scopes for the engineer: PASS.
- New migration repeated successfully; `MCP_CATALOG_MIGRATION_OK`.
- Existing direct/linked/HTTP/OAuth tests pass with a 21-tool catalog.

## Commands (current)

```powershell
# TEST_DATABASE_URL must point to a disposable database with the fixture.
cd backend
go test -p 1 ./...
go vet ./...
go build -buildvcs=false ./cmd/server
go test -p 1 -tags=integration ./internal/services ./internal/mcpserver ./internal/mcpauth -run 'DirectPricing|AuthenticatedHTTP|OAuth' -count=1
go test -p 1 -tags=integration ./internal/repository -run 'ArchiveComposeIntegration|BoqPositionTotalsIntegration|TemplateInsertIntegration' -count=1
cd ..
npm run lint -- --max-warnings 0
npm run build
```

## Deployment verification remaining

No merge, production migration, restart or deployment was performed here.
Deploy backend and frontend together after review; apply the additive direct
migration first. Reconnect with `pricing:write`: old draft scopes cannot write
directly. Test the engineer client on staging with a work and at least two
linked materials using `VERIFY.md`, then verify the live portal has no draft
section and reads back the same quantities/totals as MCP. Historical database
records remain solely to preserve provenance and audit.
