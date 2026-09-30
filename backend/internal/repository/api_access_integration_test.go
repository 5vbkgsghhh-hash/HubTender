package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/su10/hubtender/backend/internal/apikey"
)

// PostgreSQL-интеграция для управления машинным доступом к API.
// Покрывает ровно то, что дёргает страница «Настройки → Доступ к API»:
// список ключей, настройки выдачи и журнал вызовов.
//
//	HUBTENDER_TEST_DATABASE_URL='postgres://…/hubtender_test?sslmode=disable' \
//	  go test ./internal/repository/ -run ApiAccessIntegration -v

const apiAccessTestUser = "00000000-0000-0000-0000-000000000000"

func cleanupApiAccess(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := pool.Exec(ctx, `DELETE FROM public.api_call_log`); err != nil {
			t.Logf("cleanup api_call_log: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM public.api_keys`); err != nil {
			t.Logf("cleanup api_keys: %v", err)
		}
	})
}

func TestApiAccessIntegration_ListKeysOnEmptyTable(t *testing.T) {
	pool := newTestPool(t)
	cleanupApiAccess(t, pool)
	repo := NewApiAccessRepo(pool)

	keys, err := repo.ListApiKeys(context.Background())
	if err != nil {
		t.Fatalf("ListApiKeys: %v", err)
	}
	if keys == nil {
		t.Fatal("пустой список должен быть [], а не nil — иначе UI получит null")
	}
	if len(keys) != 0 {
		t.Fatalf("ожидали пустой список, получили %d", len(keys))
	}
}

func TestApiAccessIntegration_KeyLifecycle(t *testing.T) {
	pool := newTestPool(t)
	cleanupApiAccess(t, pool)
	repo := NewApiAccessRepo(pool)
	ctx := context.Background()

	gen, err := apikey.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	created, err := repo.CreateApiKey(ctx, CreateApiKeyInput{
		Name:      "itest-key",
		KeyPrefix: gen.Prefix,
		KeyHash:   gen.Hash,
		Scopes:    []string{apikey.ScopeArchiveRead, apikey.ScopeArchiveWrite},
		CreatedBy: apiAccessTestUser,
	})
	if err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}
	if created.Status != "active" {
		t.Fatalf("статус нового ключа = %q, want active", created.Status)
	}
	if len(created.Scopes) != 2 {
		t.Fatalf("области = %v", created.Scopes)
	}
	if created.AllowedTenderIDs == nil {
		t.Fatal("список тендеров должен быть [], а не nil")
	}
	if created.CreatedByName == nil || *created.CreatedByName == "" {
		t.Fatal("имя выпустившего должно подтягиваться из users")
	}

	// Проверка секрета: ключ находится по хешу.
	verified, err := repo.VerifyApiKeyHash(ctx, gen.Hash)
	if err != nil {
		t.Fatalf("VerifyApiKeyHash: %v", err)
	}
	if verified.OwnerUserID != apiAccessTestUser || verified.ID != created.ID {
		t.Fatalf("проверка вернула чужой ключ: %+v", verified)
	}

	if err := repo.TouchApiKey(ctx, created.ID); err != nil {
		t.Fatalf("TouchApiKey: %v", err)
	}

	// Отзыв: ключ перестаёт находиться, но остаётся в списке.
	revoked, err := repo.RevokeApiKey(ctx, created.ID, apiAccessTestUser)
	if err != nil {
		t.Fatalf("RevokeApiKey: %v", err)
	}
	if revoked.Status != "revoked" || revoked.RevokedAt == nil {
		t.Fatalf("после отзыва: %+v", revoked)
	}
	if _, err := repo.VerifyApiKeyHash(ctx, gen.Hash); !errors.Is(err, ErrApiKeyNotFound) {
		t.Fatalf("отозванный ключ не должен находиться, получили %v", err)
	}

	// Повторный отзыв идемпотентен.
	if _, err := repo.RevokeApiKey(ctx, created.ID, apiAccessTestUser); err != nil {
		t.Fatalf("повторный отзыв должен быть no-op: %v", err)
	}

	if err := repo.DeleteApiKey(ctx, created.ID); err != nil {
		t.Fatalf("DeleteApiKey: %v", err)
	}
	if err := repo.DeleteApiKey(ctx, created.ID); !errors.Is(err, ErrApiKeyNotFound) {
		t.Fatalf("удаление несуществующего → ErrApiKeyNotFound, получили %v", err)
	}
}

