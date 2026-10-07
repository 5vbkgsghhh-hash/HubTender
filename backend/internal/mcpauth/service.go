package mcpauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/su10/hubtender/backend/internal/access"
	"github.com/su10/hubtender/backend/internal/auth"
	"github.com/su10/hubtender/backend/internal/domain/user"
)

var (
	ErrInvalidClient      = errors.New("invalid_client")
	ErrInvalidGrant       = errors.New("invalid_grant")
	ErrInvalidRequest     = errors.New("invalid_request")
	ErrInvalidScope       = errors.New("invalid_scope")
	ErrAccessDenied       = errors.New("access_denied")
	ErrRegistrationClosed = errors.New("dynamic_client_registration_disabled")
)

type userReader interface {
	GetByID(context.Context, string) (*user.User, error)
}

type ServiceConfig struct {
	Issuer           *auth.Issuer
	CodeTTL          time.Duration
	DCR              bool
	CIMDAllowedHosts []string
}

type Service struct {
	repo  *Repository
	users userReader
	cfg   ServiceConfig
}

func NewService(repo *Repository, users userReader, cfg ServiceConfig) *Service {
	return &Service{repo: repo, users: users, cfg: cfg}
}

func (s *Service) ResolveClient(ctx context.Context, clientID string) (*Client, error) {
	c, err := s.repo.FindClient(ctx, clientID)
	if err == nil {
		if c.Disabled {
			return nil, ErrInvalidClient
		}
		return c, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if strings.HasPrefix(clientID, "https://") {
		c, err := resolveCIMD(ctx, clientID, s.cfg.CIMDAllowedHosts)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidClient, err)
		}
		return c, nil
	}
	return nil, ErrInvalidClient
}

func (s *Service) RegisterClient(ctx context.Context, name string, redirects, scopes []string, appType string) (*Client, error) {
	if !s.cfg.DCR {
		return nil, ErrRegistrationClosed
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 || len(redirects) == 0 || len(redirects) > 10 {
		return nil, ErrInvalidRequest
	}
	if appType == "" {
		appType = "native"
	}
	if appType != "native" && appType != "web" {
		return nil, ErrInvalidRequest
	}
	for _, raw := range redirects {
		if err := validateRedirectURI(raw); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
		}
	}
	if len(scopes) == 0 {
		scopes = slices.Clone(CurrentScopes)
	}
	cleanScopes, err := validateKnownScopes(scopes)
	if err != nil {
		return nil, err
	}
	id, err := randomToken(24)
	if err != nil {
		return nil, err
	}
	c := &Client{
		ID:               "mcp_" + id,
		Name:             name,
		RedirectURIs:     slices.Clone(redirects),
		AllowedScopes:    cleanScopes,
		ApplicationType:  appType,
		RegistrationKind: "dynamic",
	}
	if err := s.repo.RegisterClient(ctx, *c); err != nil {
		return nil, err
	}
	return c, nil
}

type AuthorizationRequest struct {
	ClientID      string
	RedirectURI   string
	Scope         string
	State         string
	CodeChallenge string
}

func (s *Service) ValidateAuthorizationRequest(ctx context.Context, in AuthorizationRequest) (*Client, []string, error) {
	if in.State == "" || in.CodeChallenge == "" || len(in.CodeChallenge) < 43 || len(in.CodeChallenge) > 128 {
		return nil, nil, ErrInvalidRequest
	}
	c, err := s.ResolveClient(ctx, in.ClientID)
	if err != nil {
		return nil, nil, err
	}
	if !slices.Contains(c.RedirectURIs, in.RedirectURI) {
		return nil, nil, ErrInvalidClient
	}
	requested, err := validateKnownScopes(strings.Fields(in.Scope))
	if err != nil {
		return nil, nil, err
	}
	for _, scope := range requested {
		if !slices.Contains(c.AllowedScopes, scope) {
			return nil, nil, ErrInvalidScope
		}
	}
	return c, requested, nil
}

