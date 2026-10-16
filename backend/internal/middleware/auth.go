package middleware

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog/log"
	"github.com/su10/hubtender/backend/internal/actorctx"
	"github.com/su10/hubtender/backend/pkg/apierr"
)

// jwtLeeway returns the clock-skew tolerance for JWT exp/iat checks. Read from
// JWT_CLOCK_SKEW_SECONDS env var; defaults to 0 (strict). Use only in dev when
// the local machine clock cannot be corrected.
func jwtLeeway() time.Duration {
	if v := os.Getenv("JWT_CLOCK_SKEW_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 0
}

type ctxKey string

const CtxUser ctxKey = "user"

// AuthUser holds the verified claims extracted from the app-issued JWT.
type AuthUser struct {
	ID       string
	Email    string
	Role     string // public.users.role_code, e.g. "administrator"
	Issuer   string
	Scopes   []string // OAuth scopes; empty for ordinary portal JWTs
	ClientID string   // OAuth client_id; empty for ordinary portal JWTs
}

// appClaims captures the shape of an access token minted by the local
// auth.Issuer. Fields mirror auth.AccessClaims — kept in lockstep at compile
// time by issuer_test.go's round-trip assertions.
type appClaims struct {
	jwt.RegisteredClaims
	Email    string `json:"email"`
	Role     string `json:"role"`
	Scope    string `json:"scope,omitempty"`
	ClientID string `json:"client_id,omitempty"`
}

// VerifyConfig bundles the knobs needed to verify app-issued tokens.
// Construct it once in main.go and reuse across requests.
type VerifyConfig struct {
	AppPublicKey *rsa.PublicKey
	AppIssuer    string
	AppAudience  string // optional; empty means "do not check aud"
}

// VerifyToken parses and validates a raw JWT against the configured app
// issuer. Returns the extracted AuthUser on success.
func VerifyToken(cfg VerifyConfig, raw string) (*AuthUser, error) {
	if cfg.AppPublicKey == nil {
		return nil, errors.New("app public key not configured")
	}
	if cfg.AppIssuer == "" {
		return nil, errors.New("app issuer not configured")
	}
	return verifyAppToken(cfg, raw)
}

func verifyAppToken(cfg VerifyConfig, raw string) (*AuthUser, error) {
	claims := &appClaims{}
	parseOpts := []jwt.ParserOption{
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithIssuer(cfg.AppIssuer),
	}
	if cfg.AppAudience != "" {
		parseOpts = append(parseOpts, jwt.WithAudience(cfg.AppAudience))
	}
	if leeway := jwtLeeway(); leeway > 0 {
		parseOpts = append(parseOpts, jwt.WithLeeway(leeway))
	}
	token, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return cfg.AppPublicKey, nil
	}, parseOpts...)
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, fmt.Errorf("token is not valid")
	}
	sub, err := claims.GetSubject()
	if err != nil || sub == "" {
		return nil, fmt.Errorf("token missing sub claim")
	}
	scopes := strings.Fields(claims.Scope)
	return &AuthUser{ID: sub, Email: claims.Email, Role: claims.Role, Issuer: cfg.AppIssuer, Scopes: scopes, ClientID: claims.ClientID}, nil
}

// HasScope reports whether the verified token includes scope. It is intended
// for OAuth/MCP tokens; normal portal JWTs deliberately have no scopes.
func (u *AuthUser) HasScope(scope string) bool {
	if u == nil {
		return false
	}
	for _, s := range u.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// JWTAuth returns a chi middleware that validates the Bearer JWT against the
// supplied VerifyConfig. On success the AuthUser is attached to the request
// context.
func JWTAuth(cfg VerifyConfig) func(http.Handler) http.Handler {
	return jwtAuth(cfg, "")
}

// JWTAuthWithChallenge validates a bearer token and advertises OAuth
// protected-resource metadata on 401 responses, as required for remote MCP.
func JWTAuthWithChallenge(cfg VerifyConfig, resourceMetadataURL string) func(http.Handler) http.Handler {
	challenge := `Bearer resource_metadata="` + resourceMetadataURL + `"`
	return jwtAuth(cfg, challenge)
}

func jwtAuth(cfg VerifyConfig, challenge string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenStr, ok := bearerToken(r)
			if !ok {
				if challenge != "" {
					w.Header().Set("WWW-Authenticate", challenge)
				}
				apierr.Unauthorized("missing or malformed Authorization header").Render(w)
				return
			}
			authUser, err := VerifyToken(cfg, tokenStr)
			if err != nil {
				log.Warn().Err(err).Str("path", r.URL.Path).Msg("JWT verification failed")
				if challenge != "" {
					w.Header().Set("WWW-Authenticate", challenge)
				}
				apierr.Unauthorized("invalid or expired token").Render(w)
				return
			}
			ctx := context.WithValue(r.Context(), CtxUser, authUser)
			ctx = actorctx.WithUserID(ctx, authUser.ID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserFromContext retrieves the AuthUser stored by JWTAuth middleware.
// Returns nil if the context does not contain an AuthUser (i.e., unauthenticated route).
func UserFromContext(ctx context.Context) *AuthUser {
	v := ctx.Value(CtxUser)
	if v == nil {
		return nil
	}
	u, _ := v.(*AuthUser)
	return u
}

// bearerToken extracts the raw token string from the Authorization header.
// Returns the token and true on success, or "", false if the header is absent
// or not of the form "Bearer <token>".
func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", false
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return "", false
	}
	token := strings.TrimSpace(parts[1])
	if token == "" {
		return "", false
	}
	return token, true
}