func TestApiAccessIntegration_KeyWithTenderScopeAndExpiry(t *testing.T) {
	pool := newTestPool(t)
	cleanupApiAccess(t, pool)
	repo := NewApiAccessRepo(pool)
	ctx := context.Background()

	var tenderID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO public.tenders (title, client_name, tender_number)
		VALUES ('itest-api-access','itest-client','ITEST-API-1') RETURNING id::text
	`).Scan(&tenderID); err != nil {
		t.Fatalf("fixture tender: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM public.tenders WHERE id = $1::uuid`, tenderID); err != nil {
			t.Logf("cleanup tender: %v", err)
		}
	})

	gen, _ := apikey.Generate()
	past := "2020-01-01T00:00:00Z"
	expired, err := repo.CreateApiKey(ctx, CreateApiKeyInput{
		Name:             "itest-expired",
		KeyPrefix:        gen.Prefix,
		KeyHash:          gen.Hash,
		Scopes:           []string{apikey.ScopeArchiveRead},
		AllowedTenderIDs: []string{tenderID},
		ExpiresAt:        &past,
		CreatedBy:        apiAccessTestUser,
	})
	if err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}
	if expired.Status != "expired" {
		t.Fatalf("статус просроченного = %q, want expired", expired.Status)
	}
	if len(expired.AllowedTenderIDs) != 1 || expired.AllowedTenderIDs[0] != tenderID {
		t.Fatalf("список тендеров = %v", expired.AllowedTenderIDs)
	}
	if _, err := repo.VerifyApiKeyHash(ctx, gen.Hash); !errors.Is(err, ErrApiKeyNotFound) {
		t.Fatalf("просроченный ключ не должен находиться, получили %v", err)
	}
}

func TestApiAccessIntegration_TendersReadScopeAccepted(t *testing.T) {
	// CHECK api_keys_scopes_chk перечисляет области явно: без миграции
	// 2026_08_api_scope_tenders_read ключ с этой областью не вставится,
	// каким бы ни был Go-валидатор.
	pool := newTestPool(t)
	cleanupApiAccess(t, pool)
	repo := NewApiAccessRepo(pool)

	gen, _ := apikey.Generate()
	created, err := repo.CreateApiKey(context.Background(), CreateApiKeyInput{
		Name:      "itest-tenders-read",
		KeyPrefix: gen.Prefix,
		KeyHash:   gen.Hash,
		Scopes:    []string{apikey.ScopeArchiveWrite, apikey.ScopeTendersRead},
		CreatedBy: apiAccessTestUser,
	})
	if err != nil {
		t.Fatalf("CreateApiKey с tenders:read: %v", err)
	}
	if len(created.Scopes) != 2 {
		t.Fatalf("области = %v", created.Scopes)
	}

	verified, err := repo.VerifyApiKeyHash(context.Background(), gen.Hash)
	if err != nil {
		t.Fatalf("VerifyApiKeyHash: %v", err)
	}
	if !apikey.HasScope(verified.Scopes, apikey.ScopeTendersRead) {
		t.Fatalf("область не доехала до рантайма: %v", verified.Scopes)
	}
}

