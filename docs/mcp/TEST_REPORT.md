# MCP v2 direct VOR verification

Date: 2026-10-05, Europe/Moscow. Base: `baldmaxim/HubTender@0611f20bcfc9f842263323208ffa715bd98f8387`.
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
- Real HTTP MCP SDK round trip: 15 typed tools, confirmation, write/retry,
  committed receipt output validation, archive search and grant revocation.
- MCP schema rejects an attempted `consumption_coefficient` input before the
  handler/confirmation. Retired draft/apply or read scopes do not grant writes.
- OAuth DCR/PKCE, code/token handling, token reuse and revoked grants.
- Existing repository integration regression: archive composition, linked
  quantity scaling, template insertion/rollback, and position totals.

## Commands

```powershell
# TEST_DATABASE_URL must point to a disposable database with the fixture.
cd backend
go test -p 1 ./...
go vet ./...
go build -buildvcs=false ./cmd/server
go test -tags=integration ./internal/services ./internal/mcpserver ./internal/mcpauth -run 'DirectPricing|AuthenticatedHTTP|OAuth' -count=1
go test -tags=integration ./internal/repository -run 'ArchiveComposeIntegration|BoqPositionTotalsIntegration|TemplateInsertIntegration' -count=1
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
