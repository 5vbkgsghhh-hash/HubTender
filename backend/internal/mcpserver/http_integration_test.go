//go:build integration

package mcpserver

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog"
	"github.com/su10/hubtender/backend/internal/auth"
	"github.com/su10/hubtender/backend/internal/mcpauth"
	"github.com/su10/hubtender/backend/internal/middleware"
	"github.com/su10/hubtender/backend/internal/pricing"
	"github.com/su10/hubtender/backend/internal/repository"
	"github.com/su10/hubtender/backend/internal/services"
)

const httpEvalUser = "eeeeeeee-9000-0000-0000-000000000003"

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (b bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(clone)
}

func TestAuthenticatedHTTPToolCatalogSearchAndGrantRevoke(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	key, issuer := mcpHTTPTestIssuer(t)
	oauthRepo := mcpauth.NewRepository(pool)
	userRepo := repository.NewUserRepo(pool)
	libraryRepo := repository.NewLibraryRepo(pool)
	pricingRepo := repository.NewPricingRepo(pool)
	clientID := "mcp-http-evaluation"
	scopes := []string{mcpauth.ScopeTendersRead, mcpauth.ScopeArchiveRead, mcpauth.ScopeLibraryRead, mcpauth.ScopePricingWrite, mcpauth.ScopeNomenclatureCreate, mcpauth.ScopeLibraryCreate}
	seedHTTPActor(t, ctx, pool, clientID, scopes)
	defer cleanupHTTPActor(ctx, pool, clientID)
	const directTender = "eeeeeeee-3000-0000-0000-000000000010"
	const directPosition = "eeeeeeee-4000-0000-0000-000000000010"
	if _, err := pool.Exec(ctx, `INSERT INTO public.tenders
		(id,title,client_name,tender_number,version,is_archived,usd_rate,eur_rate,created_at,updated_at)
		VALUES ($1,'MCP HTTP direct test','Eval','EVAL-HTTP-DIRECT',1,false,90,100,now(),now())
		ON CONFLICT (id) DO NOTHING`, directTender); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO public.client_positions
		(id,tender_id,position_number,unit_code,volume,work_name,hierarchy_level)
		VALUES ($1,$2,1,'шт',2,'Мобильный пресс-компактор',0)
		ON CONFLICT (id) DO NOTHING`, directPosition, directTender); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM public.mcp_catalog_creation_requests WHERE actor_id=$1`, httpEvalUser)
		_, _ = pool.Exec(ctx, `DELETE FROM public.works_library WHERE work_name_id IN (SELECT id FROM public.work_names WHERE name LIKE 'MCP HTTP CATALOG %')`)
		_, _ = pool.Exec(ctx, `DELETE FROM public.work_names WHERE name LIKE 'MCP HTTP CATALOG %'`)
		_, _ = pool.Exec(ctx, `DELETE FROM public.mcp_direct_pricing_requests WHERE actor_id=$1`, httpEvalUser)
		_, _ = pool.Exec(ctx, `DELETE FROM public.boq_items WHERE tender_id=$1`, directTender)
		_, _ = pool.Exec(ctx, `DELETE FROM public.boq_items_audit WHERE changed_by=$1`, httpEvalUser)
		_, _ = pool.Exec(ctx, `DELETE FROM public.client_positions WHERE id=$1`, directPosition)
		_, _ = pool.Exec(ctx, `DELETE FROM public.tenders WHERE id=$1`, directTender)
	}()
	token, err := issuer.IssueDelegatedAccessToken(httpEvalUser, "mcp-http@example.com", "engineer", joinScopes(scopes), clientID)
	if err != nil {
		t.Fatal(err)
	}
	pricingSvc := services.NewPricingService(pricingRepo, userRepo, libraryRepo, oauthRepo, services.PricingFeatures{WriteEnabled: true, CatalogWriteEnabled: true})
	oauthSvc := mcpauth.NewService(oauthRepo, userRepo, mcpauth.ServiceConfig{Issuer: issuer, CodeTTL: 5 * time.Minute, DCR: true})
	oauthHandler := mcpauth.NewHandler(oauthSvc, mcpauth.HandlerConfig{PublicBaseURL: "https://issuer.test", DCR: true})
	mcpHandler := NewHTTPHandler(pricingSvc, Config{MaxRequestBodyBytes: 1 << 20, Logger: zerolog.Nop()})
	verify := middleware.VerifyConfig{AppPublicKey: &key.Private.PublicKey, AppIssuer: "https://issuer.test", AppAudience: "hubtender-mcp"}
	server := httptest.NewServer(middleware.JWTAuthWithChallenge(verify, "https://issuer.test/.well-known/oauth-protected-resource")(oauthHandler.RequireActiveGrant(mcpHandler)))
	defer server.Close()
	httpClient := &http.Client{Transport: bearerTransport{token: token.Token, base: http.DefaultTransport}}
	confirmations := 0
	confirmationMessages := []string{}
	client := mcp.NewClient(&mcp.Implementation{Name: "integration-client", Version: "1.0.0"}, &mcp.ClientOptions{
		ElicitationHandler: func(_ context.Context, request *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			confirmations++
			confirmationMessages = append(confirmationMessages, request.Params.Message)
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}, nil
		},
	})
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: httpClient, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 21 {
		t.Fatalf("tool count=%d", len(listed.Tools))
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "tenderhub_search_archive_prices", Arguments: map[string]any{"query": "Мобильный пресс-компактор", "kind": "material", "unit_code": "шт", "limit": 5}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("tool error: %+v", result.Content)
	}
	selected, err := pricingRepo.GetArchiveItem(ctx, "eeeeeeee-5000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	directArgs := map[string]any{
		"tender_id": directTender, "target_position_id": directPosition,
		"source_kind": "archive", "source_id": "eeeeeeee-5000-0000-0000-000000000001",
		"expected_source_rate": 2070000, "quantity": 2,
		"expected_source_version": selected.SourceVersion,
		"detail_cost_category_id": "eeeeeeee-0000-0000-0000-000000000002",
		"expected_revision":       0, "request_key": "http-direct-price-create-001",
	}
	for attempt := 0; attempt < 2; attempt++ {
		written, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "tenderhub_price_boq_item", Arguments: directArgs})
		if err != nil || written.IsError {
			t.Fatalf("direct write attempt %d: result=%+v err=%v", attempt, written, err)
		}
	}
	if confirmations != 2 {
		t.Fatalf("confirmations=%d, want 2", confirmations)
	}
	var count, revision, receipts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.boq_items WHERE tender_id=$1`, directTender).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT financial_input_revision FROM public.tenders WHERE id=$1`, directTender).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.mcp_direct_pricing_requests WHERE actor_id=$1`, httpEvalUser).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if count != 1 || revision != 1 || receipts != 1 {
		t.Fatalf("direct HTTP write/retry: items=%d revision=%d receipts=%d", count, revision, receipts)
	}
	receipt, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "tenderhub_get_direct_pricing_receipt",
		Arguments: map[string]any{"request_key": "http-direct-price-create-001"},
	})
	if err != nil || receipt.IsError {
		t.Fatalf("read committed receipt: result=%+v err=%v", receipt, err)
	}
	callCatalog := func(name string, args map[string]any) pricing.CatalogCreationResult {
		t.Helper()
		r, e := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if e != nil || r.IsError {
			t.Fatalf("catalog HTTP tool %s: result=%+v err=%v", name, r, e)
		}
		raw, e := json.Marshal(r.StructuredContent)
		if e != nil {
			t.Fatal(e)
		}
		var created pricing.CatalogCreationResult
		if e := json.Unmarshal(raw, &created); e != nil {
			t.Fatal(e)
		}
		return created
	}
	unit := callCatalog("tenderhub_create_unit", map[string]any{"code": "шт", "name": "Штука", "request_key": "http-catalog-unit-001"})
	if unit.Created || unit.EntityID != "шт" {
		t.Fatalf("existing unit was not reused: %+v", unit)
	}
	name := callCatalog("tenderhub_create_nomenclature_item", map[string]any{"kind": "work", "name": "MCP HTTP CATALOG work", "unit_code": "шт", "request_key": "http-catalog-name-001"})
	if name.NomenclatureItem == nil {
		t.Fatal("typed nomenclature result missing")
	}
	cardArgs := map[string]any{"kind": "work", "name_id": name.EntityID, "expected_name_version": name.NomenclatureItem.Version, "unit_rate": 12345.67, "currency": "RUB", "price_source": "Цена указана пользователем в HTTP тесте", "request_key": "http-catalog-card-001"}
	card := callCatalog("tenderhub_create_library_item", cardArgs)
	if card.LibraryItem == nil || card.LibraryItem.SourceVersion == "" {
		t.Fatal("typed library result missing version")
	}
	if !strings.Contains(confirmationMessages[len(confirmationMessages)-1], "12345.67 RUB") {
		t.Error("confirmation rounded the user-supplied price")
	}
	if _, err := pool.Exec(ctx, `UPDATE public.work_names SET name='MCP HTTP CATALOG renamed' WHERE id=$1`, name.EntityID); err != nil {
		t.Fatal(err)
	}
	replayedCard := callCatalog("tenderhub_create_library_item", cardArgs)
	if !replayedCard.Replayed || replayedCard.EntityID != card.EntityID {
		t.Fatal("HTTP catalog retry created a duplicate")
	}
	_, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "tenderhub_get_catalog_creation_receipt", Arguments: map[string]any{"request_key": "http-catalog-card-001"}})
	if e != nil {
		t.Fatal(e)
	}
	if err := oauthRepo.RevokeGrant(ctx, httpEvalUser, clientID); err != nil {
		t.Fatal(err)
	}
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "tenderhub_whoami", Arguments: map[string]any{}}); err == nil {
		t.Fatal("revoked grant still called MCP")
	}
}

func seedHTTPActor(t *testing.T, ctx context.Context, pool *pgxpool.Pool, clientID string, scopes []string) {
	t.Helper()
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO public.roles(code,name,allowed_pages) VALUES ('engineer','Инженер','["/positions","/library","/library/templates"]') ON CONFLICT (code) DO NOTHING`, nil},
		{`INSERT INTO auth.users(id,email) VALUES ($1,'mcp-http@example.com') ON CONFLICT (id) DO NOTHING`, []any{httpEvalUser}},
		{`INSERT INTO public.users(id,full_name,email,access_status,access_enabled,role_code,allowed_pages) VALUES ($1,'MCP HTTP','mcp-http@example.com','approved',true,'engineer','["/positions","/library","/library/templates"]') ON CONFLICT (id) DO UPDATE SET access_status='approved',access_enabled=true`, []any{httpEvalUser}},
		{`INSERT INTO app_auth.oauth_clients(client_id,client_name,redirect_uris,allowed_scopes) VALUES ($1,'MCP HTTP Evaluation','["http://127.0.0.1/callback"]',$2) ON CONFLICT (client_id) DO NOTHING`, []any{clientID, scopes}},
		{`INSERT INTO app_auth.oauth_grants(user_id,client_id,scopes) VALUES ($1,$2,$3) ON CONFLICT (user_id,client_id) DO UPDATE SET revoked_at=NULL,scopes=EXCLUDED.scopes`, []any{httpEvalUser, clientID, scopes}},
	}
	for _, st := range statements {
		if _, err := pool.Exec(ctx, st.sql, st.args...); err != nil {
			t.Fatal(err)
		}
	}
}
func cleanupHTTPActor(ctx context.Context, pool *pgxpool.Pool, clientID string) {
	for _, st := range []struct {
		sql  string
		args []any
	}{{`DELETE FROM app_auth.oauth_grants WHERE user_id=$1`, []any{httpEvalUser}}, {`DELETE FROM app_auth.oauth_clients WHERE client_id=$1`, []any{clientID}}, {`DELETE FROM public.users WHERE id=$1`, []any{httpEvalUser}}, {`DELETE FROM auth.users WHERE id=$1`, []any{httpEvalUser}}} {
		_, _ = pool.Exec(ctx, st.sql, st.args...)
	}
}
func mcpHTTPTestIssuer(t *testing.T) (*auth.SigningKey, *auth.Issuer) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	key, err := auth.LoadSigningKey(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := auth.NewIssuer(auth.IssuerConfig{SigningKey: key, Issuer: "https://issuer.test", Audience: "hubtender-mcp", AccessTTL: 10 * time.Minute, RefreshTTL: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	return key, issuer
}
func joinScopes(scopes []string) string {
	out := ""
	for i, s := range scopes {
		if i > 0 {
			out += " "
		}
		out += s
	}
	return out
}
