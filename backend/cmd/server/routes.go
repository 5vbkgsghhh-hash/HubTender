package main

import (
	"net/http"
	"strings"
	"time"

	sentryhttp "github.com/getsentry/sentry-go/http"
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"

	"github.com/su10/hubtender/backend/internal/auth"
	"github.com/su10/hubtender/backend/internal/config"
	"github.com/su10/hubtender/backend/internal/handlers"
	"github.com/su10/hubtender/backend/internal/middleware"
)

// apiCompressMiddleware returns the gzip middleware for JSON API groups.
//
// Агрегатные чтения крупных тендеров отдают мегабайты (positions/with-costs на
// 11 тыс. позиций — ~10 МБ), а прод-nginx сжатие не включает: на ~1 МБ/с такой
// ответ не укладывался в 10-секундный таймаут apiFetch, и «Позиции заказчика»
// рисовали пустую таблицу при полностью залитых данных.
//
// Вешается на группы API, а НЕ глобально: /api/v1/ws хайджекает соединение.
// 304 не задевает — renderJSON не ставит Content-Type на этой ветке, а chi
// сжимает только по Content-Type ответа.
func apiCompressMiddleware() func(http.Handler) http.Handler {
	return chimiddleware.Compress(5)
}

// newRouter builds the chi router with global middleware, public routes and
// the authenticated API group. Extracted from main() verbatim (section 9).
func newRouter(
	cfg *config.Config,
	d *deps,
	authH *auth.Handler,
	verifyCfg middleware.VerifyConfig,
	mcpVerifyCfg middleware.VerifyConfig,
	logger zerolog.Logger,
) *chi.Mux {
	r := chi.NewRouter()

	// Global middleware.
	r.Use(chimiddleware.RequestID)
	if cfg.SentryDSN != "" {
		// Repanic: true → sentryhttp captures the panic, sends it to Sentry,
		// and re-raises so our middleware.Recoverer still returns 500 and logs.
		sentryMW := sentryhttp.New(sentryhttp.Options{Repanic: true})
		r.Use(sentryMW.Handle)
	}
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestLogger(logger))
	r.Use(chimiddleware.Timeout(5 * time.Minute))
	r.Use(corsMiddleware(cfg.CORSOrigins))

	// Public routes.
	r.Get("/health", d.healthH.ServeHTTP)
	r.Get("/health/db", d.healthH.CheckDB)
	r.Get("/health/cache", d.healthH.CacheStats)
	r.Get("/health/recalc", d.recalcHealthH.Diagnostics)
	r.Get("/health/ai", d.aiHealthH.Diagnostics)

	// Public auth routes — login / register / refresh / forgot / reset do
	// NOT require an existing JWT. JWKS is served public so any RP can
	// verify our access tokens.
	r.Post("/api/v1/auth/login", authH.Login)
	r.Post("/api/v1/auth/register", authH.Register)
	r.Post("/api/v1/auth/refresh", authH.Refresh)
	r.Post("/api/v1/auth/logout", authH.Logout)
	r.Post("/api/v1/auth/forgot-password", authH.ForgotPassword)
	r.Post("/api/v1/auth/reset-password", authH.ResetPassword)
	r.Get("/.well-known/jwks.json", authH.JWKS)

	if cfg.MCPEnabled {
		r.Get("/.well-known/oauth-protected-resource", d.mcpOAuthH.ProtectedResourceMetadata)
		r.Get("/.well-known/oauth-protected-resource/mcp", d.mcpOAuthH.ProtectedResourceMetadata)
		r.Get("/.well-known/oauth-authorization-server", d.mcpOAuthH.AuthorizationServerMetadata)
		r.Get("/oauth/authorize", d.mcpOAuthH.Authorize)
		r.Post("/oauth/token", d.mcpOAuthH.Token)
		r.Post("/oauth/revoke", d.mcpOAuthH.Revoke)
		r.Post("/oauth/register", d.mcpOAuthH.Register)
		resourceMetadata := strings.TrimRight(cfg.AppBaseURL, "/") + "/.well-known/oauth-protected-resource"
		mcpHandler := middleware.JWTAuthWithChallenge(mcpVerifyCfg, resourceMetadata)(d.mcpOAuthH.RequireActiveGrant(d.mcpHTTP))
		originProtection := http.NewCrossOriginProtection()
		for _, origin := range cfg.CORSOrigins {
			_ = originProtection.AddTrustedOrigin(origin)
		}
		r.Handle("/mcp", originProtection.Handler(mcpHandler))
	}

	// Authenticated API routes — app-issued JWT only (see VerifyConfig).
	authMW := middleware.JWTAuth(verifyCfg)

	compressMW := apiCompressMiddleware()

	registerMachineRoutes(r, d, verifyCfg, compressMW)

	r.Group(func(r chi.Router) {
		r.Use(compressMW)
		r.Use(authMW)
		templateAdminMW := middleware.RequireRoles(map[string]bool{"veduschiy_inzhener": true, "administrator": true, "developer": true})

		r.Get("/api/v1/me", d.meH.GetMe)
		r.Get("/api/v1/me/permissions", d.meH.GetPermissions)
		r.Get("/api/v1/me/telegram", d.telegramH.GetStatus)
		r.Post("/api/v1/me/telegram/link", d.telegramH.PostLink)
		r.Delete("/api/v1/me/telegram", d.telegramH.DeleteLink)
		r.Get("/api/v1/me/deadline-extensions", d.meH.GetDeadlineExtensions)
		r.Post("/api/v1/me/reapply-access", d.meH.ReapplyAccess)

		// App-auth: /me equivalent that returns a UserPayload shape identical
		// to the login response (so the frontend can use one type across
		// login/refresh/me).
		r.Get("/api/v1/auth/me", authH.Me)
		r.Post("/api/v1/auth/change-password", authH.ChangePassword)
		if cfg.MCPEnabled {
			r.Post("/api/v1/oauth/authorize", d.mcpOAuthH.Consent)
			r.Get("/api/v1/oauth/grants", d.mcpOAuthH.ListGrants)
			r.Delete("/api/v1/oauth/grants/{clientId}", d.mcpOAuthH.RevokeGrant)

			r.Get("/api/v1/tenders/{id}/pricing-state", d.pricingH.GetState)
			r.Get("/api/v1/tenders/{id}/pricing-qa", d.pricingH.QAReport)
		}

		r.Get("/api/v1/references/roles", d.refH.GetRoles)
		r.Get("/api/v1/references/units", d.refH.GetUnits)
		r.Get("/api/v1/references/material-names", d.refH.GetMaterialNames)
		r.Get("/api/v1/references/work-names", d.refH.GetWorkNames)
		r.Get("/api/v1/references/cost-categories", d.refH.GetCostCategories)
		r.Get("/api/v1/references/detail-cost-categories", d.refH.GetDetailCostCategories)

		// Phase 3 — tenders, positions, BOQ items.
		// Slice 1: reads.
		r.Get("/api/v1/tenders", d.tenderH.GetTenders)
		r.Get("/api/v1/exchange-rates", d.cbrH.GetExchangeRates)
		// Этап 2.1 (аналитика качества расчёта). Путь /quality занят страницей
		// «Проверка данных» (main, прод-контракт) — аналитика живёт отдельно.
		r.Get("/api/v1/tenders/{id}/quality-analytics", d.qualityAnalyticsH.TenderQuality)
		r.Get("/api/v1/tenders/{id}/price-benchmarks", d.priceBenchmarkH.TenderPriceBenchmarks)
		r.Get("/api/v1/tenders/{id}/price-benchmarks/{itemId}/history", d.priceBenchmarkH.ItemHistory)
		r.Get("/api/v1/tenders/{id}/price-source-quality", d.priceSourceH.TenderPriceSourceQuality)
		r.Get("/api/v1/tenders/{id}/action-plan", d.actionPlanH.TenderActionPlan)
		r.Get("/api/v1/tenders/{id}/change-impact", d.changeImpactH.TenderChangeImpact)
		r.Get("/api/v1/tenders/{id}/review-report", d.reviewPackH.TenderReviewReport)
		r.Get("/api/v1/tenders/{id}/review-report.xlsx", d.reviewPackH.TenderReviewReportXLSX)
		r.Get("/api/v1/positions/boq-preview", d.positionH.GetBoqPreview)
		r.Post("/api/v1/positions/boq-preview", d.positionH.PostBoqPreview)
		r.Get("/api/v1/positions/{id}/with-tender", d.positionH.GetPositionWithTender)
		r.Get("/api/v1/tenders/{id}/construction-cost-volumes", d.ccvH.ListByTender)

		// Проверка данных: чтение, вердикты, разделы, эталоны и выжимка — в
		// машинной группе выше (области verification:*). Здесь остаётся то, что
		// ключу не открывается: рассылка в Telegram, правка справочника эталонов
		// (роли BenchmarkRangeEditorRoles) и выгрузка вердиктов по всей базе.
		r.Post("/api/v1/tenders/{id}/verification/dispatch/preview", d.telegramH.PostPreview)
		r.Post("/api/v1/tenders/{id}/verification/dispatch", d.telegramH.PostDispatch)
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireRoles(handlers.BenchmarkRangeEditorRoles))
			r.Post("/api/v1/benchmark-ranges", d.costBenchmarkH.PostRange)
			r.Put("/api/v1/benchmark-ranges/{rangeId}", d.costBenchmarkH.PutRange)
			r.Delete("/api/v1/benchmark-ranges/{rangeId}", d.costBenchmarkH.DeleteRange)
		})
		r.Get("/api/v1/quality/export", d.qualityH.GetExport)
		r.Post("/api/v1/construction-cost-volumes", d.ccvH.Upsert)

		// Slice 2: writes with optimistic concurrency.
		r.Post("/api/v1/tenders", d.tenderWH.CreateTender)
		r.Patch("/api/v1/tenders/{id}", d.tenderWH.UpdateTender)
		r.Patch("/api/v1/tenders/{id}/admin-fields", d.tenderWH.AdminPatchTender)
		r.Post("/api/v1/tenders/{id}/financial-approval", d.tenderWH.ApproveFinancial)
		r.Delete("/api/v1/tenders/{id}", d.tenderWH.DeleteTender)

		r.Post("/api/v1/positions", d.positionWH.CreatePosition)
		r.Post("/api/v1/positions/bulk-delete", d.positionWH.BulkDeletePositions)
		r.Post("/api/v1/positions/additional", d.positionWH.CreateAdditionalPosition)
		r.Post("/api/v1/positions/bulk", d.positionWH.BulkInsertPositions)
		r.Patch("/api/v1/positions/note", d.positionWH.UpdatePositionsNote)
		r.Post("/api/v1/positions/clear-boq", d.positionWH.ClearPositionsBoq)
		r.Patch("/api/v1/positions/level", d.positionWH.ShiftPositionsLevel)
		r.Patch("/api/v1/positions/{id}/fields", d.positionWH.UpdatePositionFields)
		r.Post("/api/v1/items/{id}/recompute-linked-materials", d.boqWH.RecomputeLinkedMaterials)
		r.Post("/api/v1/positions/{id}/copy-from", d.boqWH.CopyPositionItems)
		r.Patch("/api/v1/positions/{id}", d.positionWH.UpdatePosition)

		r.Delete("/api/v1/items/{id}", d.boqWH.DeleteBoqItem)
		r.Post("/api/v1/templates/{templateId}/insert-into-position", d.boqWH.InsertTemplate)

		// Slice 3a: ported RPCs.
		// RETIRED (stage 0.1.2.2): commercial costs are calculation results and are
		// never accepted from a client. Kept registered as an explicit tombstone —
		// always 410 COMMERCIAL_COST_WRITE_RETIRED, never a mutation, never a 404.
		r.Patch("/api/v1/items/bulk-commercial", d.bulkBoqH.RetiredBulkCommercial)
		r.Patch("/api/v1/tenders/{id}/boq/quote-link", d.bulkBoqH.SetQuoteLinkByName)
		r.Patch("/api/v1/boq/quote-link-by-ids", d.bulkBoqH.SetQuoteLinkByIDs)
		r.Post("/api/v1/timeline/groups/{id}/quality", d.timelineH.SetGroupQuality)
		r.Post("/api/v1/timeline/iterations/{id}/respond", d.timelineH.RespondIteration)
		r.Get("/api/v1/timeline/assignable-users", d.timelineH.ListAssignableUsers)
		r.Post("/api/v1/timeline/iterations", d.timelineH.CreateIteration)
		r.Get("/api/v1/timeline/tenders", d.timelineH.ListTimelineTenders)
		r.Get("/api/v1/timeline/tenders/{tenderId}/groups", d.timelineH.ListTenderGroups)
		r.Post("/api/v1/timeline/tenders/{tenderId}/reconcile-groups", d.timelineH.ReconcileGroups)
		r.Get("/api/v1/timeline/groups/{groupId}/iterations", d.timelineH.ListGroupIterations)

		// Slice 3b: remaining simple RPCs.
		r.Post("/api/v1/users/register", d.userRegH.Register)
		r.Post("/api/v1/tenders/{id}/subcontract-exclusions/toggle", d.subcontractH.ToggleExclusion)

		// Phase 4c-lite: bulk BOQ import (replaces public.bulk_import_client_position_boq RPC).
		r.Post("/api/v1/imports/boq", d.importBoqH.BulkImport)
		r.Post("/api/v1/tenders/{id}/boq-import/analyze", d.smartImportH.AnalyzeBoqImport)
		r.Post("/api/v1/tenders/{id}/boq-import/execute", d.smartImportH.ExecuteBoqImport)
		r.Post("/api/v1/tenders/{id}/boq-import/suggest-nomenclature", d.smartImportH.SuggestNomenclature)
		// Этап 2.3: персональная память импорта (user-scoped, §10).
		r.Get("/api/v1/boq-import/mapping-profiles", d.importMemoryH.ListMappingProfiles)
		r.Patch("/api/v1/boq-import/mapping-profiles/{id}", d.importMemoryH.PatchMappingProfile)
		r.Delete("/api/v1/boq-import/mapping-profiles/{id}", d.importMemoryH.DeleteMappingProfile)
		r.Get("/api/v1/boq-import/nomenclature-aliases", d.importMemoryH.ListNomenclatureAliases)
		r.Delete("/api/v1/boq-import/nomenclature-aliases/{id}", d.importMemoryH.DeleteNomenclatureAlias)

		// Phase 5: version transfer (replaces public.execute_version_transfer RPC).
		r.Post("/api/v1/tenders/{id}/versions/transfer", d.transferH.Transfer)

		// Phase 5: duplicate tender as new version (calls SQL function
		// public.clone_tender_as_new_version, ported into Yandex schema).
		r.Post("/api/v1/tenders/{id}/versions/clone", d.cloneH.Clone)

		// Управление выдачей API («Настройки → Доступ к API»). Ключ открывает
		// машинный доступ ко всем сметам, поэтому гейт ролевой и серверный.
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireRoles(handlers.ApiAccessAdminRoles))

			r.Get("/api/v1/admin/api-access/keys", d.apiAccessH.ListKeys)
			r.Post("/api/v1/admin/api-access/keys", d.apiAccessH.CreateKey)
			r.Post("/api/v1/admin/api-access/keys/{id}/revoke", d.apiAccessH.RevokeKey)
			r.Delete("/api/v1/admin/api-access/keys/{id}", d.apiAccessH.DeleteKey)
			r.Get("/api/v1/admin/api-access/settings", d.apiAccessH.GetSettings)
			r.Put("/api/v1/admin/api-access/settings", d.apiAccessH.UpdateSettings)
			r.Get("/api/v1/admin/api-access/calls", d.apiAccessH.ListCallLog)
		})

		// Phase 5: tender notes (per-user; privileged roles see all).
		r.Get("/api/v1/tenders/{id}/notes", d.tenderNotesH.List)
		r.Put("/api/v1/tenders/{id}/notes", d.tenderNotesH.Save)

		// Phase 5: restore a DELETE'd BOQ item from its audit record.
		r.Post("/api/v1/boq-audit/{auditId}/rollback", d.boqAuditRollbackH.Rollback)
		r.Get("/api/v1/boq-audit", d.boqAuditRollbackH.ListByPosition)

		// Phase 5: tasks (user_tasks) + per-user work settings.
		r.Get("/api/v1/tasks", d.tasksH.List)
		r.Post("/api/v1/tasks", d.tasksH.Create)
		r.Patch("/api/v1/tasks/{id}", d.tasksH.Update)
		r.Get("/api/v1/users/{id}/work-settings", d.tasksH.GetWorkSettings)
		r.Patch("/api/v1/users/{id}/work-settings", d.tasksH.SetWorkSettings)

		// Phase 5: object comparison — notes (pair) + cost volumes.
		r.Get("/api/v1/comparison-notes", d.comparisonH.ListNotes)
		r.Post("/api/v1/comparison-notes", d.comparisonH.UpsertNote)
		r.Get("/api/v1/tenders/{id}/cost-volumes", d.comparisonH.ListCostVolumes)

		// Phase 5: atomic Excel cost-category import.
		r.Post("/api/v1/cost-import", d.costImportH.Import)

		// Library — WorksTab (works_library CRUD).
		r.Get("/api/v1/library/works", d.libraryH.ListWorks)
		r.With(templateAdminMW).Post("/api/v1/library/works", d.libraryH.CreateWork)
		r.With(templateAdminMW).Patch("/api/v1/library/works/{id}", d.libraryH.UpdateWork)
		r.With(templateAdminMW).Delete("/api/v1/library/works/{id}", d.libraryH.DeleteWork)
		r.Get("/api/v1/library/materials", d.libraryH.ListMaterials)
		r.With(templateAdminMW).Post("/api/v1/library/materials", d.libraryH.CreateMaterial)
		r.With(templateAdminMW).Patch("/api/v1/library/materials/{id}", d.libraryH.UpdateMaterial)
		r.With(templateAdminMW).Delete("/api/v1/library/materials/{id}", d.libraryH.DeleteMaterial)
		r.Get("/api/v1/library/folders", d.libraryH.ListFolders)
		r.With(templateAdminMW).Post("/api/v1/library/folders", d.libraryH.CreateFolder)
		r.With(templateAdminMW).Patch("/api/v1/library/folders/{id}", d.libraryH.RenameFolder)
		r.With(templateAdminMW).Delete("/api/v1/library/folders/{id}", d.libraryH.DeleteFolder)
		r.With(templateAdminMW).Post("/api/v1/library/move", d.libraryH.MoveItem)
		r.Get("/api/v1/library/templates", d.libraryH.ListTemplates)
		r.With(templateAdminMW).Post("/api/v1/library/templates", d.libraryH.CreateTemplate)
		r.With(templateAdminMW).Patch("/api/v1/library/templates/{id}", d.libraryH.UpdateTemplate)
		r.With(templateAdminMW).Delete("/api/v1/library/templates/{id}", d.libraryH.DeleteTemplate)
		r.Get("/api/v1/library/templates/{id}/items", d.libraryH.ListTemplateItems)
		r.With(templateAdminMW).Post("/api/v1/library/templates/{id}/items", d.libraryH.AddTemplateItem)
		r.With(templateAdminMW).Delete("/api/v1/library/template-items/{id}", d.libraryH.DeleteTemplateItem)

		// Phase 5: atomic redistribution save (cost_redistribution_results).
		r.Post("/api/v1/redistributions/save", d.redistributionH.Save)
		r.Get("/api/v1/redistributions", d.redistributionH.Load)
		r.Delete("/api/v1/redistributions", d.redistributionH.Clear)

		// Insurance (per-tender).
		r.Get("/api/v1/tenders/{id}/insurance", d.insuranceH.Get)
		r.Put("/api/v1/tenders/{id}/insurance", d.insuranceH.Put)

		// Снижение коммерческой стоимости на «Финансовых показателях».
		r.Get("/api/v1/tenders/{id}/fi-discounts", d.fiDiscountsH.Get)
		r.Put("/api/v1/tenders/{id}/fi-discounts", d.fiDiscountsH.Put)

		// User position filters (per-user, per-tender).
		r.Get("/api/v1/tenders/{id}/position-filters", d.positionFiltersH.List)
		r.Put("/api/v1/tenders/{id}/position-filters", d.positionFiltersH.Replace)
		r.Post("/api/v1/tenders/{id}/position-filters/append", d.positionFiltersH.Append)
		r.Delete("/api/v1/tenders/{id}/position-filters", d.positionFiltersH.Clear)

		// Notifications.
		r.Get("/api/v1/notifications", d.notificationsH.List)
		r.Post("/api/v1/notifications", d.notificationsH.Create)
		r.Delete("/api/v1/notifications", d.notificationsH.DeleteAll)

		// Tender registry + statuses + scopes.
		r.Get("/api/v1/tender-registry", d.tenderRegistryH.List)
		r.Get("/api/v1/tender-registry/next-sort-order", d.tenderRegistryH.NextSortOrder)
		r.Get("/api/v1/tender-registry/autocomplete", d.tenderRegistryH.Autocomplete)
		r.Get("/api/v1/tender-registry/tender-numbers", d.tenderRegistryH.TenderNumbers)
		r.Get("/api/v1/tender-registry/related-tenders", d.tenderRegistryH.RelatedTenders)
		r.Post("/api/v1/tender-registry", d.tenderRegistryH.Create)
		r.Patch("/api/v1/tender-registry/{id}", d.tenderRegistryH.Update)
		r.Patch("/api/v1/tender-registry/{id}/fields", d.tenderRegistryH.PatchFields)
		r.Get("/api/v1/tender-statuses", d.tenderRegistryH.ListTenderStatuses)
		r.Get("/api/v1/construction-scopes", d.tenderRegistryH.ListConstructionScopes)

		// Cost categories + detail cost categories CRUD.
		r.Get("/api/v1/cost-categories", d.costsH.ListCostCategories)
		r.Get("/api/v1/cost-categories/find", d.costsH.FindCostCategory)
		r.Post("/api/v1/cost-categories", d.costsH.CreateCostCategory)
		r.Patch("/api/v1/cost-categories/{id}", d.costsH.UpdateCostCategory)
		r.Delete("/api/v1/cost-categories/{id}", d.costsH.DeleteCostCategory)
		r.Delete("/api/v1/cost-categories", d.costsH.DeleteAllCostCategories)

		r.Get("/api/v1/detail-cost-categories", d.costsH.ListDetailCostCategories)
		r.Get("/api/v1/detail-cost-categories/max-order-num", d.costsH.NextDetailOrderNum)
		r.Post("/api/v1/detail-cost-categories", d.costsH.CreateDetailCostCategory)
		r.Patch("/api/v1/detail-cost-categories/{id}", d.costsH.UpdateDetailCostCategory)
		r.Delete("/api/v1/detail-cost-categories/{id}", d.costsH.DeleteDetailCostCategory)
		r.Delete("/api/v1/detail-cost-categories", d.costsH.DeleteAllDetailCostCategories)

		r.Get("/api/v1/locations", d.costsH.ListLocations)
		r.Get("/api/v1/units/active", d.costsH.ListActiveUnitsFull)
		r.Post("/api/v1/units/import-batch", d.costsH.UpsertImportedUnits)

		// Nomenclatures: full CRUD on units / material_names / work_names + remap.
		r.Get("/api/v1/nomenclatures/units", d.nomenclaturesH.ListUnits)
		r.Get("/api/v1/nomenclatures/units/active-list", d.nomenclaturesH.ListActiveUnitsShort)
		r.Get("/api/v1/nomenclatures/units/exists", d.nomenclaturesH.UnitExists)
		r.Post("/api/v1/nomenclatures/units", d.nomenclaturesH.CreateUnit)
		r.Patch("/api/v1/nomenclatures/units/{code}", d.nomenclaturesH.UpdateUnit)
		r.Delete("/api/v1/nomenclatures/units/{code}", d.nomenclaturesH.DeleteUnit)

		r.Get("/api/v1/nomenclatures/material-names", d.nomenclaturesH.ListMaterialNames)
		r.Get("/api/v1/nomenclatures/material-names/by-unit", d.nomenclaturesH.ListMaterialNamesByUnit)
		r.Post("/api/v1/nomenclatures/material-names", d.nomenclaturesH.CreateMaterialName)
		r.Patch("/api/v1/nomenclatures/material-names/{id}", d.nomenclaturesH.UpdateMaterialName)
		r.Delete("/api/v1/nomenclatures/material-names/{id}", d.nomenclaturesH.DeleteMaterialName)
		r.Post("/api/v1/nomenclatures/material-names/delete-batch", d.nomenclaturesH.DeleteMaterialNamesIn)

		r.Get("/api/v1/nomenclatures/work-names", d.nomenclaturesH.ListWorkNames)
		r.Get("/api/v1/nomenclatures/work-names/by-unit", d.nomenclaturesH.ListWorkNamesByUnit)
		r.Post("/api/v1/nomenclatures/work-names", d.nomenclaturesH.CreateWorkName)
		r.Patch("/api/v1/nomenclatures/work-names/{id}", d.nomenclaturesH.UpdateWorkName)
		r.Delete("/api/v1/nomenclatures/work-names/{id}", d.nomenclaturesH.DeleteWorkName)
		r.Post("/api/v1/nomenclatures/work-names/delete-batch", d.nomenclaturesH.DeleteWorkNamesIn)

		r.Post("/api/v1/nomenclatures/remap/boq-material", d.nomenclaturesH.RemapBoqMaterial)
		r.Post("/api/v1/nomenclatures/remap/library-material", d.nomenclaturesH.RemapLibraryMaterial)
		r.Post("/api/v1/nomenclatures/remap/boq-work", d.nomenclaturesH.RemapBoqWork)
		r.Post("/api/v1/nomenclatures/remap/library-work", d.nomenclaturesH.RemapLibraryWork)

		// Import log: read sessions + atomic cancel.
		r.Get("/api/v1/import-sessions", d.importLogH.ListSessions)
		r.Get("/api/v1/import-sessions/users", d.importLogH.UsersByIDs)
		r.Get("/api/v1/import-sessions/tenders", d.importLogH.TendersByIDs)
		r.Get("/api/v1/import-sessions/all-tenders", d.importLogH.AllTendersForFilter)
		r.Post("/api/v1/import-sessions/{id}/cancel", d.importLogH.Cancel)

		// Projects + agreements + monthly completion.
		r.Post("/api/v1/projects", d.projectsH.Create)
		r.Patch("/api/v1/projects/{id}", d.projectsH.Update)
		r.Delete("/api/v1/projects/{id}", d.projectsH.SoftDelete)
		r.Get("/api/v1/projects/active-tenders", d.projectsH.ListActiveTendersForSelect)
		r.Get("/api/v1/projects", d.projectsH.ListProjects)
		r.Get("/api/v1/projects/{id}", d.projectsH.GetProject)

		r.Get("/api/v1/projects/{id}/agreements", d.projectsH.ListAgreements)
		r.Get("/api/v1/project-agreements", d.projectsH.ListAllAgreements)
		r.Post("/api/v1/project-agreements", d.projectsH.CreateAgreement)
		r.Patch("/api/v1/project-agreements/{id}", d.projectsH.UpdateAgreement)
		r.Delete("/api/v1/project-agreements/{id}", d.projectsH.DeleteAgreement)

		r.Get("/api/v1/project-monthly-completion", d.projectsH.ListMonthlyCompletion)
		r.Post("/api/v1/project-monthly-completion", d.projectsH.CreateMonthlyCompletion)
		r.Patch("/api/v1/project-monthly-completion/{id}", d.projectsH.UpdateMonthlyCompletion)

		// Этап 2.5: AI-администрирование OpenRouter — настоящий server-side
		// role-гейт (RequireRoles), не только frontend page-ACL. Non-admin
		// получает 403 на все /api/v1/admin/ai/*.
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireRoles(handlers.AIAdminRoles))

			r.Get("/api/v1/admin/ai/openrouter/status", d.aiAdminH.OpenRouterStatus)
			// ИИ-разбор находок проверки данных: модель, пилотные тендеры, лимиты.
			r.Get("/api/v1/verification/ai-settings", d.verifAIH.GetSettings)
			r.Put("/api/v1/verification/ai-settings", d.verifAIH.PutSettings)
			r.Post("/api/v1/verification/ai-settings/test", d.verifAIH.PostTest)
			r.Post("/api/v1/admin/ai/openrouter/test-connection", d.aiAdminH.OpenRouterTestConnection)
			r.Get("/api/v1/admin/ai/openrouter/models", d.aiAdminH.OpenRouterModels)
			r.Post("/api/v1/admin/ai/openrouter/models/refresh", d.aiAdminH.OpenRouterModelsRefresh)
			// feature/ai-key-ui: write-only управление ключом (ключ не читается назад).
			r.Post("/api/v1/admin/ai/openrouter/key", d.aiAdminH.SetOpenRouterKey)
			r.Delete("/api/v1/admin/ai/openrouter/key", d.aiAdminH.DeleteOpenRouterKey)
			r.Get("/api/v1/admin/ai/nomenclature-settings", d.aiAdminH.GetNomenclatureSettings)
			r.Put("/api/v1/admin/ai/nomenclature-settings", d.aiAdminH.PutNomenclatureSettings)
			r.Post("/api/v1/admin/ai/nomenclature/test-model", d.aiAdminH.TestNomenclatureModel)
			r.Post("/api/v1/admin/ai/nomenclature/activate", d.aiAdminH.ActivateNomenclature)
			r.Post("/api/v1/admin/ai/nomenclature/deactivate", d.aiAdminH.DeactivateNomenclature)

			// Этап 2.6: controlled rollout (admin-only).
			r.Get("/api/v1/admin/ai/nomenclature/rollout", d.aiAdminH.RolloutState)
			r.Put("/api/v1/admin/ai/nomenclature/rollout/settings", d.aiAdminH.RolloutSettings)
			r.Post("/api/v1/admin/ai/nomenclature/rollout/transition", d.aiAdminH.RolloutTransition)
			r.Post("/api/v1/admin/ai/nomenclature/rollout/emergency-off", d.aiAdminH.RolloutEmergencyOff)
			r.Get("/api/v1/admin/ai/nomenclature/pilot-users", d.aiAdminH.PilotUsersList)
			r.Post("/api/v1/admin/ai/nomenclature/pilot-users", d.aiAdminH.PilotUsersAdd)
			r.Patch("/api/v1/admin/ai/nomenclature/pilot-users/{userId}", d.aiAdminH.PilotUsersPatch)
			r.Delete("/api/v1/admin/ai/nomenclature/pilot-users/{userId}", d.aiAdminH.PilotUsersRemove)
			r.Get("/api/v1/admin/ai/nomenclature/usage", d.aiAdminH.RolloutUsage)
			r.Get("/api/v1/admin/ai/nomenclature/evaluations", d.aiAdminH.RolloutEvaluations)
			r.Post("/api/v1/admin/ai/nomenclature/evaluate", d.aiAdminH.RolloutEvaluate)
			r.Post("/api/v1/admin/ai/nomenclature/circuit/reset", d.aiAdminH.CircuitReset)
		})
		// Capability — безопасное effective-состояние для ЛЮБОГО
		// аутентифицированного пользователя (без secrets/settings).
		// Этап 2.6: расширенная pilot-capability текущего пользователя.
		r.Get("/api/v1/ai/nomenclature-capability", d.aiAdminH.PilotCapability)

		// Admin user / role management.
		r.Get("/api/v1/admin/tenders-for-access", d.userAdminH.ListTendersForUserAccess)
		r.Get("/api/v1/admin/access-users", d.userAdminH.ListAccessUsers)
		r.Post("/api/v1/admin/tender-extensions", d.userAdminH.SetTenderExtension)
		r.Get("/api/v1/admin/users/pending", d.userAdminH.ListPending)
		r.Get("/api/v1/admin/users", d.userAdminH.ListAll)
		r.Get("/api/v1/admin/users/count-by-role", d.userAdminH.CountByRole)
		r.Post("/api/v1/admin/users/{id}/approve", d.userAdminH.Approve)
		r.Delete("/api/v1/admin/users/{id}", d.userAdminH.Delete)
		r.Patch("/api/v1/admin/users/{id}/access", d.userAdminH.SetAccess)
		r.Patch("/api/v1/admin/users/{id}", d.userAdminH.UpdateProfile)
		r.Patch("/api/v1/admin/users/by-role/{code}/allowed-pages", d.userAdminH.SyncPagesByRole)

		r.Get("/api/v1/admin/roles", d.userAdminH.ListRoles)
		r.Get("/api/v1/admin/roles/by-code", d.userAdminH.FindRoleByCode)
		r.Get("/api/v1/admin/roles/by-name", d.userAdminH.FindRoleByName)
		r.Post("/api/v1/admin/roles", d.userAdminH.CreateRole)
		r.Patch("/api/v1/admin/roles/{code}/allowed-pages", d.userAdminH.UpdateRolePages)
		r.Delete("/api/v1/admin/roles/{code}", d.userAdminH.DeleteRole)

		// Markup tactics + parameters + percentages + pricing + exclusions.
		r.Get("/api/v1/markup/tactics", d.markupH.ListTactics)
		r.Get("/api/v1/markup/tactics/global-by-name", d.markupH.FindGlobalTactic)
		r.Get("/api/v1/markup/tactics/{id}", d.markupH.GetTactic)
		r.Post("/api/v1/markup/tactics", d.markupH.CreateTactic)
		r.Patch("/api/v1/markup/tactics/{id}", d.markupH.UpdateTactic)
		r.Patch("/api/v1/markup/tactics/{id}/rename", d.markupH.RenameTactic)
		r.Delete("/api/v1/markup/tactics/{id}", d.markupH.DeleteTactic)

		r.Get("/api/v1/markup/parameters", d.markupH.ListParameters)
		r.Post("/api/v1/markup/parameters", d.markupH.CreateParameter)
		r.Patch("/api/v1/markup/parameters/{id}", d.markupH.UpdateParameter)
		r.Delete("/api/v1/markup/parameters/{id}", d.markupH.DeleteParameter)
		r.Patch("/api/v1/markup/parameters/{id}/order-num", d.markupH.SetParameterOrderNum)

		r.Get("/api/v1/tenders/{id}/markup/tactic-id", d.markupH.GetTenderTacticID)
		r.Put("/api/v1/tenders/{id}/markup/tactic-id", d.markupH.SetTenderTacticID)

		r.Get("/api/v1/tenders/{id}/markup/percentages", d.markupH.ListTenderMarkupPercentages)
		r.Put("/api/v1/tenders/{id}/markup/percentages", d.markupH.ReplaceTenderMarkupPercentages)

		r.Get("/api/v1/tenders/{id}/pricing-distribution", d.markupH.GetPricingDistribution)
		r.Post("/api/v1/markup/pricing-distribution", d.markupH.UpsertPricingDistribution)

		r.Get("/api/v1/tenders/{id}/markup/exclusions", d.markupH.ListSubcontractExclusions)
		r.Post("/api/v1/markup/exclusions", d.markupH.InsertSubcontractExclusion)
		r.Post("/api/v1/markup/exclusions/batch", d.markupH.InsertSubcontractExclusionsBatch)
		r.Delete("/api/v1/markup/exclusions", d.markupH.DeleteSubcontractExclusion)
		r.Delete("/api/v1/markup/exclusions/batch", d.markupH.DeleteSubcontractExclusionsBatch)

		// Financial Indicators heavy aggregate reads.
		r.Get("/api/v1/tenders/{id}", d.fiH.GetTenderByID)
		r.Get("/api/v1/tenders/{id}/boq-items-flat", d.fiH.ListBoqItemsFlat)
	})

	// Phase 4 — WebSocket endpoint. Registered OUTSIDE the authMW group because
	// the WS handler performs its own JWT verification via the ?token= query
	// parameter (the browser WebSocket API cannot set the Authorization header).
	r.Get("/api/v1/ws", d.wsH.Serve)

	return r
}

// ---------------------------------------------------------------------------
// CORS middleware
// ---------------------------------------------------------------------------

// corsMiddleware returns a minimal CORS handler that allows the configured
// origins. It does not depend on any external library.
func corsMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	originSet := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		originSet[strings.TrimRight(o, "/")] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				if _, ok := originSet[origin]; ok {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Vary", "Origin")
					w.Header().Set("Access-Control-Allow-Credentials", "true")
					w.Header().Set(
						"Access-Control-Allow-Headers",
						"Authorization, Content-Type, X-Request-ID, If-Match, If-None-Match, Cache-Control, X-API-Key, MCP-Protocol-Version, MCP-Session-Id, MCP-Method, MCP-Name",
					)
					// Preflight cache: Chrome default is only 5s, so every
					// realtime refetch would otherwise re-issue OPTIONS.
					w.Header().Set("Access-Control-Max-Age", "600")
					w.Header().Set(
						"Access-Control-Allow-Methods",
						"GET, POST, PUT, PATCH, DELETE, OPTIONS",
					)
					w.Header().Set(
						"Access-Control-Expose-Headers",
						"ETag, Location, MCP-Session-Id, WWW-Authenticate",
					)
				}
			}

			// Handle pre-flight.
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
