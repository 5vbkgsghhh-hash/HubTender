# Direct VOR pricing installation

## Zero gate: rotate exposed secrets

The source archive used during analysis contained populated `.env` files.
Before enabling MCP, rotate at minimum:

- the Yandex PostgreSQL password from `DATABASE_URL`;
- `SENTRY_AUTH_TOKEN`;
- the RSA key behind `APP_JWT_PRIVATE_KEY_*` and its `kid`.

Do not copy any `.env` from that archive. Use the server's secret store and
`deploy/mcp.env.example` only as a list of variable names.

## 1. Review the direct-pricing change

This change starts from the already merged MCP implementation:

```text
repository: https://github.com/baldmaxim/HubTender
base commit: 0611f20bcfc9f842263323208ffa715bd98f8387
```

Review the branch diff before merging. Historical draft tables remain for audit. The draft UI, REST handlers,
service/repository lifecycle and MCP draft tools are removed.

## 2. Build before touching a database

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

## 3. Apply the additive migration on staging

Take a snapshot/backup first. The v1 MCP migration must already be installed.

```bash
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 \
  -f db/yandex/incremental/2026_10_tenderhub_direct_pricing.sql
```

Run `scripts/mcp/verify_direct_migration.sql` to verify the new table,
columns and unique direct-request index on staging. Do not
run this migration on production until direct-write integration UAT passes.

## 4. Configure staging

Copy variable names from `deploy/mcp.env.example` into the protected backend
env file. Start with:

```dotenv
MCP_ENABLED=true
MCP_WRITE_ENABLED=false
MCP_TEMPLATE_WRITE_ENABLED=false
MCP_DCR_ENABLED=true
```

`APP_BASE_URL` must be the public origin, without `/api`. The MCP access token
has audience `hubtender-mcp`; a normal portal JWT cannot call `/mcp`.

Install the nginx locations from `deploy/nginx-mcp.conf.example`, validate with
`nginx -t`, then reload nginx.

## 5. Read-only pilot

Connect the MCP client to:

```text
https://<host>/mcp
```

The client performs DCR/CIMD discovery and opens `/agent-connect` in a browser.
Sign in with the engineer's own TenderHUB account and approve scopes. Confirm:

- `tenderhub_whoami` returns the engineer and client-specific scopes;
- archive/library/template reads work;
- `tenderhub_list_boq_items` and `tenderhub_get_boq_item` read VOR rows;
- the catalog has no draft tools;
- `tenderhub_price_boq_item` refuses writes while
  `MCP_WRITE_ENABLED=false`.

## 6. Direct-write gate

After read-only acceptance:

1. Set `MCP_WRITE_ENABLED=true`, restart BFF, keep template writes false.
2. Reauthorize the MCP client with `pricing:write`. The old
   `pricing:draft` scope does not grant direct-write access.
3. Pilot with two engineers and non-production tenders. Search a source,
   inspect the BOQ row/ETag and tender revision, then confirm the single
   direct write. Re-read VOR, audit, provenance, and QA.
4. Retry the identical request key and verify no second row/revision bump;
   test stale revision/ETag and an invalid source rate.
5. Enable `MCP_TEMPLATE_WRITE_ENABLED=true` only if leading-engineer template
   UAT passes. Ordinary engineers remain blocked server-side.
