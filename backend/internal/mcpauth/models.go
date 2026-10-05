package mcpauth

import "time"

const (
	ScopeTendersRead    = "tenders:read"
	ScopeArchiveRead    = "archive:read"
	ScopeLibraryRead    = "library:read"
	ScopePricingWrite   = "pricing:write"
	ScopeTemplatesWrite = "templates:write"
)

// CurrentScopes is the engineer-facing default published in OAuth discovery
// and used for new DCR clients. Template writes require explicit registration
// by an allowed senior role. Old draft/apply grants do not authorize direct writes.
var CurrentScopes = []string{
	ScopeTendersRead,
	ScopeArchiveRead,
	ScopeLibraryRead,
	ScopePricingWrite,
}

var AllScopes = []string{
	ScopeTendersRead,
	ScopeArchiveRead,
	ScopeLibraryRead,
	ScopePricingWrite,
	ScopeTemplatesWrite,
}

type Client struct {
	ID               string   `json:"client_id"`
	Name             string   `json:"client_name"`
	RedirectURIs     []string `json:"redirect_uris"`
	AllowedScopes    []string `json:"allowed_scopes"`
	ApplicationType  string   `json:"application_type"`
	RegistrationKind string   `json:"registration_kind"`
	Disabled         bool     `json:"disabled"`
}

type AuthorizationCode struct {
	ID            string
	CodeHash      string
	ClientID      string
	UserID        string
	RedirectURI   string
	Scopes        []string
	CodeChallenge string
	ExpiresAt     time.Time
}

type RefreshToken struct {
	ID            string
	TokenHash     string
	TokenFamilyID string
	ClientID      string
	UserID        string
	Scopes        []string
	IssuedAt      time.Time
	ExpiresAt     time.Time
	RevokedAt     *time.Time
}

type Grant struct {
	ClientID   string     `json:"client_id"`
	ClientName string     `json:"client_name"`
	Scopes     []string   `json:"scopes"`
	GrantedAt  time.Time  `json:"granted_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope"`
}
