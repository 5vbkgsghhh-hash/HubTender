package config

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/viper"

	"github.com/su10/hubtender/backend/internal/ai/openrouter"
)

// Config holds all runtime configuration for the BFF server.
type Config struct {
	// DatabaseURL is the full pgx connection string, e.g.
	// postgres://user:pass@host:5432/db?sslmode=require
	DatabaseURL string

	// App-issued JWT configuration. All fields are required at startup.
	AppJWTIssuer         string
	AppJWTAudience       string
	AppJWTKeyID          string
	AppJWTPrivateKeyPath string
	AppJWTPrivateKeyB64  string
	AppAccessTokenTTL    time.Duration
	AppRefreshTokenTTL   time.Duration

	// HTTP server settings.
	BindHost string
	Port     string

	// Log level: trace, debug, info, warn, error.
	LogLevel string

	// CORSOrigins is the list of allowed CORS origins parsed from a
	// comma-separated env var CORS_ORIGINS.
	CORSOrigins []string

	// DB pool tuning. Defaults are production-safe but can be overridden via
	// DB_MAX_CONNS, DB_MIN_CONNS, DB_MAX_CONN_IDLE_TIME (Go duration string).
	DBMaxConns        int32
	DBMinConns        int32
	DBMaxConnIdleTime time.Duration

	// Sentry. Empty DSN → SDK init becomes a no-op.
	SentryDSN         string
	SentryEnvironment string
	SentryRelease     string

	// AppEnv — "development" | "staging" | "production". Used by the
	// password-recovery flow: when empty SMTP_HOST AND AppEnv != "production",
	// /forgot-password returns the reset URL inline (test convenience). In
	// production an unset SMTP_HOST yields HTTP 503 — the operator must set
	// SMTP creds. AppEnv falls back to "development".
	AppEnv string

	// AppBaseURL is the public origin used to build password-reset links
	// e.g. https://tender.su10.ru. Required when SMTP is configured.
	AppBaseURL string

	// SMTP. Empty SMTPHost → email sending becomes a no-op. Used by the
	// password-recovery email flow only; nothing else in the BFF sends mail.
	SMTPHost     string
	SMTPPort     int
	SMTPUser     string
	SMTPPassword string
	SMTPFrom     string

	// CBRBaseURL is the Central Bank of Russia daily-rates XML endpoint used to
	// auto-fill tender currency rates. Public URL, not a secret.
	CBRBaseURL string

	// MCP/OAuth integration. MCP is fail-closed until MCP_ENABLED=true.
	MCPEnabled              bool
	MCPWriteEnabled         bool
	MCPCatalogWriteEnabled  bool
	MCPTemplateWriteEnabled bool
	MCPAudience             string
	MCPAccessTokenTTL       time.Duration
	MCPRefreshTokenTTL      time.Duration
	MCPAuthorizationCodeTTL time.Duration
	MCPDCREnabled           bool
	MCPCIMDAllowedHosts     []string
	MCPMaxRequestBodyBytes  int64

	// OpenRouter (этап 2.5). OpenRouterAPIKey — server-only secret: НЕ хранится
	// в БД, НЕ возвращается frontend'у, НЕ логируется. Пустой ключ допустим —
	// приложение стартует, AI-статус = not_configured, ручной Smart Import
	// работает. OpenRouterAPIBase в production обязан входить в allowlist
	// официальных base URL (openrouter.AllowedBaseURLs); в
	// development/staging разрешён кастомный base ТОЛЬКО для fake-server
	// интеграционных тестов. Из request/frontend base URL не принимается.
	OpenRouterAPIKey         string
	OpenRouterAPIBase        string
	OpenRouterHTTPReferer    string
	OpenRouterAppTitle       string
	OpenRouterTimeoutSeconds int

	// LLM-прокси. Нужен там, где у хоста нет исходящего доступа к openrouter.ai.
	// AIProviderMode переключает транспорт и задаётся ТОЛЬКО здесь: из
	// request/frontend режим не принимается, как и base URL с моделью.
	// ProxyLLMToken — server-only secret на тех же правах, что OpenRouterAPIKey.
	// ProxyLLMBaseURL хранится как ORIGIN (/healthz живёт вне /api/v1).
	// ProxyLLMAckNoProviderPolicy — осознанное подтверждение, что прокси
	// вырезает объект provider и privacy-политика делегирована его оператору;
	// без него proxy-режим остаётся не сконфигурированным.
	AIProviderMode              string
	ProxyLLMBaseURL             string
	ProxyLLMToken               string
	ProxyLLMTimeoutSeconds      int
	ProxyLLMAckNoProviderPolicy bool
}