func TestApiAccessIntegration_RegistryScopesAccepted(t *testing.T) {
	// Без миграции 2026_10_tender_registry_api ключ для бота перечня не вставится.
	pool := newTestPool(t)
	cleanupApiAccess(t, pool)
	repo := NewApiAccessRepo(pool)

	gen, _ := apikey.Generate()
	if _, err := repo.CreateApiKey(context.Background(), CreateApiKeyInput{
		Name:      "itest-registry",
		KeyPrefix: gen.Prefix,
		KeyHash:   gen.Hash,
		Scopes:    []string{apikey.ScopeRegistryRead, apikey.ScopeRegistryWrite},
		CreatedBy: apiAccessTestUser,
	}); err != nil {
		t.Fatalf("CreateApiKey с registry:*: %v", err)
	}
	verified, err := repo.VerifyApiKeyHash(context.Background(), gen.Hash)
	if err != nil || !apikey.HasScope(verified.Scopes, apikey.ScopeRegistryWrite) {
		t.Fatalf("область не доехала до рантайма: %+v %v", verified, err)
	}
}

func TestApiAccessIntegration_UnknownScopeRejectedByDB(t *testing.T) {
	pool := newTestPool(t)
	cleanupApiAccess(t, pool)

	_, err := pool.Exec(context.Background(), `
		INSERT INTO public.api_keys (name, key_prefix, key_hash, scopes, created_by)
		VALUES ('itest-bad-scope','thk_x','hash-bad', ARRAY['tenders:admin']::text[], $1::uuid)
	`, apiAccessTestUser)
	if err == nil {
		t.Fatal("БД обязана отклонить область вне списка")
	}
}

func TestApiAccessIntegration_Settings(t *testing.T) {
	pool := newTestPool(t)
	repo := NewApiAccessRepo(pool)
	ctx := context.Background()

	before, err := repo.GetApiAccessSettings(ctx)
	if err != nil {
		t.Fatalf("GetApiAccessSettings: %v", err)
	}
	t.Cleanup(func() {
		if _, err := repo.UpdateApiAccessSettings(ctx, *before, apiAccessTestUser); err != nil {
			t.Logf("restore settings: %v", err)
		}
	})

	next := *before
	next.ArchiveComposeEnabled = false
	next.MaxSearchLimit = 7
	next.RateLimitPerMinute = 0

	updated, err := repo.UpdateApiAccessSettings(ctx, next, apiAccessTestUser)
	if err != nil {
		t.Fatalf("UpdateApiAccessSettings: %v", err)
	}
	if updated.ArchiveComposeEnabled || updated.MaxSearchLimit != 7 || updated.RateLimitPerMinute != 0 {
		t.Fatalf("настройки не сохранились: %+v", updated)
	}
	if updated.UpdatedBy == nil || *updated.UpdatedBy != apiAccessTestUser {
		t.Fatalf("updated_by = %v", updated.UpdatedBy)
	}
	if updated.UpdatedByName == nil || *updated.UpdatedByName == "" {
		t.Fatal("имя изменившего должно подтягиваться из users")
	}
	if updated.UpdatedAt == "" {
		t.Fatal("updated_at пустой")
	}
}

