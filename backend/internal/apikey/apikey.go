// Package apikey — выпуск и проверка машинных API-ключей.
//
// Секрет генерируется из crypto/rand, показывается вызывающему ОДИН раз и в
// БД не попадает: хранится только SHA-256 хеш и префикс для опознания.
// Пакет чистый — ни сети, ни БД, ни времени.
package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Prefix — опознавательный префикс ключа (TenderHUB key).
const Prefix = "thk_"

// secretBytes — 32 байта энтропии: 256 бит, как у refresh-токенов app_auth.
const secretBytes = 32

// DisplayPrefixLen — сколько первых символов ключа не является секретом и
// показывается в списке, чтобы отличать ключи друг от друга.
const DisplayPrefixLen = 12

// Области доступа. Список закрытый: он продублирован в CHECK
// api_keys_scopes_chk и в валидаторе выпуска ключа — расширять надо все три.
const (
	ScopeArchiveRead  = "archive:read"
	ScopeArchiveWrite = "archive:write"
	// ScopeTendersRead — чтение тендеров и смет: список тендеров, шапка,
	// позиции с итогами, строки BOQ. Только чтение: записи в тендер эта
	// область не открывает.
	ScopeTendersRead = "tenders:read"
	// ScopeTendersWrite — запись строк BOQ в позиции тендера: создание и
	// обновление работ/материалов, пересчёт итогов позиции. Ограничение ключа
	// по списку тендеров действует и здесь.
	ScopeTendersWrite = "tenders:write"
	// ScopeVerificationRead — чтение «Проверки данных»: находки правил и их
	// каталог, готовность разделов ВОР, сравнение с эталонами, выжимка для
	// руководства. Только чтение.
	ScopeVerificationRead = "verification:read"
	// ScopeVerificationWrite — действия проверяющего: вердикт по находке,
	// отметка «Проверка завершена», отметка раздела, текст выжимки. Рассылка
	// в Telegram и справочник эталонов ключу не открываются.
	ScopeVerificationWrite = "verification:write"
	// ScopeRegistryRead — «Перечень тендеров» для Telegram-бота: поиск и
	// карточка тендера (api/REGISTRY.md). Только чтение.
	ScopeRegistryRead = "registry:read"
	// ScopeRegistryWrite — выдача положенных напоминаний (запоминается, что
	// выдано) и запись звонка/события в хронологию. Ключ, ограниченный списком
	// тендеров, к перечню не допускается: у перечня нет связи с тендерами.
	ScopeRegistryWrite = "registry:write"
)

// KnownScopes — все допустимые области.
var KnownScopes = []string{
	ScopeArchiveRead, ScopeArchiveWrite, ScopeTendersRead, ScopeTendersWrite,
	ScopeVerificationRead, ScopeVerificationWrite, ScopeRegistryRead, ScopeRegistryWrite,
}

func isKnownScope(s string) bool {
	for _, k := range KnownScopes {
		if k == s {
			return true
		}
	}
	return false
}

// ErrUnknownScope — область вне известного списка.
var ErrUnknownScope = errors.New("unknown api key scope")

// ErrNoScopes — ключ без прав. Такой ключ создаёт иллюзию доступа и запрещён.
var ErrNoScopes = errors.New("api key must have at least one scope")

// Generated — результат выпуска ключа.
type Generated struct {
	// Secret отдаётся пользователю ОДИН раз и нигде не сохраняется.
	Secret string
	// Hash кладётся в БД вместо секрета.
	Hash string
	// Prefix — неконфиденциальная часть для отображения.
	Prefix string
}

// Generate выпускает новый ключ.
func Generate() (Generated, error) {
	buf := make([]byte, secretBytes)
	if _, err := rand.Read(buf); err != nil {
		return Generated{}, fmt.Errorf("apikey.Generate: %w", err)
	}
	secret := Prefix + base64.RawURLEncoding.EncodeToString(buf)
	return Generated{
		Secret: secret,
		Hash:   Hash(secret),
		Prefix: DisplayPrefix(secret),
	}, nil
}

// Hash — SHA-256 hex полного секрета. Соль не нужна: секрет сам по себе имеет
// 256 бит энтропии, перебор по словарю к нему неприменим.
func Hash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// DisplayPrefix — первые DisplayPrefixLen символов ключа.
func DisplayPrefix(secret string) string {
	r := []rune(secret)
	if len(r) <= DisplayPrefixLen {
		return secret
	}
	return string(r[:DisplayPrefixLen])
}

// Equal — сравнение хешей за постоянное время.
func Equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// LooksLikeKey — дешёвая проверка формы до похода в БД.
func LooksLikeKey(s string) bool {
	return strings.HasPrefix(s, Prefix) && len(s) > len(Prefix)+8
}

// NormalizeScopes валидирует, дедуплицирует и сортирует области.
func NormalizeScopes(scopes []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(scopes))
	for _, s := range scopes {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !isKnownScope(s) {
			return nil, fmt.Errorf("%w: %q", ErrUnknownScope, s)
		}
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, ErrNoScopes
	}
	sort.Strings(out)
	return out, nil
}

// HasScope сообщает, есть ли область у ключа.
func HasScope(scopes []string, want string) bool {
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
}
