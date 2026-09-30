package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/su10/hubtender/backend/internal/middleware"
	"github.com/su10/hubtender/backend/internal/repository"
	"github.com/su10/hubtender/backend/internal/services"
	"github.com/su10/hubtender/backend/pkg/apierr"
)

type tenderRegistryBotServicer interface {
	Search(ctx context.Context, query string) (*services.RegistrySearchResult, error)
	Card(ctx context.Context, id string) (*services.RegistryCard, error)
	ClaimReminders(ctx context.Context, chatID int64) (*services.RegistryReminders, error)
	AppendChronology(ctx context.Context, registryID, text, entryType, date string) (*services.RegistryChronologyResult, error)
}

// TenderRegistryBotHandler — «Перечень тендеров» для внешнего Telegram-бота по
// машинному ключу (api/REGISTRY.md). Ответы — {data}, ошибки — RFC 7807 с
// машинным code: REGISTRY_INVALID_REQUEST, REGISTRY_NOT_FOUND.
type TenderRegistryBotHandler struct {
	svc tenderRegistryBotServicer
}

func NewTenderRegistryBotHandler(svc tenderRegistryBotServicer) *TenderRegistryBotHandler {
	return &TenderRegistryBotHandler{svc: svc}
}

const registryBodyLimit = 64 << 10

// registryProblem — ошибка с кодом, который попадает и в журнал вызовов ключа.
func registryProblem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	if s := middleware.CallStatFromContext(r.Context()); s != nil {
		s.ErrorCode = code
	}
	apierr.ArchiveProblem(status, code, detail, nil).Render(w)
}

func (h *TenderRegistryBotHandler) fail(w http.ResponseWriter, r *http.Request, err error, op string) {
	var in *services.RegistryInputError
	switch {
	case errors.As(err, &in):
		registryProblem(w, r, http.StatusBadRequest, "REGISTRY_INVALID_REQUEST", in.Reason)
	case errors.Is(err, repository.ErrTenderRegistryNotFound):
		registryProblem(w, r, http.StatusNotFound, "REGISTRY_NOT_FOUND", "Тендера нет в перечне.")
	default:
		apierr.InternalFromErr(w, r, err, op)
	}
}

// renderRegistry — {data} без кэширования: напоминания и карточки должны быть свежими.
func renderRegistry(w http.ResponseWriter, r *http.Request, status int, v any) {
	body, err := json.Marshal(dataEnvelope{Data: v})
	if err != nil {
		apierr.InternalFromErr(w, r, err, "response serialization failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func registryID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := chi.URLParam(r, "id")
	if !uuidPatchRe.MatchString(id) {
		registryProblem(w, r, http.StatusBadRequest, "REGISTRY_INVALID_REQUEST", "id: ожидается uuid строки перечня")
		return "", false
	}
	return strings.ToLower(id), true
}

func decodeRegistryBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, registryBodyLimit)).Decode(v); err != nil {
		registryProblem(w, r, http.StatusBadRequest, "REGISTRY_INVALID_REQUEST", "Тело запроса — JSON-объект до 64 КБ.")
		return false
	}
	return true
}

// Search handles GET /api/v1/tender-registry/search?q= — готовое сообщение:
// «не нашёл», карточка (одно совпадение) или список кнопками.
func (h *TenderRegistryBotHandler) Search(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.Search(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		h.fail(w, r, err, "registry search failed")
		return
	}
	middleware.SetCallItems(r.Context(), len(res.Items), false)
	renderRegistry(w, r, http.StatusOK, res)
}

// Card handles GET /api/v1/tender-registry/{id}/card.
func (h *TenderRegistryBotHandler) Card(w http.ResponseWriter, r *http.Request) {
	id, ok := registryID(w, r)
	if !ok {
		return
	}
	res, err := h.svc.Card(r.Context(), id)
	if err != nil {
		h.fail(w, r, err, "registry card failed")
		return
	}
	middleware.SetCallItems(r.Context(), 1, false)
	renderRegistry(w, r, http.StatusOK, res)
}

type registryClaimRequest struct {
	ChatID int64 `json:"chat_id"`
}

// ClaimReminders handles POST /api/v1/tender-registry/reminders/claim —
// напоминания, которые пора отправить в чат и которые ему ещё не выдавались.
func (h *TenderRegistryBotHandler) ClaimReminders(w http.ResponseWriter, r *http.Request) {
	var req registryClaimRequest
	if !decodeRegistryBody(w, r, &req) {
		return
	}
	res, err := h.svc.ClaimReminders(r.Context(), req.ChatID)
	if err != nil {
		h.fail(w, r, err, "registry reminders claim failed")
		return
	}
	middleware.SetCallItems(r.Context(), len(res.Items), false)
	renderRegistry(w, r, http.StatusOK, res)
}

type registryChronologyRequest struct {
	Text string `json:"text"`
	Type string `json:"type"`
	Date string `json:"date"`
}

// AppendChronology handles POST /api/v1/tender-registry/{id}/chronology:
// 201 — запись добавлена, 200 — такая запись уже была (повтор запроса).
func (h *TenderRegistryBotHandler) AppendChronology(w http.ResponseWriter, r *http.Request) {
	id, ok := registryID(w, r)
	if !ok {
		return
	}
	var req registryChronologyRequest
	if !decodeRegistryBody(w, r, &req) {
		return
	}
	res, err := h.svc.AppendChronology(r.Context(), id, req.Text, req.Type, req.Date)
	if err != nil {
		h.fail(w, r, err, "registry chronology append failed")
		return
	}
	status, items := http.StatusOK, 0
	if res.Created {
		status, items = http.StatusCreated, 1
	}
	middleware.SetCallItems(r.Context(), items, false)
	renderRegistry(w, r, status, res)
}