func (s *Service) BeginAuthorization(ctx context.Context, userID string, in AuthorizationRequest) (string, error) {
	_, requested, err := s.ValidateAuthorizationRequest(ctx, in)
	if err != nil {
		return "", err
	}
	u, err := s.currentUser(ctx, userID)
	if err != nil {
		return "", err
	}
	allowed := allowedScopes(u)
	for _, scope := range requested {
		if !slices.Contains(allowed, scope) {
			return "", ErrAccessDenied
		}
	}
	rawCode, err := randomToken(32)
	if err != nil {
		return "", err
	}
	if err := s.repo.StoreAuthorizationCode(ctx, AuthorizationCode{
		CodeHash:      tokenHash(rawCode),
		ClientID:      in.ClientID,
		UserID:        userID,
		RedirectURI:   in.RedirectURI,
		Scopes:        requested,
		CodeChallenge: in.CodeChallenge,
		ExpiresAt:     time.Now().UTC().Add(s.cfg.CodeTTL),
	}); err != nil {
		return "", err
	}
	redirect, _ := url.Parse(in.RedirectURI)
	q := redirect.Query()
	q.Set("code", rawCode)
	q.Set("state", in.State)
	redirect.RawQuery = q.Encode()
	return redirect.String(), nil
}

func (s *Service) ExchangeCode(ctx context.Context, clientID, redirectURI, code, verifier, userAgent, ip string) (*TokenResponse, error) {
	if clientID == "" || code == "" || verifier == "" || redirectURI == "" {
		return nil, ErrInvalidRequest
	}
	record, err := s.repo.ConsumeAuthorizationCode(ctx, tokenHash(code))
	if err != nil {
		return nil, ErrInvalidGrant
	}
	if record.ClientID != clientID || record.RedirectURI != redirectURI || !verifyPKCE(verifier, record.CodeChallenge) {
		return nil, ErrInvalidGrant
	}
	u, err := s.currentUser(ctx, record.UserID)
	if err != nil {
		return nil, ErrAccessDenied
	}
	return s.issueInitialPair(ctx, u, clientID, record.Scopes, userAgent, ip)
}

func (s *Service) issueInitialPair(ctx context.Context, u *user.User, clientID string, scopes []string, userAgent, ip string) (*TokenResponse, error) {
	allowed := allowedScopes(u)
	for _, scope := range scopes {
		if !slices.Contains(allowed, scope) {
			return nil, ErrAccessDenied
		}
	}
	accessToken, err := s.cfg.Issuer.IssueDelegatedAccessToken(u.ID, u.Email, u.RoleCode, strings.Join(scopes, " "), clientID)
	if err != nil {
		return nil, err
	}
	refresh, err := s.cfg.Issuer.IssueRefreshToken()
	if err != nil {
		return nil, err
	}
	familyID, err := randomUUID()
	if err != nil {
		return nil, err
	}
	if err := s.repo.InsertRefreshToken(ctx, auth.HashRefreshToken(refresh.Token), familyID, clientID, u.ID, scopes, refresh.ExpiresAt, userAgent, ip); err != nil {
		return nil, err
	}
	if err := s.repo.UpsertGrant(ctx, u.ID, clientID, scopes); err != nil {
		return nil, err
	}
	return &TokenResponse{AccessToken: accessToken.Token, TokenType: "Bearer", ExpiresIn: int(s.cfg.Issuer.AccessTTL().Seconds()), RefreshToken: refresh.Token, Scope: strings.Join(scopes, " ")}, nil
}

func (s *Service) RefreshWithRecord(ctx context.Context, record *RefreshToken, u *user.User, userAgent, ip string) (*TokenResponse, error) {
	accessToken, err := s.cfg.Issuer.IssueDelegatedAccessToken(u.ID, u.Email, u.RoleCode, strings.Join(record.Scopes, " "), record.ClientID)
	if err != nil {
		return nil, err
	}
	refresh, err := s.cfg.Issuer.IssueRefreshToken()
	if err != nil {
		return nil, err
	}
	if err := s.repo.RotateRefreshToken(ctx, record, auth.HashRefreshToken(refresh.Token), refresh.ExpiresAt, userAgent, ip); err != nil {
		_ = s.repo.RevokeFamily(ctx, record.TokenFamilyID)
		return nil, ErrInvalidGrant
	}
	_ = s.repo.TouchGrant(ctx, u.ID, record.ClientID)
	return &TokenResponse{AccessToken: accessToken.Token, TokenType: "Bearer", ExpiresIn: int(s.cfg.Issuer.AccessTTL().Seconds()), RefreshToken: refresh.Token, Scope: strings.Join(record.Scopes, " ")}, nil
}

