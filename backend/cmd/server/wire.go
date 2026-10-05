package main

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/su10/hubtender/backend/internal/ai/keycrypt"
	ainom "github.com/su10/hubtender/backend/internal/ai/nomenclature"
	"github.com/su10/hubtender/backend/internal/ai/openrouter"
	"github.com/su10/hubtender/backend/internal/auth"
	"github.com/su10/hubtender/backend/internal/cache"
	"github.com/su10/hubtender/backend/internal/cbr"
	"github.com/su10/hubtender/backend/internal/config"
	"github.com/su10/hubtender/backend/internal/handlers"
	"github.com/su10/hubtender/backend/internal/mcpauth"
	"github.com/su10/hubtender/backend/internal/mcpserver"
	"github.com/su10/hubtender/backend/internal/middleware"
	"github.com/su10/hubtender/backend/internal/notify/telegram"
	"github.com/su10/hubtender/backend/internal/realtime"
	"github.com/su10/hubtender/backend/internal/repository"
	"github.com/su10/hubtender/backend/internal/services"
)

// deps carries every handler wired by buildDeps plus the recalc queue
// (needed by the graceful-shutdown sequence in main.go).
type deps struct {
	recalcQueue    *services.RecalcQueue
	recalcRecovery *services.FinancialCalculationRecoveryService
	verifQueue     *services.RecalcQueue
	verifRetention *services.VerificationRetentionService
	telegramBot    *services.TelegramBot
	telegramH      *handlers.TelegramHandler
	aiTriageQueue  *services.RecalcQueue
	verifAIH       *handlers.VerificationAIHandler
	recalcHealthH  *handlers.RecalcHealthHandler

	healthH        *handlers.HealthHandler
	meH            *handlers.MeHandler
	refH           *handlers.ReferenceHandler
	tenderH        *handlers.TenderHandler
	tenderWH       *handlers.TenderWriteHandler
	tenderBriefH   *handlers.TenderBriefHandler
	cbrH           *handlers.CBRHandler
	positionH      *handlers.PositionHandler
	positionWH     *handlers.PositionWriteHandler
	positionCostsH *handlers.PositionCostsHandler
	boqH           *handlers.BoqHandler
	boqWH          *handlers.BoqWriteHandler
	bulkBoqH       *handlers.BulkBoqHandler
	importBoqH     *handlers.ImportBoqHandler
	timelineH      *handlers.TimelineHandler
	userRegH       *handlers.UserRegisterHandler
	subcontractH   *handlers.SubcontractHandler
	transferH      *handlers.TenderTransferHandler
	cloneH         *handlers.TenderCloneHandler
	archiveH       *handlers.ArchiveHandler
	apiAccessH     *handlers.ApiAccessHandler
	// apiAccessSvc нужен маршрутам напрямую: он же проверяет ключи и пишет журнал.
	apiAccessSvc *services.ApiAccessService
	// positionSvc/boqSvc нужны гейту машинного доступа: резолвить тендер по
	// id позиции/строки для маршрутов без {id} тендера в URL.
	positionSvc       *services.PositionService
	boqSvc            *services.BoqService
	tenderNotesH      *handlers.TenderNotesHandler
	boqAuditRollbackH *handlers.BoqAuditRollbackHandler
	tasksH            *handlers.TasksHandler
	comparisonH       *handlers.ComparisonHandler
	costImportH       *handlers.CostImportHandler
	libraryH          *handlers.LibraryHandler
	redistributionH   *handlers.RedistributionHandler
	insuranceH        *handlers.InsuranceHandler
	fiDiscountsH      *handlers.FIDiscountsHandler
	positionFiltersH  *handlers.PositionFiltersHandler
	notificationsH    *handlers.NotificationsHandler
	tenderRegistryH   *handlers.TenderRegistryHandler
	registryBotH      *handlers.TenderRegistryBotHandler
	costsH            *handlers.CostsHandler
	nomenclaturesH    *handlers.NomenclaturesHandler
	importLogH        *handlers.ImportLogHandler
	projectsH         *handlers.ProjectsHandler
	userAdminH        *handlers.UserAdminHandler
	markupH           *handlers.MarkupHandler
	fiH               *handlers.FIHandler
	qualityAnalyticsH *handlers.QualityAnalyticsHandler
	priceBenchmarkH   *handlers.PriceBenchmarkHandler
	priceSourceH      *handlers.PriceSourceHandler
	actionPlanH       *handlers.ActionPlanHandler
	changeImpactH     *handlers.ChangeImpactHandler
	reviewPackH       *handlers.ReviewPackHandler
	smartImportH      *handlers.SmartImportHandler
	importMemoryH     *handlers.ImportMemoryHandler
	ccvH              *handlers.ConstructionCostVolumesHandler
	aiAdminH          *handlers.AIAdminHandler
	aiHealthH         *handlers.AIHealthHandler
	wsH               *handlers.WsHandler
	qualityH          *handlers.QualityHandler
	verifSectionsH    *handlers.VerificationSectionsHandler
	costBenchmarkH    *handlers.CostBenchmarkHandler
	pricingH          *handlers.PricingHandler
	mcpOAuthH         *mcpauth.Handler
	mcpHTTP           http.Handler
}

