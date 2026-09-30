package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/su10/hubtender/backend/internal/handlers"
	"github.com/su10/hubtender/backend/internal/middleware"
	"github.com/su10/hubtender/backend/internal/notify/telegram"
	"github.com/su10/hubtender/backend/internal/repository"
	"github.com/su10/hubtender/backend/internal/services"
)

const regID = "0f8fad5b-d9cb-469f-a165-70867728950e"

type fakeRegistryBot struct {
	appendErr error
	created   bool
}

func (f *fakeRegistryBot) Search(context.Context, string) (*services.RegistrySearchResult, error) {
	return &services.RegistrySearchResult{Query: "ода", Items: []services.RegistryTenderBrief{},
		Message: telegram.Payload("не нашёл", nil)}, nil
}
func (f *fakeRegistryBot) Card(_ context.Context, id string) (*services.RegistryCard, error) {
	if id != regID {
		return nil, repository.ErrTenderRegistryNotFound
	}
	return &services.RegistryCard{Tender: services.RegistryTenderBrief{ID: id},
		Message: telegram.Payload("карточка", [][]telegram.InlineButton{{{Text: "x", CallbackData: "tc:" + id}}})}, nil
}
func (f *fakeRegistryBot) ClaimReminders(_ context.Context, chatID int64) (*services.RegistryReminders, error) {
	if chatID == 0 {
		return nil, &services.RegistryInputError{Reason: "chat_id: нужен id чата"}
	}
	return &services.RegistryReminders{Items: []services.RegistryReminder{}}, nil
}
func (f *fakeRegistryBot) AppendChronology(context.Context, string, string, string, string) (*services.RegistryChronologyResult, error) {
	if f.appendErr != nil {
		return nil, f.appendErr
	}
	return &services.RegistryChronologyResult{Created: f.created, Title: "ЖК"}, nil
}

// registryRouter — маршруты перечня; principal — ключ запроса (nil — человек с JWT).
func registryRouter(bot *fakeRegistryBot, principal *middleware.APIKeyPrincipal) *chi.Mux {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if principal != nil {
				req = req.WithContext(context.WithValue(req.Context(), middleware.CtxAPIKey, principal))
			}
			next.ServeHTTP(w, req)
		})
	})
	registerRegistryBotRoutes(r, handlers.NewTenderRegistryBotHandler(bot))
	return r
}

func callRegistry(t *testing.T, r http.Handler, method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return w, m
}

func TestRegistryRoutesGate(t *testing.T) {
	both := &middleware.APIKeyPrincipal{ID: "k", Scopes: []string{"registry:read", "registry:write"}}
	readOnly := &middleware.APIKeyPrincipal{ID: "k", Scopes: []string{"registry:read"}}
	tendersOnly := &middleware.APIKeyPrincipal{ID: "k", Scopes: []string{"tenders:read", "tenders:write"}}
	restricted := &middleware.APIKeyPrincipal{ID: "k", Scopes: []string{"registry:read", "registry:write"},
		AllowedTenderIDs: []string{"11111111-1111-1111-1111-111111111111"}}
	search, claim := "/api/v1/tender-registry/search?q=ода", "/api/v1/tender-registry/reminders/claim"
	cases := []struct {
		name      string
		principal *middleware.APIKeyPrincipal
		method    string
		path      string
		status    int
		code      string
	}{
		{"человек с JWT", nil, "GET", search, 403, "API_KEY_REQUIRED"},
		{"ключ с обеими областями — поиск", both, "GET", search, 200, ""},
		{"ключ с обеими областями — выдача", both, "POST", claim, 200, ""},
		{"ключ на чтение — поиск", readOnly, "GET", search, 200, ""},
		{"ключ на чтение — выдача", readOnly, "POST", claim, 403, "API_KEY_SCOPE_DENIED"},
		{"ключ тендеров", tendersOnly, "GET", search, 403, "API_KEY_SCOPE_DENIED"},
		{"ключ со списком тендеров", restricted, "GET", search, 403, "API_KEY_TENDER_DENIED"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, m := callRegistry(t, registryRouter(&fakeRegistryBot{}, c.principal), c.method, c.path, `{"chat_id":-100}`)
			if w.Code != c.status || (c.code != "" && m["code"] != c.code) {
				t.Fatalf("status %d code %v, ожидалось %d %s: %s", w.Code, m["code"], c.status, c.code, w.Body.String())
			}
		})
	}
}

