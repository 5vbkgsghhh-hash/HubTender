package apierr

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/getsentry/sentry-go"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog/log"
)

// InternalFromErr is the recommended way for handlers to surface an
// internal error: it logs the underlying err (with method/path/request id +
// any caller-supplied k/v context) to zerolog at ERROR level, then renders
// the safe RFC 7807 "Internal Server Error" body to the client.
//
// Why a helper:
//
//	handlers historically did `apierr.InternalError("...").Render(w)` and
//	threw away the inner err. That made schema-drift / pgx errors invisible
//	in prod — operators had to add ad-hoc logging or attach a debugger to
//	diagnose every new 500. This helper keeps the response surface
//	identical (same problem+json body, same status, no leak of error text
//	to the client) while making the server-side log self-sufficient.
//
// detail is the user-facing summary (RFC 7807 `detail`). err is logged but
// NEVER sent to the client. Extra k/v pairs are added to the log event as
// "kN" / "vN" — keep them short (tender_id, position_id, etc.). Caller is
// responsible for NOT putting tokens / passwords / DSNs into either.
func InternalFromErr(w http.ResponseWriter, r *http.Request, err error, detail string, kv ...any) {
	var ownership *pgconn.PgError
	if errors.As(err, &ownership) && ownership.Code == "42501" && (strings.HasPrefix(ownership.Message, "PRICING_OWNER_") || strings.HasPrefix(ownership.Message, "PRICING_ACTOR_")) {
		message := "Изменять или удалять расчёт может только его владелец"
		if strings.HasPrefix(ownership.Message, "PRICING_OWNER_UNASSIGNED") {
			message = "Автор расчёта не определён или авторов несколько; требуется назначение владельца"
		}
		Forbidden(message).Render(w)
		return
	}
	// Клиент оборвал запрос (навигация / AbortController): r.Context() отменён.
	// Это не серверная ошибка — не капчим в Sentry, отдаём 499. Только
	// context.Canceled: context.DeadlineExceeded (серверные таймауты) должен
	// и дальше попадать в Sentry.
	if errors.Is(err, context.Canceled) || r.Context().Err() == context.Canceled {
		log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Msg("request canceled by client")
		New(499, "Client Closed Request", "Запрос отменён клиентом").Render(w)
		return
	}

	rid := r.Header.Get("X-Request-ID")

	event := log.Error().
		Err(err).
		Str("method", r.Method).
		Str("path", r.URL.Path)
	if rid != "" {
		event = event.Str("request_id", rid)
	}
	// Pairwise k/v: even index = string key, odd index = value.
	for i := 0; i+1 < len(kv); i += 2 {
		k, ok := kv[i].(string)
		if !ok {
			continue
		}
		event = event.Interface(k, kv[i+1])
	}
	event.Msg("internal error")

	// Sentry: захватываем underlying err с request-scope тегами. sentryhttp
	// middleware кладёт per-request hub в context; fallback на глобальный hub,
	// если init был с пустым DSN (тогда CaptureException — no-op).
	hub := sentry.GetHubFromContext(r.Context())
	if hub == nil {
		hub = sentry.CurrentHub()
	}
	hub.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("path", r.URL.Path)
		scope.SetTag("method", r.Method)
		if rid != "" {
			scope.SetTag("request_id", rid)
		}
		scope.SetExtra("detail", detail)
		for i := 0; i+1 < len(kv); i += 2 {
			if k, ok := kv[i].(string); ok {
				scope.SetExtra(k, kv[i+1])
			}
		}
		hub.CaptureException(err)
	})

	New(http.StatusInternalServerError, "Internal Server Error", detail).Render(w)
}