// buildDeps wires repositories → cache → services → handlers. Extracted from
// main() verbatim (section 8) to keep the entrypoint under the 600-line limit.
func buildDeps(
	rootCtx context.Context,
	pool *pgxpool.Pool,
	hub *realtime.Hub,
	verifyCfg middleware.VerifyConfig,
	cfg *config.Config,
	logger zerolog.Logger,
	mcpIssuer *auth.Issuer,
) *deps {
	inMemCache := cache.New()
	cbrClient := cbr.NewClient(inMemCache, cfg.CBRBaseURL)

	userRepo := repository.NewUserRepo(pool)
	refRepo := repository.NewReferenceRepo(pool)
	tenderRepo := repository.NewTenderRepo(pool)
	positionRepo := repository.NewPositionRepo(pool)
	positionCostsRepo := repository.NewPositionCostsRepo(pool)
	qualityRepo := repository.NewQualityRepo(pool)
	verifSectionsRepo := repository.NewVerificationSectionsRepo(pool)
	costBenchmarkRepo := repository.NewCostBenchmarkRepo(pool)
	boqRepo := repository.NewBoqRepo(pool)
	bulkBoqRepo := repository.NewBulkBoqRepo(pool)
	importBoqRepo := repository.NewImportRepo(pool)
	timelineRepo := repository.NewTimelineRepo(pool)
	subcontractRepo := repository.NewSubcontractRepo(pool)
	transferRepo := repository.NewTransferRepo(pool)
	cloneRepo := repository.NewCloneRepo(pool)
	tenderNotesRepo := repository.NewTenderNotesRepo(pool)
	boqAuditRollbackRepo := repository.NewBoqAuditRollbackRepo(pool)
	tasksRepo := repository.NewTasksRepo(pool)
	comparisonRepo := repository.NewComparisonRepo(pool)
	costImportRepo := repository.NewCostImportRepo(pool)
	libraryRepo := repository.NewLibraryRepo(pool)
	redistributionRepo := repository.NewRedistributionRepo(pool)
	insuranceRepo := repository.NewInsuranceRepo(pool)
	fiDiscountsRepo := repository.NewFIDiscountsRepo(pool)
	positionFiltersRepo := repository.NewPositionFiltersRepo(pool)
	notificationsRepo := repository.NewNotificationsRepo(pool)
	tenderRegistryRepo := repository.NewTenderRegistryRepo(pool)
	costsRepo := repository.NewCostsRepo(pool)
	nomenclaturesRepo := repository.NewNomenclaturesRepo(pool)
	importLogRepo := repository.NewImportLogRepo(pool)
	projectsRepo := repository.NewProjectsRepo(pool)
	userAdminRepo := repository.NewUserAdminRepo(pool)
	markupRepo := repository.NewMarkupRepo(pool)
	fiRepo := repository.NewFIRepo(pool)
	ccvRepo := repository.NewConstructionCostVolumesRepo(pool)
	qualityAnalyticsRepo := repository.NewQualityAnalyticsRepo(pool)
	priceBenchmarkRepo := repository.NewPriceBenchmarkRepo(pool)
	priceSourceRepo := repository.NewPriceSourceRepo(pool)
	actionPlanRepo := repository.NewActionPlanRepo(pool)
	changeImpactRepo := repository.NewChangeImpactRepo(pool)
	reviewPackRepo := repository.NewReviewPackRepo(pool)
	importAnalysisRepo := repository.NewImportAnalysisRepo(pool)
	importMemoryRepo := repository.NewImportMemoryRepo(pool)
	archiveRepo := repository.NewArchiveRepo(pool)
	apiAccessRepo := repository.NewApiAccessRepo(pool)
	pricingRepo := repository.NewPricingRepo(pool)
	mcpOAuthRepo := mcpauth.NewRepository(pool)

	// Commercial-cost auto-recalc — replaces the manual «Пересчитать» button.
	// Mutation services Enqueue(tenderID) after changing a pricing input (BOQ
	// items, markup config, currency rates); the queue debounces per tender and
	// runs an authoritative server-side recalc (calc.CalculateBoqItemCost) that
	// materializes boq_items commercial costs + tenders.cached_grand_total.
	// The commercial calculation itself lives in repository.CommercialRepo so the
	// SAME implementation can run inside copy / version-transfer transactions.
	// 0-F2: the recalc runs as ONE REPEATABLE READ tx (advisory lock + revision
	// CAS) directly on the pool; a stale_requeue outcome re-enqueues the tender.
	recalcSvc := services.NewCommercialRecalcService(pool, inMemCache)
	recalcQueue := services.NewRecalcQueue(rootCtx, recalcSvc, 1500*time.Millisecond, 4, logger)
	recalcSvc.WithRequeue(recalcQueue)
	// Этап 2.4 (§2): recovery-механизм; env-переопределения конфигурации.
	recoveryCfg := services.DefaultRecoveryConfig()
	if v := os.Getenv("RECALC_RECOVERY_ENABLED"); v == "false" {
		recoveryCfg.Enabled = false
	}
	if v, err := time.ParseDuration(os.Getenv("RECALC_RECOVERY_SCAN_INTERVAL")); err == nil && v > 0 {
		recoveryCfg.ScanInterval = v
	}
	if v, err := time.ParseDuration(os.Getenv("RECALC_RECOVERY_CALCULATING_TIMEOUT")); err == nil && v > 0 {
		recoveryCfg.CalculatingTimeout = v
	}
	if v, err := strconv.Atoi(os.Getenv("RECALC_RECOVERY_BATCH_SIZE")); err == nil && v > 0 {
		recoveryCfg.BatchSize = v
	}
	recalcRecovery := services.NewFinancialCalculationRecoveryService(pool, recalcQueue, recoveryCfg, logger)

	userSvc := services.NewUserService(userRepo, inMemCache)
	refSvc := services.NewReferenceService(refRepo, inMemCache)
	tenderSvc := services.NewTenderService(tenderRepo, inMemCache).WithRecalcQueue(recalcQueue)
	positionSvc := services.NewPositionService(positionRepo, inMemCache)
	positionCostsSvc := services.NewPositionCostsService(positionCostsRepo, inMemCache)
	qualitySvc := services.NewQualityService(qualityRepo, inMemCache)
	verifSectionsSvc := services.NewVerificationSectionsService(verifSectionsRepo)
	costBenchmarkSvc := services.NewCostBenchmarkService(costBenchmarkRepo, inMemCache)
	// Кэш находок «Проверки данных» держится 10 минут и до сих пор сбрасывался
	// только при сохранении вердикта. Привязываем его к очереди пересчёта: она
	// уже дёргается со всех финансовых путей записи.
	recalcQueue.SetInvalidator(qualitySvc)
	// Фоновый прогон проверки данных — после каждого успешного пересчёта. Дебаунс
	// минута: серия правок инженера схлопывается в один прогон (~5 с на тендер).
	verifQueue := services.NewRecalcQueue(rootCtx, services.NewVerificationRunner(qualitySvc), time.Minute, 1, logger)
	verifQueue.SetLabel("verification run")
	if os.Getenv("VERIFICATION_AUTO_RUN_ENABLED") != "false" {
		recalcQueue.SetAfterSuccess(verifQueue)
	}
	verifRetentionCfg := services.DefaultVerificationRetentionConfig()
	if os.Getenv("VERIFICATION_RETENTION_ENABLED") == "false" {
		verifRetentionCfg.Enabled = false
	}
	verifRetention := services.NewVerificationRetentionService(qualityRepo, verifRetentionCfg, logger)
	// Telegram: рассылка замечаний проверки по кнопке. Без TELEGRAM_BOT_TOKEN и
	// TELEGRAM_BOT_USERNAME бот не стартует, эндпоинты отвечают «не настроен».
	telegramCfg := telegram.ConfigFromEnv()
	telegramRepo := repository.NewTelegramRepo(pool)
	dispatchRepo := repository.NewVerificationDispatchRepo(pool)
	var telegramBot *services.TelegramBot
	if telegramCfg.Enabled() {
		tgClient, tgErr := telegram.NewClient(telegramCfg)
		if tgErr != nil {
			logger.Error().Err(tgErr).Msg("telegram bot disabled")
			telegramCfg.Token = ""
		} else {
			telegramBot = services.NewTelegramBot(tgClient, telegramRepo, dispatchRepo, qualitySvc, telegramCfg, logger)
		}
	}
	telegramH := handlers.NewTelegramHandler(
		services.NewTelegramLinkService(telegramRepo, telegramCfg),
		services.NewVerificationDispatchService(dispatchRepo, telegramCfg),
	)
	boqSvc := services.NewBoqService(boqRepo, inMemCache).WithRecalcQueue(recalcQueue)
	bulkBoqSvc := services.NewBulkBoqService(bulkBoqRepo, inMemCache)
	importBoqSvc := services.NewImportBoqService(importBoqRepo, inMemCache).WithRecalcQueue(recalcQueue)
	timelineSvc := services.NewTimelineService(timelineRepo)
	subcontractSvc := services.NewSubcontractService(subcontractRepo, inMemCache).WithRecalcQueue(recalcQueue)
	transferSvc := services.NewTransferService(transferRepo, inMemCache)
	cloneSvc := services.NewCloneService(cloneRepo, inMemCache)
	tenderNotesSvc := services.NewTenderNotesService(tenderNotesRepo)
	boqAuditRollbackSvc := services.NewBoqAuditRollbackService(boqAuditRollbackRepo, inMemCache)
	tasksSvc := services.NewTasksService(tasksRepo)
	comparisonSvc := services.NewComparisonService(comparisonRepo)
	costImportSvc := services.NewCostImportService(costImportRepo, inMemCache)
	librarySvc := services.NewLibraryService(libraryRepo, inMemCache)
	redistributionSvc := services.NewRedistributionService(redistributionRepo, inMemCache)
	insuranceSvc := services.NewInsuranceService(insuranceRepo, inMemCache)
	fiDiscountsSvc := services.NewFIDiscountsService(fiDiscountsRepo, inMemCache)
	positionFiltersSvc := services.NewPositionFiltersService(positionFiltersRepo)
	notificationsSvc := services.NewNotificationsService(notificationsRepo)
	tenderRegistrySvc := services.NewTenderRegistryService(tenderRegistryRepo)
	costsSvc := services.NewCostsService(costsRepo, inMemCache)
	nomenclaturesSvc := services.NewNomenclaturesService(nomenclaturesRepo, inMemCache)
	importLogSvc := services.NewImportLogService(importLogRepo, inMemCache).WithRecalcQueue(recalcQueue)
	projectsSvc := services.NewProjectsService(projectsRepo)
	userAdminSvc := services.NewUserAdminService(userAdminRepo, inMemCache)
	markupSvc := services.NewMarkupService(markupRepo, inMemCache).WithRecalcQueue(recalcQueue)
	fiSvc := services.NewFIService(fiRepo)
	qualityAnalyticsSvc := services.NewQualityAnalyticsService(qualityAnalyticsRepo)
	priceBenchmarkSvc := services.NewPriceBenchmarkService(priceBenchmarkRepo)
	priceSourceSvc := services.NewPriceSourceService(priceSourceRepo)
	actionPlanSvc := services.NewActionPlanService(actionPlanRepo)
	changeImpactSvc := services.NewChangeImpactService(changeImpactRepo)
	reviewPackSvc := services.NewReviewPackService(reviewPackRepo)
	archiveSvc := services.NewArchiveService(archiveRepo, inMemCache)
	apiAccessSvc := services.NewApiAccessService(rootCtx, apiAccessRepo)
	pricingSvc := services.NewPricingService(pricingRepo, userRepo, libraryRepo, mcpOAuthRepo, services.PricingFeatures{
		WriteEnabled: cfg.MCPWriteEnabled, TemplateWriteEnabled: cfg.MCPTemplateWriteEnabled,
	}).WithRecalcQueue(recalcQueue).WithCache(inMemCache)
	mcpOAuthSvc := mcpauth.NewService(mcpOAuthRepo, userRepo, mcpauth.ServiceConfig{
		Issuer: mcpIssuer, CodeTTL: cfg.MCPAuthorizationCodeTTL,
		DCR: cfg.MCPDCREnabled, CIMDAllowedHosts: cfg.MCPCIMDAllowedHosts,
	})
	// Этап 2.2: AI-подбор номенклатуры. Одобренного provider в проекте нет —
	// по умолчанию DisabledProvider; config-contract (владелец проекта):
	//   AI_NOMENCLATURE_ENABLED, AI_NOMENCLATURE_PROVIDER, AI_NOMENCLATURE_MODEL,
	//   AI_NOMENCLATURE_TIMEOUT_SECONDS. Реальный сетевой adapter добавляется
	//   отдельным подтверждённым решением (docs/AI_NOMENCLATURE_MATCHING.md).
	aiNomCfg := ainom.Config{
		Enabled:       os.Getenv("AI_NOMENCLATURE_ENABLED") == "true",
		Provider:      os.Getenv("AI_NOMENCLATURE_PROVIDER"),
		Model:         os.Getenv("AI_NOMENCLATURE_MODEL"),
		PromptVersion: ainom.PromptVersion,
	}
	var aiReranker ainom.NomenclatureReranker = ainom.DisabledProvider{}
	if aiNomCfg.Enabled {
		// Сетевой adapter не реализован до выбора провайдера владельцем:
		// enabled без adapter честно остаётся disabled-поведением.
		aiNomCfg.Enabled = false
	}
	smartImportSvc := services.NewSmartImportService(importAnalysisRepo, importBoqSvc).
		WithNomenclatureAI(importAnalysisRepo, aiReranker, aiNomCfg).
		WithImportMemory(importMemoryRepo)
	importMemorySvc := services.NewImportMemoryService(importMemoryRepo)
	ccvSvc := services.NewConstructionCostVolumesService(ccvRepo)

	// Этап 2.5: OpenRouter AI administration. API key — ТОЛЬКО server env
	// (OPENROUTER_API_KEY); в БД/frontend/логи не попадает. Пустой ключ =
	// not_configured, приложение работает. Base URL: в production — только
	// allowlist официальных URL; кастомный base (fake-server интеграционные
	// тесты) разрешён вне production. Rollout off: user-путь Smart Import
	// выше остаётся на DisabledProvider — единственный live-вызов OpenRouter
	// в 2.5 — админский synthetic model test.
	orBase := cfg.OpenRouterAPIBase
	if orBase != "" && !openrouter.AllowedBaseURLs[orBase] {
		if cfg.AppEnv == "production" {
			logger.Warn().
				Str("operation", "openrouter_config").
				Msg("OPENROUTER_API_BASE не входит в allowlist официальных base URL — игнорируется, используется официальный base")
			orBase = ""
		} else {
			logger.Warn().
				Str("operation", "openrouter_config").
				Str("app_env", cfg.AppEnv).
				Msg("OPENROUTER_API_BASE вне allowlist разрешён только вне production (fake-server тесты)")
		}
	}
	// feature/ai-key-ui: UI-ключ хранится в БД шифротекстом (AES-GCM от
	// JWT-private-key) и имеет приоритет над env; без JWT-материала фича
	// честно отключена (действует только env-ключ).
	aiSettingsRepo := repository.NewAISettingsRepo(pool)
	var aiKeyCipher *keycrypt.Cipher
	if master, kerr := loadJWTKeyMaterial(cfg); kerr != nil {
		logger.Warn().Err(kerr).Msg("ai-key-ui: JWT-материал недоступен — UI-управление ключом отключено")
	} else if c, cerr := keycrypt.New(master); cerr != nil {
		logger.Warn().Err(cerr).Msg("ai-key-ui: keycrypt init failed — UI-управление ключом отключено")
	} else {
		aiKeyCipher = c
	}
	aiKeyResolver := services.NewAIKeyResolver(aiSettingsRepo, aiKeyCipher)

	// Транспорт LLM: прямой OpenRouter либо собственный прокси (там, где у
	// хоста нет исходящего доступа к openrouter.ai). Значение уже провалидировано
	// в config.Load — опечатка валит старт, а не откатывается молча.
	llmTransport, _ := openrouter.ParseTransport(cfg.AIProviderMode)
	orCfg := openrouter.Config{
		APIKey:      cfg.OpenRouterAPIKey,
		BaseURL:     orBase,
		HTTPReferer: cfg.OpenRouterHTTPReferer,
		AppTitle:    cfg.OpenRouterAppTitle,
		Timeout:     time.Duration(cfg.OpenRouterTimeoutSeconds) * time.Second,
		Transport:   llmTransport,
	}
	if llmTransport == openrouter.TransportProxyLLM {
		orCfg.APIKey = cfg.ProxyLLMToken
		orCfg.BaseURL = cfg.ProxyLLMBaseURL
		orCfg.Timeout = time.Duration(cfg.ProxyLLMTimeoutSeconds) * time.Second
		// Прокси вырезает объект provider ⇒ ZDR, data_collection=deny и
		// require_parameters на стороне провайдера НЕ применяются. Работать в
		// таком режиме без явного подтверждения оператора нельзя: это тихая
		// потеря privacy-гарантии, записанной в настройках фичи.
		//
		// Не валим процесс — портал не должен падать из-за AI-фичи. Гасим
		// транспорт тем же способом, что и пустой ключ: not_configured,
		// сетевых вызовов нет.
		if !cfg.ProxyLLMAckNoProviderPolicy {
			orCfg.APIKey = ""
			logger.Error().
				Str("operation", "openrouter_config").
				Msg("proxy_llm: не задан PROXY_LLM_ACK_NO_PROVIDER_POLICY — прокси вырезает provider, " +
					"privacy-политика (ZDR, data_collection=deny, require_parameters, запрет fallback) " +
					"НЕ применяется; транспорт отключён до явного подтверждения оператора")
		}
	}
	orOpts := []openrouter.Option{openrouter.WithKeySource(aiKeyResolver.Current)}
	// У прод-хоста нет выхода к openrouter.ai: запросы к модели можно пустить через
	// HTTP(S)-прокси (CONNECT; TLS до провайдера не разрывается). Только для клиента
	// модели — остальной исходящий трафик процесса прокси не трогает.
	if raw := strings.TrimSpace(os.Getenv("AI_HTTPS_PROXY_URL")); raw != "" {
		if pu, perr := url.Parse(raw); perr != nil || pu.Host == "" {
			logger.Error().Msg("AI_HTTPS_PROXY_URL некорректен — прокси для модели не используется")
		} else {
			tr := http.DefaultTransport.(*http.Transport).Clone()
			tr.Proxy = http.ProxyURL(pu)
			orOpts = append(orOpts, openrouter.WithHTTPClient(&http.Client{Transport: tr, Timeout: orCfg.Timeout}))
		}
	}
	orClient, orErr := openrouter.New(orCfg, orOpts...)
	if orErr != nil {
		// Невалидный base — не валим приложение: клиент без конфигурации.
		logger.Warn().Err(orErr).Msg("openrouter client init failed; AI administration будет not_configured")
		orClient, _ = openrouter.New(openrouter.Config{}, openrouter.WithKeySource(aiKeyResolver.Current))
	}
	// Источник каталога. У прокси нет GET /models/user — вместо сетевого вызова
	// подставляется синтетический каталог из одной псевдо-модели (вариант A:
	// модель выбирает прокси). Это сохраняет radio-выбор из server-каталога и
	// валидацию model ID через FindModel вместо ветки «здесь не проверяем».
	// ИИ-разбор находок проверки данных: после фонового прогона правил сервер отдаёт
	// модели новые находки с контекстом позиции. Оценка — подсказка проверяющему.
	verifAIRepo := repository.NewVerificationAIRepo(pool)
	verifAISvc := services.NewVerificationAIService(rootCtx, verifAIRepo, orClient, logger)
	aiTriageQueue := services.NewRecalcQueue(rootCtx, verifAISvc, 10*time.Second, 1, logger)
	aiTriageQueue.SetLabel("verification ai triage")
	verifQueue.SetAfterSuccess(aiTriageQueue)
	verifAIH := handlers.NewVerificationAIHandler(verifAISvc)

	var catalogSource openrouter.ModelsLister = orClient
	if llmTransport == openrouter.TransportProxyLLM {
		catalogSource = openrouter.ProxyCatalogLister{}
	}
	orCatalog := openrouter.NewCatalogCache(catalogSource, openrouter.CatalogTTL)
	aiAdminSvc := services.NewAIAdminService(orClient, orCatalog, aiSettingsRepo).
		WithLiveTestFlag(os.Getenv("OPENROUTER_LIVE_TEST") == "true").
		WithKeyManagement(aiSettingsRepo, aiKeyCipher, aiKeyResolver, strings.TrimSpace(cfg.OpenRouterAPIKey) != "")
	// Этап 2.6: live-gateway пилота. Rollout по умолчанию off (БД);
	// non-pilot и любые exact/alias/execute пути провайдера не вызывают.
	smartImportSvc.WithAIRolloutGateway(aiAdminSvc)
	// Maintenance: reservation recovery + retention cleanup (§8/§21).
	aiMaintCfg := services.DefaultAIMaintenanceConfig()
	if v := os.Getenv("AI_ROLLOUT_MAINTENANCE_ENABLED"); v == "false" {
		aiMaintCfg.Enabled = false
	}
	if v, err := time.ParseDuration(os.Getenv("AI_ROLLOUT_MAINTENANCE_SCAN_INTERVAL")); err == nil && v > 0 {
		aiMaintCfg.ScanInterval = v
	}
	aiMaintenance := services.NewAIRolloutMaintenanceService(aiSettingsRepo, aiMaintCfg, logger)
	aiMaintenance.Start(rootCtx)
	aiHealthH := handlers.NewAIHealthHandler(aiAdminSvc, aiMaintenance)
	// Startup redacted config summary (§22): без секретов.
	baseLabel := "official"
	if orBase != "" {
		baseLabel = "custom-dev"
	}
	providerPolicyEnforced := llmTransport != openrouter.TransportProxyLLM
	if llmTransport == openrouter.TransportProxyLLM {
		baseLabel = "proxy_llm"
		if cfg.ProxyLLMTimeoutSeconds <= 190 {
			logger.Warn().
				Str("operation", "openrouter_config").
				Int("timeout_seconds", cfg.ProxyLLMTimeoutSeconds).
				Msg("proxy_llm: таймаут не превышает серверный дедлайн прокси (~190 с) — его 504 deadline_exceeded недостижим")
		}
		if orClient.Configured() {
			logger.Warn().
				Str("operation", "openrouter_config").
				Str("provider_policy_version", openrouter.ProviderPolicyVersionProxy).
				Msg("proxy_llm: provider вырезается прокси — ZDR, data_collection=deny, require_parameters " +
					"и запрет provider-fallback НЕ применяются; приватность делегирована оператору прокси")
		}
	}
	logger.Info().
		Str("operation", "openrouter_config").
		Bool("api_key_configured", orClient.Configured()).
		Str("api_base", baseLabel).
		Str("llm_transport", openrouter.String(llmTransport)).
		Bool("provider_policy_enforced", providerPolicyEnforced).
		Str("prompt_version", ainom.PromptVersion).
		Str("adapter_version", openrouter.AdapterVersion).
		Str("rollout_status", services.AIRolloutStatus).
		Msg("openrouter AI administration wired")

	return &deps{
		recalcQueue:    recalcQueue,
		recalcRecovery: recalcRecovery,
		verifQueue:     verifQueue,
		verifRetention: verifRetention,
		telegramBot:    telegramBot,
		telegramH:      telegramH,
		aiTriageQueue:  aiTriageQueue,
		verifAIH:       verifAIH,
		recalcHealthH:  handlers.NewRecalcHealthHandler(recalcRecovery),

		healthH:           handlers.NewHealthHandler(pool, inMemCache),
		meH:               handlers.NewMeHandler(userSvc),
		refH:              handlers.NewReferenceHandler(refSvc),
		tenderH:           handlers.NewTenderHandler(tenderSvc),
		tenderWH:          handlers.NewTenderWriteHandler(tenderSvc),
		tenderBriefH:      handlers.NewTenderBriefHandler(tenderSvc),
		cbrH:              handlers.NewCBRHandler(cbrClient),
		positionH:         handlers.NewPositionHandler(positionSvc),
		positionWH:        handlers.NewPositionWriteHandler(positionSvc),
		positionCostsH:    handlers.NewPositionCostsHandler(positionCostsSvc),
		qualityH:          handlers.NewQualityHandler(qualitySvc),
		verifSectionsH:    handlers.NewVerificationSectionsHandler(verifSectionsSvc),
		costBenchmarkH:    handlers.NewCostBenchmarkHandler(costBenchmarkSvc),
		pricingH:          handlers.NewPricingHandler(pricingSvc),
		mcpOAuthH:         mcpauth.NewHandler(mcpOAuthSvc, mcpauth.HandlerConfig{PublicBaseURL: cfg.AppBaseURL, DCR: cfg.MCPDCREnabled}),
		mcpHTTP:           mcpserver.NewHTTPHandler(pricingSvc, mcpserver.Config{MaxRequestBodyBytes: cfg.MCPMaxRequestBodyBytes, Logger: logger}),
		boqH:              handlers.NewBoqHandler(boqSvc),
		boqWH:             handlers.NewBoqWriteHandler(boqSvc),
		bulkBoqH:          handlers.NewBulkBoqHandler(bulkBoqSvc),
		importBoqH:        handlers.NewImportBoqHandler(importBoqSvc),
		timelineH:         handlers.NewTimelineHandler(timelineSvc),
		userRegH:          handlers.NewUserRegisterHandler(userSvc),
		subcontractH:      handlers.NewSubcontractHandler(subcontractSvc),
		transferH:         handlers.NewTenderTransferHandler(transferSvc),
		cloneH:            handlers.NewTenderCloneHandler(cloneSvc),
		archiveH:          handlers.NewArchiveHandler(archiveSvc, apiAccessSvc),
		apiAccessH:        handlers.NewApiAccessHandler(apiAccessSvc),
		apiAccessSvc:      apiAccessSvc,
		positionSvc:       positionSvc,
		boqSvc:            boqSvc,
		tenderNotesH:      handlers.NewTenderNotesHandler(tenderNotesSvc),
		boqAuditRollbackH: handlers.NewBoqAuditRollbackHandler(boqAuditRollbackSvc),
		tasksH:            handlers.NewTasksHandler(tasksSvc),
		comparisonH:       handlers.NewComparisonHandler(comparisonSvc),
		costImportH:       handlers.NewCostImportHandler(costImportSvc),
		libraryH:          handlers.NewLibraryHandler(librarySvc),
		redistributionH:   handlers.NewRedistributionHandler(redistributionSvc),
		insuranceH:        handlers.NewInsuranceHandler(insuranceSvc),
		fiDiscountsH:      handlers.NewFIDiscountsHandler(fiDiscountsSvc),
		positionFiltersH:  handlers.NewPositionFiltersHandler(positionFiltersSvc),
		notificationsH:    handlers.NewNotificationsHandler(notificationsSvc),
		tenderRegistryH:   handlers.NewTenderRegistryHandler(tenderRegistrySvc),
		registryBotH: handlers.NewTenderRegistryBotHandler(services.NewTenderRegistryBotService(
			repository.NewTenderRegistryBotRepo(pool), cfg.AppBaseURL)),
		costsH:            handlers.NewCostsHandler(costsSvc),
		nomenclaturesH:    handlers.NewNomenclaturesHandler(nomenclaturesSvc),
		importLogH:        handlers.NewImportLogHandler(importLogSvc),
		projectsH:         handlers.NewProjectsHandler(projectsSvc),
		userAdminH:        handlers.NewUserAdminHandler(userAdminSvc),
		markupH:           handlers.NewMarkupHandler(markupSvc),
		fiH:               handlers.NewFIHandler(fiSvc),
		qualityAnalyticsH: handlers.NewQualityAnalyticsHandler(qualityAnalyticsSvc),
		priceBenchmarkH:   handlers.NewPriceBenchmarkHandler(priceBenchmarkSvc),
		priceSourceH:      handlers.NewPriceSourceHandler(priceSourceSvc),
		actionPlanH:       handlers.NewActionPlanHandler(actionPlanSvc),
		changeImpactH:     handlers.NewChangeImpactHandler(changeImpactSvc),
		reviewPackH:       handlers.NewReviewPackHandler(reviewPackSvc),
		smartImportH:      handlers.NewSmartImportHandler(smartImportSvc),
		importMemoryH:     handlers.NewImportMemoryHandler(importMemorySvc),
		ccvH:              handlers.NewConstructionCostVolumesHandler(ccvSvc),
		aiAdminH:          handlers.NewAIAdminHandler(aiAdminSvc),
		aiHealthH:         aiHealthH,
		wsH:               handlers.NewWsHandler(hub, verifyCfg, logger),
	}
}
