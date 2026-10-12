package mcpauth

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/su10/hubtender/backend/internal/middleware"
)

type HandlerConfig struct {
	PublicBaseURL string
	DCR           bool
}

type Handler struct {
	svc                 *Service
	cfg                 HandlerConfig
	mu                  sync.Mutex
	registrationWindows map[string]registrationWindow
}

type registrationWindow struct {
	Started time.Time
	Count   int
}

func NewHandler(svc *Service, cfg HandlerConfig) *Handler {
	return &Handler{svc: svc, cfg: cfg, registrationWindows: make(map[string]registrationWindow)}
}

func (h *Handler) RequireActiveGrant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil || !h.svc.IsPrincipalActive(r.Context(), u.ID, u.ClientID) {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+strings.TrimRight(h.cfg.PublicBaseURL, "/")+`/.well-known/oauth-protected-resource"`)
			writeOAuthError(w, http.StatusUnauthorized, ErrAccessDenied, "OAuth grant is revoked or user access is disabled")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) ProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 strings.TrimRight(h.cfg.PublicBaseURL, "/") + "/mcp",
		"authorization_servers":    []string{h.cfg.PublicBaseURL},
		"scopes_supported":         CurrentScopes,
		"bearer_methods_supported": []string{"header"},
	})
}

func (h *Handler) AuthorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	base := strings.TrimRight(h.cfg.PublicBaseURL, "/")
	body := map[string]any{
		"issuer":                                         base,
		"jwks_uri":                                       base + "/.well-known/jwks.json",
		"authorization_endpoint":                         base + "/oauth/authorize",
		"token_endpoint":                                 base + "/oauth/token",
		"revocation_endpoint":                            base + "/oauth/revoke",
		"response_types_supported":                       []string{"code"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"none"},
		"scopes_supported":                               CurrentScopes,
		"authorization_response_iss_parameter_supported": true,
		"client_id_metadata_document_supported":          true,
	}
	if h.cfg.DCR {
		body["registration_endpoint"] = base + "/oauth/register"
	}
	writeJSON(w, http.StatusOK, body)
}

func (h *Handler) Authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" {
		writeOAuthError(w, http.StatusBadRequest, ErrInvalidRequest, "response_type=code and code_challenge_method=S256 are required")
		return
	}
	in := AuthorizationRequest{
		ClientID:      q.Get("client_id"),
		RedirectURI:   q.Get("redirect_uri"),
		Scope:         q.Get("scope"),
		State:         q.Get("state"),
		CodeChallenge: q.Get("code_challenge"),
	}
	if _, _, err := h.svc.ValidateAuthorizationRequest(r.Context(), in); err != nil {
		writeServiceError(w, err)
		return
	}
	base := strings.TrimRight(h.cfg.PublicBaseURL, "/")
	target, _ := url.Parse(base + "/agent-connect")
	target.RawQuery = q.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

