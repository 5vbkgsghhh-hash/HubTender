package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/su10/hubtender/backend/internal/apikey"
	"github.com/su10/hubtender/backend/internal/handlers"
	"github.com/su10/hubtender/backend/internal/middleware"
	"github.com/su10/hubtender/backend/pkg/apierr"
)

// registerMachineRoutes — маршруты с МАШИННЫМ доступом: пускают по заголовку
// X-API-Key или по обычному JWT. Каждый вызов пишется в журнал «Настройки →
// Доступ к API». Хендлеры в основном общие с UI и про области ключа не знают —
// область и ограничение по тендерам проверяет маршрутный гейт; архив смет
// гейтится тумблерами и областями внутри своих хендлеров.
//
// Маршруты, перенесённые сюда из JWT-группы, из неё удалены: у chi при дубле
// побеждает поздняя регистрация.
func registerMachineRoutes(r chi.Router, d *deps, verifyCfg middleware.VerifyConfig, compressMW func(http.Handler) http.Handler) {
	archiveMW := middleware.JWTOrAPIKey(verifyCfg, d.apiAccessSvc)
	r.Group(func(r chi.Router) {
		r.Use(compressMW)
		r.Use(archiveMW)
		r.Use(middleware.APICallLogger(d.apiAccessSvc))

		r.Get("/api/v1/archive/positions/search", d.archiveH.SearchPositions)
		r.Post("/api/v1/archive/positions/suggest", d.archiveH.SuggestPositions)
		r.Get("/api/v1/archive/positions/{id}", d.archiveH.GetPosition)
		r.Post("/api/v1/archive/compose", d.archiveH.Compose)
		r.Get("/api/v1/archive/openapi.yaml", d.archiveH.OpenAPI)

		// Чтение тендера машинным ключом — область tenders:read. Хендлеры
		// общие с UI и про области не знают, поэтому область и ограничение по
		// тендерам проверяет маршрутный гейт.
		//
		// Узкий список тендеров для выбора цели (TenderConnector): без id
		// тендера в URL гейт проверяет только область, ограничение ключа по
		// тендерам применяет сам хендлер как фильтр выборки.
		r.With(middleware.RequireAPIKeyScope(apikey.ScopeTendersRead, "")).
			Get("/api/v1/tenders/brief", d.tenderBriefH.List)
		// Список позиций тендера: без него внешний код не может сопоставить
		// свои строки с id существующих позиций перед сборкой сметы.
		r.With(middleware.RequireAPIKeyScope(apikey.ScopeTendersRead, "id")).
			Get("/api/v1/tenders/{id}/positions", d.positionH.GetPositions)
		// Строки позиции — чтобы повторная выгрузка видела, что уже записано,
		// и не задваивала: идемпотентности у записи нет.
		r.With(middleware.RequireAPIKeyScope(apikey.ScopeTendersRead, "id")).
			Get("/api/v1/tenders/{id}/positions/{posId}/items", d.boqH.GetBoqItems)
		// Смета тендера целиком — то, что видит инженер на странице позиций:
		// шапка, позиции с итогами и КП, строки с названиями/ценами. Маршруты
		// сняты с JWT-группы (chi не ругается на дубль, а побеждает поздняя
		// регистрация — ключ остался бы за дверью). Только чтение.
		r.With(middleware.RequireAPIKeyScope(apikey.ScopeTendersRead, "id")).
			Get("/api/v1/tenders/{id}/overview", d.tenderH.GetTenderOverview)
		r.With(middleware.RequireAPIKeyScope(apikey.ScopeTendersRead, "id")).
			Get("/api/v1/tenders/{id}/positions/with-costs", d.positionCostsH.GetPositionsWithCosts)
		r.With(middleware.RequireAPIKeyScope(apikey.ScopeTendersRead, "id")).
			Get("/api/v1/tenders/{id}/boq-items-full", d.positionH.ListBoqItemsFullByTender)
		r.With(middleware.RequireAPIKeyScopeResolved(apikey.ScopeTendersRead, d.tenderOfPosition)).
			Get("/api/v1/positions/{id}/boq-items-full", d.positionH.ListBoqItemsFullByPosition)
		// Одна строка с ETag — нужен для If-Match при PATCH.
		r.With(middleware.RequireAPIKeyScopeResolved(apikey.ScopeTendersRead, d.tenderOfItem)).
			Get("/api/v1/items/{id}", d.boqH.GetBoqItem)

		// Запись строк BOQ по ключу — область tenders:write. Хендлеры общие с
		// UI; ключ действует от имени выпустившего пользователя. Там, где id
		// тендера в URL нет, его резолвит гейт по строке/позиции в БД.
		r.With(middleware.RequireAPIKeyScope(apikey.ScopeTendersWrite, "id")).
			Post("/api/v1/tenders/{id}/positions/{posId}/items", d.boqWH.CreateBoqItem)
		r.With(middleware.RequireAPIKeyScopeResolved(apikey.ScopeTendersWrite, d.tenderOfItem)).
			Patch("/api/v1/items/{id}", d.boqWH.UpdateBoqItem)
		r.With(middleware.RequireAPIKeyScopeResolved(apikey.ScopeTendersWrite, d.tenderOfPosition)).
			Post("/api/v1/positions/{id}/recompute-totals", d.positionWH.RecomputePositionTotals)

		// Проверка данных по ключу (конвейер проверки, этап 6). Хендлеры общие с
		// UI; человек с JWT проходит гейт без ограничений. Маршруты сняты с
		// JWT-группы — у chi побеждает поздняя регистрация.
		readV := middleware.RequireAPIKeyScope(apikey.ScopeVerificationRead, "id")
		writeV := middleware.RequireAPIKeyScope(apikey.ScopeVerificationWrite, "id")
		r.With(readV).Get("/api/v1/tenders/{id}/quality", d.qualityH.GetReport)
		r.With(middleware.RequireAPIKeyScope(apikey.ScopeVerificationRead, "")).
			Get("/api/v1/quality/rules", d.qualityH.GetRules)
		r.With(readV).Get("/api/v1/tenders/{id}/verification/sections", d.verifSectionsH.GetSections)
		r.With(readV).Get("/api/v1/tenders/{id}/cost-benchmarks", d.costBenchmarkH.GetReport)
		r.With(middleware.RequireAPIKeyScope(apikey.ScopeVerificationRead, "")).
			Get("/api/v1/benchmark-ranges", d.costBenchmarkH.GetRanges)
		r.With(readV).Get("/api/v1/tenders/{id}/brief", d.costBenchmarkH.GetBrief)

		r.With(writeV).Post("/api/v1/tenders/{id}/quality/verdict", d.qualityH.PostVerdict)
		r.With(writeV).Post("/api/v1/tenders/{id}/quality/verdicts", d.qualityH.PostVerdicts)
		r.With(writeV).Post("/api/v1/tenders/{id}/quality/checkpoint", d.qualityH.PostCheckpoint)
		r.With(writeV).Post("/api/v1/tenders/{id}/verification/sections/mark", d.verifSectionsH.PostMark)
		r.With(writeV).Post("/api/v1/tenders/{id}/verification/sections/unmark", d.verifSectionsH.PostUnmark)
		r.With(writeV).Put("/api/v1/tenders/{id}/brief", d.costBenchmarkH.PutBrief)
		// ИИ-разбор находок: оценки — чтение, запуск разбора — действие проверяющего.
		r.With(readV).Get("/api/v1/tenders/{id}/verification/ai-assessments", d.verifAIH.GetAssessments)
		r.With(writeV).Post("/api/v1/tenders/{id}/verification/ai-triage", d.verifAIH.PostTriage)

		registerRegistryBotRoutes(r, d.registryBotH)
	})
}

