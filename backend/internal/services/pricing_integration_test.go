//go:build integration

package services

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/su10/hubtender/backend/internal/mcpauth"
)

const (
	evalUser         = "eeeeeeee-9000-0000-0000-000000000001"
	evalClient       = "mcp-evaluation-client"
	targetTender     = "eeeeeeee-3000-0000-0000-000000000001"
	targetPosition   = "eeeeeeee-4000-0000-0000-000000000001"
	templatePosition = "eeeeeeee-4000-0000-0000-000000000099"
	fxTender         = "eeeeeeee-3000-0000-0000-000000000098"
	fxPosition       = "eeeeeeee-4000-0000-0000-000000000098"
	sourceMaterial   = "eeeeeeee-5000-0000-0000-000000000001"
	sourceWork       = "eeeeeeee-5000-0000-0000-000000000002"
	furnitureWork    = "eeeeeeee-5000-0000-0000-000000000003"
)

func seedPricingActor(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	scopes := []string{mcpauth.ScopeTendersRead, mcpauth.ScopeArchiveRead, mcpauth.ScopeLibraryRead, mcpauth.ScopePricingWrite}
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO public.roles(code,name,allowed_pages) VALUES ('engineer','Инженер','["/positions","/library","/library/templates"]') ON CONFLICT (code) DO NOTHING`, nil},
		{`INSERT INTO auth.users(id,email) VALUES ($1,'mcp-eval@example.com') ON CONFLICT (id) DO NOTHING`, []any{evalUser}},
		{`INSERT INTO public.users(id,full_name,email,access_status,access_enabled,role_code,allowed_pages) VALUES ($1,'MCP Eval','mcp-eval@example.com','approved',true,'engineer','["/positions","/library","/library/templates"]') ON CONFLICT (id) DO UPDATE SET access_status='approved',access_enabled=true`, []any{evalUser}},
		{`INSERT INTO app_auth.oauth_clients(client_id,client_name,redirect_uris,allowed_scopes) VALUES ($1,'MCP Evaluation','["http://127.0.0.1/callback"]',$2) ON CONFLICT (client_id) DO NOTHING`, []any{evalClient, scopes}},
		{`INSERT INTO app_auth.oauth_grants(user_id,client_id,scopes) VALUES ($1,$2,$3) ON CONFLICT (user_id,client_id) DO UPDATE SET revoked_at=NULL,scopes=EXCLUDED.scopes`, []any{evalUser, evalClient, scopes}},
		{`DELETE FROM public.mcp_direct_pricing_requests WHERE actor_id=$1`, []any{evalUser}},
		{`DELETE FROM public.mcp_catalog_creation_requests WHERE actor_id=$1`, []any{evalUser}},
		{`DELETE FROM public.boq_items WHERE client_position_id IN ($1,$2)`, []any{targetPosition, templatePosition}},
		{`DELETE FROM public.boq_items_audit WHERE changed_by=$1`, []any{evalUser}},
		{`DELETE FROM public.pricing_drafts WHERE tender_id=$1`, []any{targetTender}},
		{`DELETE FROM public.pricing_drafts WHERE tender_id=$1`, []any{fxTender}},
		{`DELETE FROM public.client_positions WHERE id=$1`, []any{templatePosition}},
		{`DELETE FROM public.client_positions WHERE id=$1`, []any{fxPosition}},
		{`DELETE FROM public.tenders WHERE id=$1`, []any{fxTender}},
		{`UPDATE public.tenders SET financial_input_revision=0,financial_calculation_revision=0,updated_at='2026-09-01' WHERE id=$1`, []any{targetTender}},
	}
	for _, st := range statements {
		if _, err := pool.Exec(ctx, st.sql, st.args...); err != nil {
			t.Fatal(err)
		}
	}
}

func cleanupPricingActor(ctx context.Context, pool *pgxpool.Pool) {
	statements := []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM public.mcp_direct_pricing_requests WHERE actor_id=$1`, []any{evalUser}},
		{`DELETE FROM public.mcp_catalog_creation_requests WHERE actor_id=$1`, []any{evalUser}},
		{`DELETE FROM public.boq_items WHERE client_position_id IN ($1,$2)`, []any{targetPosition, templatePosition}},
		{`DELETE FROM public.boq_items_audit WHERE changed_by=$1`, []any{evalUser}},
		{`DELETE FROM public.pricing_drafts WHERE tender_id=$1`, []any{targetTender}},
		{`DELETE FROM public.pricing_drafts WHERE tender_id=$1`, []any{fxTender}},
		{`DELETE FROM public.client_positions WHERE id=$1`, []any{templatePosition}},
		{`DELETE FROM public.client_positions WHERE id=$1`, []any{fxPosition}},
		{`DELETE FROM public.tenders WHERE id=$1`, []any{fxTender}},
		{`DELETE FROM app_auth.oauth_grants WHERE user_id=$1`, []any{evalUser}},
		{`DELETE FROM app_auth.oauth_clients WHERE client_id=$1`, []any{evalClient}},
		{`DELETE FROM public.users WHERE id=$1`, []any{evalUser}},
		{`DELETE FROM auth.users WHERE id=$1`, []any{evalUser}},
	}
	for _, st := range statements {
		_, _ = pool.Exec(ctx, st.sql, st.args...)
	}
}
