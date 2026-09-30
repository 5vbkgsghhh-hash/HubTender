package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"

	"github.com/su10/hubtender/backend/internal/apikey"
	"github.com/su10/hubtender/backend/internal/middleware"
	"github.com/su10/hubtender/backend/internal/repository"
	"github.com/su10/hubtender/backend/internal/services"
	"github.com/su10/hubtender/backend/pkg/apierr"
)

// ApiAccessAdminRoles — кто управляет выдачей API. Ключ даёт машинный доступ
// ко всем сметам, поэтому гейт ролевой и серверный, а не только по allowed_pages.
var ApiAccessAdminRoles = map[string]bool{
	"administrator": true,
	"developer":     true,
}

// apiAccessServicer — граница сервиса.
type apiAccessServicer interface {
	ListKeys(ctx context.Context) ([]repository.ApiKeyRow, error)
	CreateKey(ctx context.Context, req services.CreateApiKeyRequest) (*services.IssuedApiKey, error)
	RevokeKey(ctx context.Context, id, revokedBy string) (*repository.ApiKeyRow, error)
	DeleteKey(ctx context.Context, id string) error
	Settings(ctx context.Context) (*repository.ApiAccessSettings, error)
	UpdateSettings(ctx context.Context, in repository.ApiAccessSettings, updatedBy string) (*repository.ApiAccessSettings, error)
	ListCallLog(ctx context.Context, f repository.ApiCallLogFilter) ([]repository.ApiCallLogEntry, error)
}

// ApiAccessHandler обслуживает /api/v1/admin/api-access/*.
type ApiAccessHandler struct {
	svc      apiAccessServicer
	validate *validator.Validate
}

// NewApiAccessHandler creates an ApiAccessHandler.
func NewApiAccessHandler(svc apiAccessServicer) *ApiAccessHandler {
	return &ApiAccessHandler{svc: svc, validate: validator.New()}
}

// ─── Ключи ──────────────────────────────────────────────────────────────────

// ListKeys handles GET /api/v1/admin/api-access/keys.
func (h *ApiAccessHandler) ListKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.svc.ListKeys(r.Context())
	if err != nil {
		apierr.InternalFromErr(w, r, err, "не удалось загрузить список ключей")
		return
	}
	renderJSON(w, r, http.StatusOK, dataEnvelope{Data: keys})
}

type createKeyReq struct {
	Name             string   `json:"name"               validate:"required,min=1,max=120"`
	Scopes           []string `json:"scopes"             validate:"required,min=1,dive,oneof=archive:read archive:write tenders:read tenders:write verification:read verification:write registry:read registry:write"`
	AllowedTenderIDs []string `json:"allowed_tender_ids" validate:"omitempty,dive,uuid"`
	ExpiresAt        *string  `json:"expires_at"`
}

// CreateKey handles POST /api/v1/admin/api-access/keys.
//
// Секрет возвращается ЕДИНСТВЕННЫЙ раз: в БД лежит только его SHA-256 хеш,
// восстановить ключ потом нельзя ни через API, ни из бэкапа.
func (h *ApiAccessHandler) CreateKey(w http.ResponseWriter, r *http.Request) {
	authUser := middleware.UserFromContext(r.Context())
	if authUser == nil {
		apierr.Unauthorized("missing auth context").Render(w)
		return
	}

	var req createKeyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierr.BadRequest("invalid JSON body").Render(w)
		return
	}
	if err := h.validate.Struct(req); err != nil {
		apierr.BadRequest("validation failed: " + err.Error()).Render(w)
		return
	}
	if len(req.AllowedTenderIDs) > 0 && hasRegistryScope(req.Scopes) {
		apierr.BadRequest("области registry:* не сочетаются со списком тендеров: у перечня нет связи с тендерами").Render(w)
		return
	}

	issued, err := h.svc.CreateKey(r.Context(), services.CreateApiKeyRequest{
		Name:             req.Name,
		Scopes:           req.Scopes,
		AllowedTenderIDs: req.AllowedTenderIDs,
		ExpiresAt:        req.ExpiresAt,
		CreatedBy:        authUser.ID,
	})
	if err != nil {
		if errors.Is(err, apikey.ErrUnknownScope) || errors.Is(err, apikey.ErrNoScopes) {
			apierr.BadRequest(err.Error()).Render(w)
			return
		}
		apierr.InternalFromErr(w, r, err, "не удалось выпустить ключ")
		return
	}
	renderJSON(w, r, http.StatusCreated, dataEnvelope{Data: issued})
}