func TestRegistryHandlerResponses(t *testing.T) {
	key := &middleware.APIKeyPrincipal{ID: "k", Scopes: []string{"registry:read", "registry:write"}}
	chronology := "/api/v1/tender-registry/" + regID + "/chronology"
	body := `{"text":"Позвонили","type":"call_follow_up","date":"2026-09-30T09:00:00Z"}`

	w, m := callRegistry(t, registryRouter(&fakeRegistryBot{}, key), "GET", "/api/v1/tender-registry/search?q=ода", "")
	data, _ := m["data"].(map[string]any)
	msg, _ := data["message"].(map[string]any)
	if w.Header().Get("Cache-Control") != "no-store" || msg["parse_mode"] != "HTML" || data["items"] == nil {
		t.Fatalf("ответ поиска: %s %s", w.Header().Get("Cache-Control"), w.Body.String())
	}
	if _, ok := msg["reply_markup"]; ok {
		t.Fatal("reply_markup без кнопок должен опускаться")
	}

	w, m = callRegistry(t, registryRouter(&fakeRegistryBot{}, key), "GET", "/api/v1/tender-registry/"+regID+"/card", "")
	if data, _ := m["data"].(map[string]any); w.Code != 200 || data["message"].(map[string]any)["reply_markup"] == nil {
		t.Fatalf("карточка: %d %s", w.Code, w.Body.String())
	}
	cases := []struct {
		name   string
		bot    *fakeRegistryBot
		method string
		path   string
		body   string
		status int
		code   string
	}{
		{"карточка: не uuid", &fakeRegistryBot{}, "GET", "/api/v1/tender-registry/abc/card", "", 400, "REGISTRY_INVALID_REQUEST"},
		{"карточка: нет тендера", &fakeRegistryBot{}, "GET", "/api/v1/tender-registry/1f8fad5b-d9cb-469f-a165-70867728950e/card", "", 404, "REGISTRY_NOT_FOUND"},
		{"выдача: битое тело", &fakeRegistryBot{}, "POST", "/api/v1/tender-registry/reminders/claim", "chat=1", 400, "REGISTRY_INVALID_REQUEST"},
		{"выдача: без chat_id", &fakeRegistryBot{}, "POST", "/api/v1/tender-registry/reminders/claim", "{}", 400, "REGISTRY_INVALID_REQUEST"},
		{"запись: новая", &fakeRegistryBot{created: true}, "POST", chronology, body, 201, ""},
		{"запись: повтор", &fakeRegistryBot{}, "POST", chronology, body, 200, ""},
		{"запись: неверный ввод", &fakeRegistryBot{appendErr: &services.RegistryInputError{Reason: "text"}}, "POST", chronology, body, 400, "REGISTRY_INVALID_REQUEST"},
		{"запись: нет тендера", &fakeRegistryBot{appendErr: repository.ErrTenderRegistryNotFound}, "POST", chronology, body, 404, "REGISTRY_NOT_FOUND"},
		{"запись: тело больше 64 КБ", &fakeRegistryBot{}, "POST", chronology, `{"text":"` + strings.Repeat("я", 40000) + `"}`, 400, "REGISTRY_INVALID_REQUEST"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, m := callRegistry(t, registryRouter(c.bot, key), c.method, c.path, c.body)
			if w.Code != c.status || (c.code != "" && m["code"] != c.code) {
				t.Fatalf("status %d code %v, ожидалось %d %s: %.300s", w.Code, m["code"], c.status, c.code, w.Body.String())
			}
		})
	}
}