// Load reads configuration from environment variables via Viper.
// All keys are bound to env vars automatically (viper.AutomaticEnv).
func Load() (*Config, error) {
	v := viper.New()
	v.AutomaticEnv()

	v.SetDefault("PORT", "3005")
	v.SetDefault("BIND_HOST", "0.0.0.0")
	v.SetDefault("LOG_LEVEL", "info")
	v.SetDefault("DB_MAX_CONNS", 20)
	v.SetDefault("DB_MIN_CONNS", 2)
	v.SetDefault("DB_MAX_CONN_IDLE_TIME", "5m")
	v.SetDefault("APP_ACCESS_TOKEN_TTL_MINUTES", 15)
	v.SetDefault("APP_REFRESH_TOKEN_TTL_DAYS", 30)
	v.SetDefault("APP_ENV", "development")
	v.SetDefault("SMTP_PORT", 587)
	v.SetDefault("CBR_BASE_URL", "https://www.cbr.ru/scripts/XML_daily.asp")
	v.SetDefault("MCP_ENABLED", false)
	v.SetDefault("MCP_WRITE_ENABLED", false)
	v.SetDefault("MCP_CATALOG_WRITE_ENABLED", false)
	v.SetDefault("MCP_TEMPLATE_WRITE_ENABLED", false)
	v.SetDefault("MCP_AUDIENCE", "hubtender-mcp")
	v.SetDefault("MCP_ACCESS_TOKEN_TTL_MINUTES", 10)
	v.SetDefault("MCP_REFRESH_TOKEN_TTL_DAYS", 30)
	v.SetDefault("MCP_AUTHORIZATION_CODE_TTL_MINUTES", 5)
	v.SetDefault("MCP_DCR_ENABLED", false)
	v.SetDefault("MCP_MAX_REQUEST_BODY_BYTES", 1048576)
	v.SetDefault("OPENROUTER_TIMEOUT_SECONDS", 60)
	// Дедлайн самого прокси ~190 с: клиентский таймаут обязан быть больше,
	// иначе его 504 deadline_exceeded недостижим в принципе.
	v.SetDefault("PROXY_LLM_TIMEOUT_SECONDS", 200)

	dbURL := v.GetString("DATABASE_URL")
	if dbURL == "" {
		return nil, fmt.Errorf("config: DATABASE_URL is required but not set")
	}

	appIssuer := v.GetString("APP_JWT_ISSUER")
	appAudience := v.GetString("APP_JWT_AUDIENCE")
	appKID := v.GetString("APP_JWT_KEY_ID")
	appKeyPath := v.GetString("APP_JWT_PRIVATE_KEY_PATH")
	appKeyB64 := v.GetString("APP_JWT_PRIVATE_KEY_B64")
	accessMins := v.GetInt("APP_ACCESS_TOKEN_TTL_MINUTES")
	refreshDays := v.GetInt("APP_REFRESH_TOKEN_TTL_DAYS")

	if appIssuer == "" {
		return nil, fmt.Errorf("config: APP_JWT_ISSUER is required")
	}
	if appKeyPath == "" && appKeyB64 == "" {
		return nil, fmt.Errorf("config: APP_JWT_PRIVATE_KEY_PATH or APP_JWT_PRIVATE_KEY_B64 is required")
	}
	if accessMins < 1 {
		return nil, fmt.Errorf("config: APP_ACCESS_TOKEN_TTL_MINUTES must be >= 1, got %d", accessMins)
	}
	if refreshDays < 1 {
		return nil, fmt.Errorf("config: APP_REFRESH_TOKEN_TTL_DAYS must be >= 1, got %d", refreshDays)
	}

	rawOrigins := v.GetString("CORS_ORIGINS")
	if rawOrigins == "" {
		return nil, fmt.Errorf("config: CORS_ORIGINS is required but not set")
	}
	origins := parseCORSOrigins(rawOrigins)
	if len(origins) == 0 {
		return nil, fmt.Errorf("config: CORS_ORIGINS must contain at least one origin")
	}

	maxConns := v.GetInt32("DB_MAX_CONNS")
	if maxConns < 1 {
		return nil, fmt.Errorf("config: DB_MAX_CONNS must be >= 1, got %d", maxConns)
	}
	minConns := v.GetInt32("DB_MIN_CONNS")
	if minConns < 0 || minConns > maxConns {
		return nil, fmt.Errorf("config: DB_MIN_CONNS must be in [0, DB_MAX_CONNS=%d], got %d", maxConns, minConns)
	}
	maxIdle, err := time.ParseDuration(v.GetString("DB_MAX_CONN_IDLE_TIME"))
	if err != nil {
		return nil, fmt.Errorf("config: DB_MAX_CONN_IDLE_TIME parse: %w", err)
	}
	mcpAccessMins := v.GetInt("MCP_ACCESS_TOKEN_TTL_MINUTES")
	mcpRefreshDays := v.GetInt("MCP_REFRESH_TOKEN_TTL_DAYS")
	mcpCodeMins := v.GetInt("MCP_AUTHORIZATION_CODE_TTL_MINUTES")
	if mcpAccessMins < 1 || mcpAccessMins > 60 {
		return nil, fmt.Errorf("config: MCP_ACCESS_TOKEN_TTL_MINUTES must be in [1,60]")
	}
	if mcpRefreshDays < 1 || mcpRefreshDays > 90 {
		return nil, fmt.Errorf("config: MCP_REFRESH_TOKEN_TTL_DAYS must be in [1,90]")
	}
	if mcpCodeMins < 1 || mcpCodeMins > 10 {
		return nil, fmt.Errorf("config: MCP_AUTHORIZATION_CODE_TTL_MINUTES must be in [1,10]")
	}
	mcpMaxBody := v.GetInt64("MCP_MAX_REQUEST_BODY_BYTES")
	if mcpMaxBody < 65536 || mcpMaxBody > 8*1024*1024 {
		return nil, fmt.Errorf("config: MCP_MAX_REQUEST_BODY_BYTES must be in [65536,8388608]")
	}

	appEnv := strings.ToLower(strings.TrimSpace(v.GetString("APP_ENV")))
	providerMode, proxyBase, proxyToken, proxyTimeout, err := loadLLMTransport(v, appEnv)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		DatabaseURL:             dbURL,
		AppJWTIssuer:            appIssuer,
		AppJWTAudience:          appAudience,
		AppJWTKeyID:             appKID,
		AppJWTPrivateKeyPath:    appKeyPath,
		AppJWTPrivateKeyB64:     appKeyB64,
		AppAccessTokenTTL:       time.Duration(accessMins) * time.Minute,
		AppRefreshTokenTTL:      time.Duration(refreshDays) * 24 * time.Hour,
		BindHost:                v.GetString("BIND_HOST"),
		Port:                    v.GetString("PORT"),
		LogLevel:                v.GetString("LOG_LEVEL"),
		CORSOrigins:             origins,
		DBMaxConns:              maxConns,
		DBMinConns:              minConns,
		DBMaxConnIdleTime:       maxIdle,
		SentryDSN:               v.GetString("SENTRY_DSN"),
		SentryEnvironment:       v.GetString("SENTRY_ENVIRONMENT"),
		SentryRelease:           v.GetString("SENTRY_RELEASE"),
		AppEnv:                  appEnv,
		AppBaseURL:              strings.TrimRight(strings.TrimSpace(v.GetString("APP_BASE_URL")), "/"),
		SMTPHost:                v.GetString("SMTP_HOST"),
		SMTPPort:                v.GetInt("SMTP_PORT"),
		SMTPUser:                v.GetString("SMTP_USER"),
		SMTPPassword:            v.GetString("SMTP_PASSWORD"),
		SMTPFrom:                v.GetString("SMTP_FROM"),
		CBRBaseURL:              v.GetString("CBR_BASE_URL"),
		MCPEnabled:              v.GetBool("MCP_ENABLED"),
		MCPWriteEnabled:         v.GetBool("MCP_WRITE_ENABLED"),
		MCPCatalogWriteEnabled:  v.GetBool("MCP_CATALOG_WRITE_ENABLED"),
		MCPTemplateWriteEnabled: v.GetBool("MCP_TEMPLATE_WRITE_ENABLED"),
		MCPAudience:             strings.TrimSpace(v.GetString("MCP_AUDIENCE")),
		MCPAccessTokenTTL:       time.Duration(mcpAccessMins) * time.Minute,
		MCPRefreshTokenTTL:      time.Duration(mcpRefreshDays) * 24 * time.Hour,
		MCPAuthorizationCodeTTL: time.Duration(mcpCodeMins) * time.Minute,
		MCPDCREnabled:           v.GetBool("MCP_DCR_ENABLED"),
		MCPCIMDAllowedHosts:     parseCORSOrigins(v.GetString("MCP_CIMD_ALLOWED_HOSTS")),
		MCPMaxRequestBodyBytes:  mcpMaxBody,

		OpenRouterAPIKey:         v.GetString("OPENROUTER_API_KEY"),
		OpenRouterAPIBase:        strings.TrimSpace(v.GetString("OPENROUTER_API_BASE")),
		OpenRouterHTTPReferer:    v.GetString("OPENROUTER_HTTP_REFERER"),
		OpenRouterAppTitle:       v.GetString("OPENROUTER_APP_TITLE"),
		OpenRouterTimeoutSeconds: v.GetInt("OPENROUTER_TIMEOUT_SECONDS"),

		AIProviderMode:              providerMode,
		ProxyLLMBaseURL:             proxyBase,
		ProxyLLMToken:               proxyToken,
		ProxyLLMTimeoutSeconds:      proxyTimeout,
		ProxyLLMAckNoProviderPolicy: v.GetBool("PROXY_LLM_ACK_NO_PROVIDER_POLICY"),
	}
	if cfg.MCPEnabled && cfg.MCPAudience == "" {
		return nil, fmt.Errorf("config: MCP_AUDIENCE is required when MCP_ENABLED=true")
	}
	if cfg.MCPEnabled && cfg.AppBaseURL == "" {
		return nil, fmt.Errorf("config: APP_BASE_URL is required when MCP_ENABLED=true")
	}

	return cfg, nil
}

