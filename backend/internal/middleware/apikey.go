package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/su10/hubtender/backend/internal/actorctx"
	"github.com/su10/hubtender/backend/pkg/apierr"
)

// APIKeyHeader — заголовок машинного доступа.
const APIKeyHeader = "X-API-Key"

// CtxAPIKey — ключ контекста для принципала машинного доступа.
const CtxAPIKey ctxKey = "api_key"

// CtxCallStat — ключ контекста для метаданных вызова (журнал API).
const CtxCallStat ctxKey = "api_call_stat"

// APIKeyPrincipal — то, чем ключ ограничен. Секрета здесь нет.
type APIKeyPrincipal struct {
	ID               string
	Name             string
	Scopes           []string
	AllowedTenderIDs []string
}

// HasScope сообщает, разрешена ли ключу область.
func (p *APIKeyPrincipal) HasScope(want string) bool {
	if p == nil {
		return false
	}
	for _, s := range p.Scopes {
		if s == want {
			return true
		}
	}
	return false
}

// AllowsTender — ограничен ли ключ списком тендеров. Пустой список = все.
func (p *APIKeyPrincipal) AllowsTender(tenderID string) bool {
	if p == nil || len(p.AllowedTenderIDs) == 0 {
		return true
	}
	for _, id := range p.AllowedTenderIDs {
		if id == tenderID {
			return true
		}
	}
	return false
}

// ErrAPIKeyRateLimited — ключ упёрся в лимит запросов.
var ErrAPIKeyRateLimited = errors.New("api key rate limit exceeded")

// APIKeyVerifier — проверка секрета из заголовка. Реализуется сервисным слоем.
type APIKeyVerifier interface {
	// VerifyAPIKey возвращает принципала ключа и пользователя, от имени
	// которого ключ действует (нужен для аудита и прав).
	VerifyAPIKey(ctx context.Context, secret string) (*APIKeyPrincipal, *AuthUser, error)
}

// APIKeyFromContext достаёт принципала ключа; nil — запрос пришёл не по ключу.
func APIKeyFromContext(ctx context.Context) *APIKeyPrincipal {
	v, _ := ctx.Value(CtxAPIKey).(*APIKeyPrincipal)
	return v
}

// JWTOrAPIKey пускает запрос по одному из двух путей: заголовок X-API-Key
// (машинный доступ) либо обычный Bearer JWT (человек в браузере).
//
// Ключ проверяется ПЕРВЫМ и, если он есть, JWT не рассматривается: смешивать
// два принципала в одном запросе нельзя — иначе непонятно, чьи ограничения
// применять.
func JWTOrAPIKey(cfg VerifyConfig, verifier APIKeyVerifier) func(http.Handler) http.Handler {
	jwtOnly := JWTAuth(cfg)
	return func(next http.Handler) http.Handler {
		jwtChain := jwtOnly(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			secret := strings.TrimSpace(r.Header.Get(APIKeyHeader))
			if secret == "" || verifier == nil {
				jwtChain.ServeHTTP(w, r)
				return
			}

			principal, authUser, err := verifier.VerifyAPIKey(r.Context(), secret)
			if err != nil {
				if errors.Is(err, ErrAPIKeyRateLimited) {
					apierr.TooManyRequests("превышен лимит запросов для ключа").Render(w)
					return
				}
				// Причину наружу не раскрываем: отозван, просрочен или подделан —
				// для вызывающего это один и тот же ответ.
				log.Warn().Str("path", r.URL.Path).Msg("API key verification failed")
				apierr.Unauthorized("invalid API key").Render(w)
				return
			}

			ctx := context.WithValue(r.Context(), CtxAPIKey, principal)
			ctx = context.WithValue(ctx, CtxUser, authUser)
			ctx = actorctx.WithUserID(ctx, authUser.ID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ─── Журнал вызовов ─────────────────────────────────────────────────────────

// CallStat — то, что хендлер может дописать о вызове для журнала.
type CallStat struct {
	ItemsAffected *int
	DryRun        *bool
	ErrorCode     string
}

// CallStatFromContext возвращает изменяемую запись статистики вызова.
// nil — журналирование для этого маршрута не включено.
func CallStatFromContext(ctx context.Context) *CallStat {
	v, _ := ctx.Value(CtxCallStat).(*CallStat)
	return v
}

// SetCallItems помечает, сколько строк затронул вызов.
func SetCallItems(ctx context.Context, items int, dryRun bool) {
	if s := CallStatFromContext(ctx); s != nil {
		s.ItemsAffected = &items
		s.DryRun = &dryRun
	}
}

// CallRecord — то, что уходит в журнал.
type CallRecord struct {
	APIKeyID      string
	UserID        string
	Method        string
	Path          string
	Status        int
	DurationMs    int
	ErrorCode     string
	ItemsAffected *int
	DryRun        *bool
}

// CallSink — приёмник записей журнала (сервисный слой).
type CallSink interface {
	RecordAPICall(rec CallRecord)
}

// APICallLogger пишет метаданные вызова в журнал API.
//
// Приёмник вызывается ПОСЛЕ ответа и обязан быть неблокирующим: журнал —
// диагностика, он не должен ни задерживать ответ, ни валить запрос.
func APICallLogger(sink CallSink) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if sink == nil {
				next.ServeHTTP(w, r)
				return
			}
			stat := &CallStat{}
			ctx := context.WithValue(r.Context(), CtxCallStat, stat)
			r = r.WithContext(ctx)

			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(rw, r)

			rec := CallRecord{
				Method:        r.Method,
				Path:          r.URL.Path,
				Status:        rw.status,
				DurationMs:    int(time.Since(start).Milliseconds()),
				ErrorCode:     stat.ErrorCode,
				ItemsAffected: stat.ItemsAffected,
				DryRun:        stat.DryRun,
			}
			if p := APIKeyFromContext(r.Context()); p != nil {
				rec.APIKeyID = p.ID
			}
			if u := UserFromContext(r.Context()); u != nil {
				rec.UserID = u.ID
			}
			sink.RecordAPICall(rec)
		})
	}
}