// registerRegistryBotRoutes — «Перечень тендеров» для внешнего Telegram-бота
// (api/REGISTRY.md): registry:read — поиск и карточка, registry:write — выдача
// напоминаний и запись в хронологию. Только по ключу. У перечня нет связи с
// тендерами, поэтому ключ, ограниченный списком тендеров, получает 403.
func registerRegistryBotRoutes(r chi.Router, h *handlers.TenderRegistryBotHandler) {
	regRead := middleware.RequireAPIKeyScopeResolved(apikey.ScopeRegistryRead, registryDeniesRestrictedKey)
	regWrite := middleware.RequireAPIKeyScopeResolved(apikey.ScopeRegistryWrite, registryDeniesRestrictedKey)
	r.With(requireAPIKey, regRead).Get("/api/v1/tender-registry/search", h.Search)
	r.With(requireAPIKey, regRead).Get("/api/v1/tender-registry/{id}/card", h.Card)
	r.With(requireAPIKey, regWrite).Post("/api/v1/tender-registry/reminders/claim", h.ClaimReminders)
	r.With(requireAPIKey, regWrite).Post("/api/v1/tender-registry/{id}/chronology", h.AppendChronology)
}

// requireAPIKey — маршрут только для машинного ключа: людям с JWT он не нужен
// (например, выдача напоминаний чату от их имени).
func requireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if middleware.APIKeyFromContext(r.Context()) == nil {
			apierr.ArchiveProblem(http.StatusForbidden, "API_KEY_REQUIRED",
				"Маршрут доступен только по машинному ключу (заголовок X-API-Key).", nil).Render(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}