type consentRequest struct {
	ClientID            string `json:"client_id"`
	RedirectURI         string `json:"redirect_uri"`
	Scope               string `json:"scope"`
	State               string `json:"state"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
}

func (h *Handler) Consent(w http.ResponseWriter, r *http.Request) {
	u := middleware.UserFromContext(r.Context())
	if u == nil {
		writeOAuthError(w, http.StatusUnauthorized, ErrAccessDenied, "authentication required")
		return
	}
	var req consentRequest
	if err := decodeJSON(r, &req); err != nil || req.CodeChallengeMethod != "S256" {
		writeOAuthError(w, http.StatusBadRequest, ErrInvalidRequest, "invalid consent request")
		return
	}
	redirect, err := h.svc.BeginAuthorization(r.Context(), u.ID, AuthorizationRequest{
		ClientID: req.ClientID, RedirectURI: req.RedirectURI, Scope: req.Scope,
		State: req.State, CodeChallenge: req.CodeChallenge,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if parsed, parseErr := url.Parse(redirect); parseErr == nil {
		q := parsed.Query()
		q.Set("iss", strings.TrimRight(h.cfg.PublicBaseURL, "/"))
		parsed.RawQuery = q.Encode()
		redirect = parsed.String()
	}
	writeJSON(w, http.StatusOK, map[string]string{"redirect_uri": redirect})
}

func (h *Handler) Token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, ErrInvalidRequest, "invalid form")
		return
	}
	grantType := r.Form.Get("grant_type")
	clientID := r.Form.Get("client_id")
	ua, ip := r.UserAgent(), remoteIP(r)
	var (
		res *TokenResponse
		err error
	)
	switch grantType {
	case "authorization_code":
		res, err = h.svc.ExchangeCode(r.Context(), clientID, r.Form.Get("redirect_uri"), r.Form.Get("code"), r.Form.Get("code_verifier"), ua, ip)
	case "refresh_token":
		res, err = h.svc.RefreshToken(r.Context(), clientID, r.Form.Get("refresh_token"), ua, ip)
	default:
		err = ErrInvalidGrant
	}
	if err != nil {
		writeServiceError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, ErrInvalidRequest, "invalid form")
		return
	}
	if err := h.svc.RevokeToken(r.Context(), r.Form.Get("token")); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, errors.New("server_error"), "revocation failed")
		return
	}
	w.WriteHeader(http.StatusOK)
}

type registerRequest struct {
	ClientName      string   `json:"client_name"`
	RedirectURIs    []string `json:"redirect_uris"`
	Scope           string   `json:"scope"`
	ApplicationType string   `json:"application_type"`
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	if !h.allowRegistration(remoteIP(r)) {
		w.Header().Set("Retry-After", "60")
		writeOAuthError(w, http.StatusTooManyRequests, errors.New("temporarily_unavailable"), "registration rate limit exceeded")
		return
	}
	var req registerRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 128*1024)).Decode(&req); err != nil {
		writeOAuthError(w, http.StatusBadRequest, ErrInvalidRequest, "invalid registration document")
		return
	}
	c, err := h.svc.RegisterClient(r.Context(), req.ClientName, req.RedirectURIs, strings.Fields(req.Scope), req.ApplicationType)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id": c.ID, "client_name": c.Name, "redirect_uris": c.RedirectURIs,
		"scope": strings.Join(c.AllowedScopes, " "), "application_type": c.ApplicationType,
		"token_endpoint_auth_method": "none",
	})
}

func (h *Handler) ListGrants(w http.ResponseWriter, r *http.Request) {
	u := middleware.UserFromContext(r.Context())
	if u == nil {
		writeOAuthError(w, http.StatusUnauthorized, ErrAccessDenied, "authentication required")
		return
	}
	grants, err := h.svc.ListGrants(r.Context(), u.ID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": grants})
}

func (h *Handler) RevokeGrant(w http.ResponseWriter, r *http.Request) {
	u := middleware.UserFromContext(r.Context())
	if u == nil {
		writeOAuthError(w, http.StatusUnauthorized, ErrAccessDenied, "authentication required")
		return
	}
	if err := h.svc.RevokeGrant(r.Context(), u.ID, chi.URLParam(r, "clientId")); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) allowRegistration(ip string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	if len(h.registrationWindows) > 10000 {
		for key, window := range h.registrationWindows {
			if now.Sub(window.Started) > 2*time.Minute {
				delete(h.registrationWindows, key)
			}
		}
	}
	w := h.registrationWindows[ip]
	if w.Started.IsZero() || now.Sub(w.Started) >= time.Minute {
		h.registrationWindows[ip] = registrationWindow{Started: now, Count: 1}
		return true
	}
	if w.Count >= 10 {
		return false
	}
	w.Count++
	h.registrationWindows[ip] = w
	return true
}

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidClient):
		writeOAuthError(w, http.StatusBadRequest, ErrInvalidClient, "unknown or disabled client")
	case errors.Is(err, ErrInvalidGrant):
		writeOAuthError(w, http.StatusBadRequest, ErrInvalidGrant, "authorization code or refresh token is invalid")
	case errors.Is(err, ErrInvalidScope):
		writeOAuthError(w, http.StatusBadRequest, ErrInvalidScope, "requested scope is not allowed")
	case errors.Is(err, ErrInvalidRequest), errors.Is(err, ErrRegistrationClosed):
		writeOAuthError(w, http.StatusBadRequest, err, err.Error())
	case errors.Is(err, ErrAccessDenied):
		writeOAuthError(w, http.StatusForbidden, ErrAccessDenied, "user access is disabled or scope is not permitted")
	default:
		writeOAuthError(w, http.StatusInternalServerError, errors.New("server_error"), "internal authorization error")
	}
}

func writeOAuthError(w http.ResponseWriter, status int, code error, description string) {
	writeJSON(w, status, map[string]string{"error": code.Error(), "error_description": description})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 128*1024))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func remoteIP(r *http.Request) string {
	if raw := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(raw) != nil {
		return raw
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return ""
}
