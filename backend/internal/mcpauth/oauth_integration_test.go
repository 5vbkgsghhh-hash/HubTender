//go:build integration

package mcpauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/su10/hubtender/backend/internal/auth"
	"github.com/su10/hubtender/backend/internal/middleware"
	"github.com/su10/hubtender/backend/internal/repository"
)

const oauthEvalUser = "eeeeeeee-9000-0000-0000-000000000002"

func TestOAuthPKCERotationAndImmediateGrantRevoke(t *testing.T) {
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
	seedOAuthUser(t, ctx, pool)
	defer cleanupOAuthUser(ctx, pool)
	issuer, key := testMCPIssuer(t)
	repo := NewRepository(pool)
	svc := NewService(repo, repository.NewUserRepo(pool), ServiceConfig{Issuer: issuer, CodeTTL: 5 * time.Minute, DCR: true})
	client, err := svc.RegisterClient(ctx, "Integration Client", []string{"http://127.0.0.1:43210/callback"}, []string{ScopeTendersRead, ScopePricingWrite, ScopeNomenclatureCreate, ScopeLibraryCreate}, "native")
	if err != nil {
		t.Fatal(err)
	}
	verifier := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	redirect, err := svc.BeginAuthorization(ctx, oauthEvalUser, AuthorizationRequest{ClientID: client.ID, RedirectURI: client.RedirectURIs[0], Scope: ScopeTendersRead + " " + ScopePricingWrite + " " + ScopeNomenclatureCreate + " " + ScopeLibraryCreate, State: "state-1", CodeChallenge: challenge})
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(redirect)
	if err != nil {
		t.Fatal(err)
	}
	code := u.Query().Get("code")
	if code == "" || u.Query().Get("state") != "state-1" {
		t.Fatalf("bad redirect: %s", redirect)
	}
	pair, err := svc.ExchangeCode(ctx, client.ID, client.RedirectURIs[0], code, verifier, "integration-test", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	principal, err := middleware.VerifyToken(middleware.VerifyConfig{AppPublicKey: &key.Private.PublicKey, AppIssuer: "https://issuer.test", AppAudience: "hubtender-mcp"}, pair.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if principal.ClientID != client.ID || !principal.HasScope(ScopePricingWrite) || !principal.HasScope(ScopeNomenclatureCreate) || !principal.HasScope(ScopeLibraryCreate) {
		t.Fatalf("bad principal: %+v", principal)
	}
	if !svc.IsPrincipalActive(ctx, oauthEvalUser, client.ID) {
		t.Fatal("new grant is not active")
	}
	rotated, err := svc.RefreshToken(ctx, client.ID, pair.RefreshToken, "integration-test", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if rotated.RefreshToken == pair.RefreshToken {
		t.Fatal("refresh token did not rotate")
	}
	if _, err := svc.RefreshToken(ctx, client.ID, pair.RefreshToken, "integration-test", "127.0.0.1"); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("refresh reuse should fail: %v", err)
	}
	if _, err := svc.RefreshToken(ctx, client.ID, rotated.RefreshToken, "integration-test", "127.0.0.1"); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("family should be revoked after reuse: %v", err)
	}
	if err := svc.RevokeGrant(ctx, oauthEvalUser, client.ID); err != nil {
		t.Fatal(err)
	}
	if svc.IsPrincipalActive(ctx, oauthEvalUser, client.ID) {
		t.Fatal("revoked grant remained active")
	}
}

func seedOAuthUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO public.roles(code,name,allowed_pages) VALUES ('engineer','Инженер','["/positions","/library"]') ON CONFLICT (code) DO NOTHING`, nil},
		{`INSERT INTO auth.users(id,email) VALUES ($1,'oauth-eval@example.com') ON CONFLICT (id) DO NOTHING`, []any{oauthEvalUser}},
		{`INSERT INTO public.users(id,full_name,email,access_status,access_enabled,role_code,allowed_pages) VALUES ($1,'OAuth Eval','oauth-eval@example.com','approved',true,'engineer','["/positions","/library"]') ON CONFLICT (id) DO UPDATE SET access_status='approved',access_enabled=true`, []any{oauthEvalUser}},
	}
	for _, st := range statements {
		if _, err := pool.Exec(ctx, st.sql, st.args...); err != nil {
			t.Fatal(err)
		}
	}
}

func cleanupOAuthUser(ctx context.Context, pool *pgxpool.Pool) {
	for _, st := range []struct {
		sql  string
		args []any
	}{{`DELETE FROM app_auth.oauth_grants WHERE user_id=$1`, []any{oauthEvalUser}}, {`DELETE FROM app_auth.oauth_refresh_tokens WHERE user_id=$1`, []any{oauthEvalUser}}, {`DELETE FROM app_auth.oauth_authorization_codes WHERE user_id=$1`, []any{oauthEvalUser}}, {`DELETE FROM app_auth.oauth_clients WHERE client_name='Integration Client'`, nil}, {`DELETE FROM public.users WHERE id=$1`, []any{oauthEvalUser}}, {`DELETE FROM auth.users WHERE id=$1`, []any{oauthEvalUser}}} {
		_, _ = pool.Exec(ctx, st.sql, st.args...)
	}
}

func testMCPIssuer(t *testing.T) (*auth.Issuer, *auth.SigningKey) {
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
	return issuer, key
}