func (s *Service) RefreshToken(ctx context.Context, clientID, raw, userAgent, ip string) (*TokenResponse, error) {
	record, err := s.repo.FindRefreshToken(ctx, auth.HashRefreshToken(raw))
	if err != nil || record.ClientID != clientID || record.RevokedAt != nil || time.Now().UTC().After(record.ExpiresAt) {
		if err == nil && record != nil && record.RevokedAt != nil {
			_ = s.repo.RevokeFamily(ctx, record.TokenFamilyID)
		}
		return nil, ErrInvalidGrant
	}
	u, err := s.currentUser(ctx, record.UserID)
	if err != nil {
		return nil, ErrAccessDenied
	}
	allowed := allowedScopes(u)
	for _, scope := range record.Scopes {
		if !slices.Contains(allowed, scope) {
			return nil, ErrAccessDenied
		}
	}
	return s.RefreshWithRecord(ctx, record, u, userAgent, ip)
}

func (s *Service) RevokeToken(ctx context.Context, raw string) error {
	if raw == "" {
		return nil
	}
	return s.repo.RevokeToken(ctx, auth.HashRefreshToken(raw))
}

func (s *Service) ListGrants(ctx context.Context, userID string) ([]Grant, error) {
	if _, err := s.currentUser(ctx, userID); err != nil {
		return nil, err
	}
	return s.repo.ListGrants(ctx, userID)
}

func (s *Service) RevokeGrant(ctx context.Context, userID, clientID string) error {
	if _, err := s.currentUser(ctx, userID); err != nil {
		return err
	}
	return s.repo.RevokeGrant(ctx, userID, clientID)
}

func (s *Service) IsPrincipalActive(ctx context.Context, userID, clientID string) bool {
	if clientID == "" {
		return false
	}
	if _, err := s.currentUser(ctx, userID); err != nil {
		return false
	}
	active, err := s.repo.IsGrantActive(ctx, userID, clientID)
	return err == nil && active
}

func (s *Service) currentUser(ctx context.Context, id string) (*user.User, error) {
	u, err := s.users.GetByID(ctx, id)
	if err != nil || u == nil || !u.AccessEnabled || !strings.EqualFold(u.AccessStatus, "approved") {
		return nil, ErrAccessDenied
	}
	return u, nil
}

func allowedScopes(u *user.User) []string {
	out := []string{ScopeTendersRead, ScopeArchiveRead, ScopeLibraryRead, ScopePricingWrite}
	if !access.HasAccess(u.AllowedPages, "/positions") {
		out = []string{ScopeTendersRead, ScopeArchiveRead, ScopeLibraryRead}
	}
	if slices.Contains([]string{"engineer", "veduschiy_inzhener", "administrator", "developer"}, u.RoleCode) && access.HasAccess(u.AllowedPages, "/library") {
		out = append(out, ScopeNomenclatureCreate, ScopeLibraryCreate)
	}
	if slices.Contains([]string{"veduschiy_inzhener", "administrator", "developer"}, u.RoleCode) && access.HasAccess(u.AllowedPages, "/library/templates") {
		out = append(out, ScopeTemplatesWrite)
	}
	return out
}

func validateKnownScopes(scopes []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" || seen[scope] {
			continue
		}
		if !slices.Contains(AllScopes, scope) {
			return nil, ErrInvalidScope
		}
		seen[scope] = true
		out = append(out, scope)
	}
	if len(out) == 0 {
		return nil, ErrInvalidScope
	}
	return out, nil
}

func validateRedirectURI(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.User != nil || u.Fragment != "" {
		return errors.New("redirect_uri must be an absolute URI without userinfo or fragment")
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" {
		host := u.Hostname()
		if host == "localhost" || host == "127.0.0.1" || host == "::1" || (net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()) {
			return nil
		}
	}
	return errors.New("redirect_uri must use HTTPS, except for loopback native clients")
}

func verifyPKCE(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:]) == challenge
}

func tokenHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func randomUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