// proxyTokenRe — токен LLM-прокси: ровно 32 байта hex. Строже, чем проверка
// ключа OpenRouter (префикс + длина), поэтому это не ослабление валидации.
var proxyTokenRe = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

// loadLLMTransport читает и валидирует переключатель транспорта LLM.
//
// Опечатка в режиме — fail-fast, а не молчаливый откат на openrouter: откат
// означал бы вызовы в сеть, которой у прод-хоста может не быть, и диагностику
// «AI просто не работает» вместо внятной ошибки старта.
func loadLLMTransport(v *viper.Viper, appEnv string) (mode, baseURL, token string, timeout int, err error) {
	mode = strings.ToLower(strings.TrimSpace(v.GetString("AI_PROVIDER_MODE")))
	if mode == "" {
		mode = openrouter.String(openrouter.TransportOpenRouter)
	}
	t, perr := openrouter.ParseTransport(mode)
	if perr != nil {
		return "", "", "", 0, fmt.Errorf("config: AI_PROVIDER_MODE: %w", perr)
	}
	mode = openrouter.String(t)
	timeout = v.GetInt("PROXY_LLM_TIMEOUT_SECONDS")
	if t != openrouter.TransportProxyLLM {
		return mode, "", "", timeout, nil
	}

	baseURL, berr := openrouter.NormalizeProxyBaseURL(
		v.GetString("PROXY_LLM_BASE_URL"), appEnv == "production")
	if berr != nil {
		return "", "", "", 0, fmt.Errorf("config: PROXY_LLM_BASE_URL: %w", berr)
	}
	// Значение токена в текст ошибки не попадает.
	token = strings.TrimSpace(v.GetString("PROXY_LLM_TOKEN"))
	if !proxyTokenRe.MatchString(token) {
		return "", "", "", 0, fmt.Errorf("config: PROXY_LLM_TOKEN must be 64 hex characters")
	}
	if timeout < 5 || timeout > 600 {
		return "", "", "", 0, fmt.Errorf("config: PROXY_LLM_TIMEOUT_SECONDS must be between 5 and 600")
	}
	return mode, baseURL, token, timeout, nil
}

// parseCORSOrigins splits and trims a comma-separated list of origins.
func parseCORSOrigins(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