func TestApiAccessIntegration_SettingsUpsertWhenRowMissing(t *testing.T) {
	pool := newTestPool(t)
	repo := NewApiAccessRepo(pool)
	ctx := context.Background()

	before, err := repo.GetApiAccessSettings(ctx)
	if err != nil {
		t.Fatalf("GetApiAccessSettings: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM public.api_access_settings`); err != nil {
		t.Fatalf("удаление строки настроек: %v", err)
	}
	t.Cleanup(func() {
		if _, err := repo.UpdateApiAccessSettings(ctx, *before, apiAccessTestUser); err != nil {
			t.Logf("restore settings: %v", err)
		}
	})

	// Свежая установка: строки нет — читатель отдаёт значения по умолчанию.
	got, err := repo.GetApiAccessSettings(ctx)
	if err != nil {
		t.Fatalf("чтение без строки должно отдавать умолчания, получили %v", err)
	}
	def := DefaultApiAccessSettings()
	if got.MaxSearchLimit != def.MaxSearchLimit || !got.ArchiveSearchEnabled {
		t.Fatalf("умолчания не совпали: %+v", got)
	}

	// Первое сохранение обязано создать строку, а не промолчать.
	next := def
	next.MaxSuggestQueries = 42
	saved, err := repo.UpdateApiAccessSettings(ctx, next, apiAccessTestUser)
	if err != nil {
		t.Fatalf("UpdateApiAccessSettings: %v", err)
	}
	if saved.MaxSuggestQueries != 42 {
		t.Fatalf("upsert не создал строку: %+v", saved)
	}
}

func TestApiAccessIntegration_CallLog(t *testing.T) {
	pool := newTestPool(t)
	cleanupApiAccess(t, pool)
	repo := NewApiAccessRepo(pool)
	ctx := context.Background()

	empty, err := repo.ListApiCallLog(ctx, ApiCallLogFilter{})
	if err != nil {
		t.Fatalf("ListApiCallLog: %v", err)
	}
	if empty == nil {
		t.Fatal("пустой журнал должен быть [], а не nil")
	}

	gen, _ := apikey.Generate()
	key, err := repo.CreateApiKey(ctx, CreateApiKeyInput{
		Name: "itest-log-key", KeyPrefix: gen.Prefix, KeyHash: gen.Hash,
		Scopes: []string{apikey.ScopeArchiveRead}, CreatedBy: apiAccessTestUser,
	})
	if err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}

	items, dry := 12, true
	code := "MISSING_FX_RATE"
	entries := []ApiCallLogEntry{
		{ApiKeyID: &key.ID, UserID: strPtr(apiAccessTestUser), Method: "GET",
			Path: "/api/v1/archive/positions/search", Status: 200, DurationMs: 15,
			ItemsAffected: &items},
		{UserID: strPtr(apiAccessTestUser), Method: "POST",
			Path: "/api/v1/archive/compose", Status: 400, DurationMs: 40,
			ErrorCode: &code, DryRun: &dry},
	}
	for i, e := range entries {
		if err := repo.InsertApiCallLog(ctx, e); err != nil {
			t.Fatalf("InsertApiCallLog %d: %v", i, err)
		}
	}

	all, err := repo.ListApiCallLog(ctx, ApiCallLogFilter{Limit: 50})
	if err != nil {
		t.Fatalf("ListApiCallLog: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("записей %d, want 2", len(all))
	}

	byKey, err := repo.ListApiCallLog(ctx, ApiCallLogFilter{ApiKeyID: key.ID})
	if err != nil {
		t.Fatalf("ListApiCallLog по ключу: %v", err)
	}
	if len(byKey) != 1 || byKey[0].ApiKeyName == nil || *byKey[0].ApiKeyName != "itest-log-key" {
		t.Fatalf("фильтр по ключу вернул %+v", byKey)
	}

	onlyErrors, err := repo.ListApiCallLog(ctx, ApiCallLogFilter{OnlyErrors: true})
	if err != nil {
		t.Fatalf("ListApiCallLog только ошибки: %v", err)
	}
	if len(onlyErrors) != 1 || onlyErrors[0].Status != 400 {
		t.Fatalf("фильтр ошибок вернул %+v", onlyErrors)
	}
	if onlyErrors[0].ErrorCode == nil || *onlyErrors[0].ErrorCode != code {
		t.Fatalf("код ошибки не сохранился: %+v", onlyErrors[0])
	}
	if onlyErrors[0].DryRun == nil || !*onlyErrors[0].DryRun {
		t.Fatal("признак dry_run не сохранился")
	}

	// Удаление ключа не должно уносить журнал (ON DELETE SET NULL).
	if err := repo.DeleteApiKey(ctx, key.ID); err != nil {
		t.Fatalf("DeleteApiKey: %v", err)
	}
	after, err := repo.ListApiCallLog(ctx, ApiCallLogFilter{})
	if err != nil {
		t.Fatalf("ListApiCallLog после удаления ключа: %v", err)
	}
	if len(after) != 2 {
		t.Fatalf("журнал потерял записи после удаления ключа: %d", len(after))
	}

	if _, err := repo.PurgeApiCallLog(ctx, 30); err != nil {
		t.Fatalf("PurgeApiCallLog: %v", err)
	}
}

func strPtr(v string) *string { return &v }