// RevokeKey handles POST /api/v1/admin/api-access/keys/{id}/revoke.
func (h *ApiAccessHandler) RevokeKey(w http.ResponseWriter, r *http.Request) {
	authUser := middleware.UserFromContext(r.Context())
	if authUser == nil {
		apierr.Unauthorized("missing auth context").Render(w)
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		apierr.BadRequest("missing key id").Render(w)
		return
	}

	key, err := h.svc.RevokeKey(r.Context(), id, authUser.ID)
	if err != nil {
		if errors.Is(err, repository.ErrApiKeyNotFound) {
			apierr.NotFound("ключ не найден").Render(w)
			return
		}
		apierr.InternalFromErr(w, r, err, "не удалось отозвать ключ")
		return
	}
	renderJSON(w, r, http.StatusOK, dataEnvelope{Data: key})
}

// DeleteKey handles DELETE /api/v1/admin/api-access/keys/{id}.
func (h *ApiAccessHandler) DeleteKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		apierr.BadRequest("missing key id").Render(w)
		return
	}
	if err := h.svc.DeleteKey(r.Context(), id); err != nil {
		if errors.Is(err, repository.ErrApiKeyNotFound) {
			apierr.NotFound("ключ не найден").Render(w)
			return
		}
		apierr.InternalFromErr(w, r, err, "не удалось удалить ключ")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Настройки ──────────────────────────────────────────────────────────────

// GetSettings handles GET /api/v1/admin/api-access/settings.
func (h *ApiAccessHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	st, err := h.svc.Settings(r.Context())
	if err != nil {
		apierr.InternalFromErr(w, r, err, "не удалось загрузить настройки доступа к API")
		return
	}
	renderJSON(w, r, http.StatusOK, dataEnvelope{Data: st})
}

type updateSettingsReq struct {
	ArchiveSearchEnabled  bool `json:"archive_search_enabled"`
	ArchiveReadEnabled    bool `json:"archive_read_enabled"`
	ArchiveSuggestEnabled bool `json:"archive_suggest_enabled"`
	ArchiveComposeEnabled bool `json:"archive_compose_enabled"`
	MaxSearchLimit        int  `json:"max_search_limit"        validate:"min=1,max=1000"`
	MaxCandidateLimit     int  `json:"max_candidate_limit"     validate:"min=50,max=20000"`
	MaxSuggestQueries     int  `json:"max_suggest_queries"     validate:"min=1,max=500"`
	RateLimitPerMinute    int  `json:"rate_limit_per_minute"   validate:"min=0,max=100000"`
	CallLogRetentionDays  int  `json:"call_log_retention_days" validate:"min=1,max=365"`
}

// UpdateSettings handles PUT /api/v1/admin/api-access/settings.
func (h *ApiAccessHandler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	authUser := middleware.UserFromContext(r.Context())
	if authUser == nil {
		apierr.Unauthorized("missing auth context").Render(w)
		return
	}

	var req updateSettingsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierr.BadRequest("invalid JSON body").Render(w)
		return
	}
	if err := h.validate.Struct(req); err != nil {
		apierr.BadRequest("validation failed: " + err.Error()).Render(w)
		return
	}

	st, err := h.svc.UpdateSettings(r.Context(), repository.ApiAccessSettings{
		ArchiveSearchEnabled:  req.ArchiveSearchEnabled,
		ArchiveReadEnabled:    req.ArchiveReadEnabled,
		ArchiveSuggestEnabled: req.ArchiveSuggestEnabled,
		ArchiveComposeEnabled: req.ArchiveComposeEnabled,
		MaxSearchLimit:        req.MaxSearchLimit,
		MaxCandidateLimit:     req.MaxCandidateLimit,
		MaxSuggestQueries:     req.MaxSuggestQueries,
		RateLimitPerMinute:    req.RateLimitPerMinute,
		CallLogRetentionDays:  req.CallLogRetentionDays,
	}, authUser.ID)
	if err != nil {
		apierr.InternalFromErr(w, r, err, "не удалось сохранить настройки доступа к API")
		return
	}
	renderJSON(w, r, http.StatusOK, dataEnvelope{Data: st})
}

// ─── Журнал ─────────────────────────────────────────────────────────────────

// ListCallLog handles GET /api/v1/admin/api-access/calls.
func (h *ApiAccessHandler) ListCallLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	entries, err := h.svc.ListCallLog(r.Context(), repository.ApiCallLogFilter{
		ApiKeyID:   q.Get("api_key_id"),
		OnlyErrors: boolParam(q.Get("only_errors"), false),
		Limit:      intParam(q.Get("limit"), 100),
	})
	if err != nil {
		apierr.InternalFromErr(w, r, err, "не удалось загрузить журнал вызовов")
		return
	}
	renderJSON(w, r, http.StatusOK, dataEnvelope{Data: entries})
}

// hasRegistryScope — среди областей есть registry:*. Ключ с ограничением по
// тендерам к перечню не допускается (гейт маршрута ответит 403), поэтому такой
// ключ не выпускаем вовсе.
func hasRegistryScope(scopes []string) bool {
	for _, s := range scopes {
		if s == apikey.ScopeRegistryRead || s == apikey.ScopeRegistryWrite {
			return true
		}
	}
	return false
}