// RequireAPIKeyScope — маршрутный гейт для эндпоинтов ВНЕ домена архива,
// открытых машинному доступу.
//
// Нужен потому, что такие хендлеры (например список позиций тендера) написаны
// для человека с JWT и про области не знают: без гейта перенос маршрута под
// JWTOrAPIKey открыл бы его ЛЮБОМУ валидному ключу, включая чисто архивный.
//
// Человек с JWT проходит без ограничений — у него права от роли и списка
// страниц. Для ключа проверяется область и, если задано имя URL-параметра с id
// тендера, принадлежность этого тендера списку разрешённых для ключа.
func RequireAPIKeyScope(scope, tenderURLParam string) func(http.Handler) http.Handler {
	return RequireAPIKeyScopeResolved(scope, func(r *http.Request) (string, error) {
		if tenderURLParam == "" {
			return "", nil
		}
		return chi.URLParam(r, tenderURLParam), nil
	})
}

// TenderResolver достаёт id тендера для запроса, у которого его нет в URL
// (например PATCH /items/{id} — тендер известен только по строке в БД).
// Пустая строка = ограничение по тендерам не проверять. Ошибка резолвера
// (в т.ч. «не найдено») превращается в отказ: ключ, ограниченный тендерами,
// не должен писать в строку, чью принадлежность подтвердить нельзя.
type TenderResolver func(r *http.Request) (string, error)

// RequireAPIKeyScopeResolved — то же, что RequireAPIKeyScope, но id тендера
// для проверки ограничения ключа вычисляет resolve. Резолвер вызывается
// только для ключа, ограниченного списком тендеров: остальным лишний поход
// в БД не нужен.
func RequireAPIKeyScopeResolved(scope string, resolve TenderResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := APIKeyFromContext(r.Context())
			if p == nil {
				next.ServeHTTP(w, r)
				return
			}
			if !p.HasScope(scope) {
				markCallError(r.Context(), "API_KEY_SCOPE_DENIED")
				apierr.ArchiveProblem(http.StatusForbidden, "API_KEY_SCOPE_DENIED",
					"Ключу не выдана требуемая область доступа.",
					map[string]any{"requiredScope": scope}).Render(w)
				return
			}
			if resolve != nil && len(p.AllowedTenderIDs) > 0 {
				tenderID, err := resolve(r)
				if err != nil {
					markCallError(r.Context(), "API_KEY_TENDER_DENIED")
					apierr.ArchiveProblem(http.StatusForbidden, "API_KEY_TENDER_DENIED",
						"Ключ ограничен списком тендеров, а принадлежность запрошенного объекта тендеру подтвердить не удалось.",
						nil).Render(w)
					return
				}
				if tenderID != "" && !p.AllowsTender(tenderID) {
					markCallError(r.Context(), "API_KEY_TENDER_DENIED")
					apierr.ArchiveProblem(http.StatusForbidden, "API_KEY_TENDER_DENIED",
						"Ключ ограничен списком тендеров, и запрошенный в него не входит.",
						map[string]any{"tenderId": tenderID}).Render(w)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// markCallError помечает код отказа для журнала вызовов.
func markCallError(ctx context.Context, code string) {
	if s := CallStatFromContext(ctx); s != nil {
		s.ErrorCode = code
	}
}
