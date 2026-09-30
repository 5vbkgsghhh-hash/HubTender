package services

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/su10/hubtender/backend/internal/notify/telegram"
	"github.com/su10/hubtender/backend/internal/repository"
)

// TenderRegistryBotService — «Перечень тендеров» для внешнего Telegram-бота
// (@SU10TenderNotes_BOT) по машинному ключу: поиск и карточка тендера, выдача
// положенных напоминаний, запись звонка/события в хронологию. Сообщения
// отдаются готовыми к sendMessage — правила сроков, тексты и склонения живут
// здесь, бот только доставляет (api/REGISTRY.md).
type TenderRegistryBotService struct {
	store      tenderRegistryBotStore
	loc        *time.Location
	appBaseURL string
	now        func() time.Time
}

type tenderRegistryBotStore interface {
	ListTenders(ctx context.Context) ([]repository.RegistryTender, error)
	GetTender(ctx context.Context, id string) (*repository.RegistryTender, error)
	ClaimReminders(ctx context.Context, chatID int64, keys []repository.ReminderKey, pruneBefore string) ([]repository.ReminderKey, error)
	AppendChronology(ctx context.Context, registryID string, e repository.ChronologyEntry) (string, bool, error)
}

func NewTenderRegistryBotService(store tenderRegistryBotStore, appBaseURL string) *TenderRegistryBotService {
	return &TenderRegistryBotService{store: store, loc: moscowLocation(), appBaseURL: appBaseURL, now: time.Now}
}

// Окно выдачи напоминаний по Москве: ночью в чат не пишем.
const (
	reminderSendFrom  = 9 * time.Hour
	reminderSendUntil = 21 * time.Hour
	// reminderJournalDays — сколько дней хранить журнал выданных напоминаний.
	reminderJournalDays = 90
	// Запрос поиска и окно даты записи хронологии.
	maxSearchQueryRunes = 100
	chronologyMaxAge    = 30 * 24 * time.Hour
	chronologyMaxAhead  = 10 * time.Minute
)

// moscowLocation — Europe/Moscow; без базы часовых поясов — фиксированный UTC+3.
func moscowLocation() *time.Location {
	if loc, err := time.LoadLocation("Europe/Moscow"); err == nil {
		return loc
	}
	return time.FixedZone("MSK", 3*3600)
}

// RegistryInputError — неверный запрос бота (пустой поиск, текст, тип, дата,
// chat_id); текст объясняет, что исправить.
type RegistryInputError struct{ Reason string }

func (e *RegistryInputError) Error() string { return e.Reason }

func registryInput(format string, args ...any) error {
	return &RegistryInputError{Reason: fmt.Sprintf(format, args...)}
}

// RegistryTenderBrief — тендер в ответах API.
type RegistryTenderBrief struct {
	ID              string `json:"id"`
	Number          string `json:"number,omitempty"`
	Title           string `json:"title"`
	Client          string `json:"client"`
	Status          string `json:"status"`           // подпись статуса, как на странице
	DashboardStatus string `json:"dashboard_status"` // calc | sent | waiting_pd | archive
}

func brief(t repository.RegistryTender) RegistryTenderBrief {
	status := ResolveDashboardStatus(t.StatusName, t.DashboardStatus, t.IsArchived)
	return RegistryTenderBrief{
		ID: t.ID, Number: strings.TrimSpace(strOrEmpty(t.TenderNumber)), Title: t.Title, Client: t.ClientName,
		Status: statusLabel(t.StatusName, status), DashboardStatus: status,
	}
}

// RegistrySearchResult — ответ поиска: всегда одно сообщение для отправки —
// «не нашёл», карточка (одно совпадение) или список кнопками.
type RegistrySearchResult struct {
	Query   string                  `json:"query"`
	Total   int                     `json:"total"`
	Items   []RegistryTenderBrief   `json:"items"`
	Message telegram.MessagePayload `json:"message"`
}

// Search — тендеры по номеру, названию или заказчику.
func (s *TenderRegistryBotService) Search(ctx context.Context, query string) (*RegistrySearchResult, error) {
	q := strings.TrimSpace(query)
	if n := utf8.RuneCountInString(q); n == 0 || n > maxSearchQueryRunes {
		return nil, registryInput("q: нужен запрос от 1 до %d символов", maxSearchQueryRunes)
	}
	tenders, err := s.store.ListTenders(ctx)
	if err != nil {
		return nil, err
	}
	found := matchTenders(tenders, q)
	res := &RegistrySearchResult{Query: q, Total: len(found), Items: []RegistryTenderBrief{}}
	shown := found[:min(len(found), telegram.MaxSearchResults)]
	views := make([]telegram.TenderView, 0, len(shown))
	for _, t := range shown {
		res.Items = append(res.Items, brief(t))
		views = append(views, telegram.TenderView{ID: t.ID, Number: strOrEmpty(t.TenderNumber), Title: t.Title, Client: t.ClientName})
	}
	switch len(found) {
	case 0:
		res.Message = telegram.Payload(telegram.RenderNotFound(q), nil)
	case 1:
		res.Message = s.cardMessage(found[0])
	default:
		res.Message = telegram.Payload(telegram.RenderSearchResults(q, views, len(found)))
	}
	return res, nil
}

// RegistryCard — карточка тендера.
type RegistryCard struct {
	Tender  RegistryTenderBrief     `json:"tender"`
	Message telegram.MessagePayload `json:"message"`
}

// Card — карточка тендера по id строки перечня.
func (s *TenderRegistryBotService) Card(ctx context.Context, id string) (*RegistryCard, error) {
	t, err := s.store.GetTender(ctx, id)
	if err != nil {
		return nil, err
	}
	return &RegistryCard{Tender: brief(*t), Message: s.cardMessage(*t)}, nil
}

func (s *TenderRegistryBotService) cardMessage(t repository.RegistryTender) telegram.MessagePayload {
	entries := parseChronology(t.Chronology, s.loc)
	return telegram.Payload(telegram.RenderTenderCard(s.tenderView(t, entries, s.now(), 3)))
}

// ReminderWindow — окно выдачи напоминаний.
type ReminderWindow struct {
	From     string `json:"from"`
	Until    string `json:"until"`
	Timezone string `json:"timezone"`
	Open     bool   `json:"open"`
}

// RegistryReminder — напоминание, которое бот должен отправить.
type RegistryReminder struct {
	Kind    string                  `json:"kind"` // call | waiting_pd
	DueOn   string                  `json:"due_on"`
	Tender  RegistryTenderBrief     `json:"tender"`
	Message telegram.MessagePayload `json:"message"`
}

// RegistryReminders — ответ выдачи напоминаний.
type RegistryReminders struct {
	Window ReminderWindow     `json:"window"`
	Items  []RegistryReminder `json:"items"`
}

// ClaimReminders — положенные сейчас напоминания, которые чату chatID ещё не
// выдавались. Выданное запоминается: повторный вызов в тот же день вернёт
// пусто, поэтому бот может спрашивать хоть каждые 10 минут. Вне окна
// 09:00–21:00 по Москве — пусто.
func (s *TenderRegistryBotService) ClaimReminders(ctx context.Context, chatID int64) (*RegistryReminders, error) {
	if chatID == 0 {
		return nil, registryInput("chat_id: нужен id чата, куда бот отправит напоминания")
	}
	now := s.now()
	res := &RegistryReminders{
		Window: ReminderWindow{From: "09:00", Until: "21:00", Timezone: "Europe/Moscow", Open: s.inWindow(now)},
		Items:  []RegistryReminder{},
	}
	if !res.Window.Open {
		return res, nil
	}
	tenders, err := s.store.ListTenders(ctx)
	if err != nil {
		return nil, err
	}
	type due struct {
		key     repository.ReminderKey
		tender  repository.RegistryTender
		entries []chronoEntry
	}
	var dues []due
	keys := []repository.ReminderKey{}
	for _, t := range tenders {
		entries := parseChronology(t.Chronology, s.loc)
		if kind, dueOn, ok := s.dueReminder(t, entries, now); ok {
			k := repository.ReminderKey{RegistryID: t.ID, Kind: kind, DueOn: dueOn}
			dues = append(dues, due{k, t, entries})
			keys = append(keys, k)
		}
	}
	claimed, err := s.store.ClaimReminders(ctx, chatID, keys, localDate(now.AddDate(0, 0, -reminderJournalDays), s.loc))
	if err != nil {
		return nil, err
	}
	fresh := make(map[repository.ReminderKey]bool, len(claimed))
	for _, k := range claimed {
		fresh[k] = true
	}
	for _, d := range dues { // порядок перечня, а не порядок RETURNING
		if fresh[d.key] {
			res.Items = append(res.Items, RegistryReminder{
				Kind: d.key.Kind, DueOn: d.key.DueOn, Tender: brief(d.tender),
				Message: s.reminderMessage(d.tender, d.key.Kind, d.entries, now),
			})
		}
	}
	return res, nil
}

func (s *TenderRegistryBotService) inWindow(now time.Time) bool {
	l := now.In(s.loc)
	sinceMidnight := time.Duration(l.Hour())*time.Hour + time.Duration(l.Minute())*time.Minute
	return sinceMidnight >= reminderSendFrom && sinceMidnight < reminderSendUntil
}

// dueReminder — положенное тендеру напоминание: вид и дата срока (YYYY-MM-DD).
// «Направлено» — на каждый день, пока звонок просрочен; «Ожидание ПД» — на
// «месячную годовщину» последней записи.
func (s *TenderRegistryBotService) dueReminder(t repository.RegistryTender, entries []chronoEntry, now time.Time) (kind, dueOn string, ok bool) {
	switch ResolveDashboardStatus(t.StatusName, t.DashboardStatus, t.IsArchived) {
	case dashSent:
		if evalCall(t, entries, now, s.loc).Due {
			return repository.ReminderKindCall, localDate(now, s.loc), true
		}
	case dashWaitingPD:
		if p := evalPD(t, entries, now, s.loc); p.Due {
			return repository.ReminderKindWaitingPD, p.DueOn.Format("2006-01-02"), true
		}
	}
	return "", "", false
}

func (s *TenderRegistryBotService) reminderMessage(t repository.RegistryTender, kind string, entries []chronoEntry, now time.Time) telegram.MessagePayload {
	view := s.tenderView(t, entries, now, 1)
	if kind == repository.ReminderKindCall {
		ev := evalCall(t, entries, now, s.loc)
		return telegram.Payload(telegram.RenderCallReminder(telegram.CallReminder{
			Tender: view, Days: ev.Days, Threshold: callThresholdDays, Since: fmtDate(&ev.Since, s.loc), FromCall: ev.FromCall,
		}))
	}
	ev := evalPD(t, entries, now, s.loc)
	return telegram.Payload(telegram.RenderPDReminder(telegram.PDReminder{
		Tender: view, Months: ev.Months, Since: fmtDate(&ev.Anchor, s.loc), FromCreated: ev.FromCreated,
	}))
}

// RegistryChronologyResult — итог записи в хронологию.
type RegistryChronologyResult struct {
	Created bool                       `json:"created"` // false — такая запись уже была (повтор запроса)
	Title   string                     `json:"title"`
	Entry   repository.ChronologyEntry `json:"entry"`
	Message telegram.MessagePayload    `json:"message"`
}

// AppendChronology дописывает звонок (call_follow_up) или событие (default) в
// хронологию. date — время ответа пользователя в Telegram: по нему и тексту
// повтор того же запроса распознаётся и второй записи не даёт.
func (s *TenderRegistryBotService) AppendChronology(ctx context.Context, registryID, text, entryType, date string) (*RegistryChronologyResult, error) {
	text = strings.TrimSpace(text)
	if n := utf8.RuneCountInString(text); n == 0 || n > maxChronologyTextRunes {
		return nil, registryInput("text: нужен текст от 1 до %d символов", maxChronologyTextRunes)
	}
	if entryType != "call_follow_up" && entryType != "default" {
		return nil, registryInput("type: ожидается call_follow_up (звонок) или default (событие)")
	}
	at, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(date))
	if err != nil {
		return nil, registryInput("date: ожидается время в формате RFC3339, например 2026-09-30T09:15:00Z")
	}
	if now := s.now(); at.Before(now.Add(-chronologyMaxAge)) || at.After(now.Add(chronologyMaxAhead)) {
		return nil, registryInput("date: время записи должно быть не старше 30 дней и не в будущем")
	}
	entry := repository.ChronologyEntry{Date: chronologyDate(at), Text: text, Type: entryType}
	title, created, err := s.store.AppendChronology(ctx, registryID, entry)
	if err != nil {
		return nil, err
	}
	return &RegistryChronologyResult{
		Created: created, Title: title, Entry: entry,
		Message: telegram.Payload(telegram.RenderEntrySaved(title, entryType == "call_follow_up", text), nil),
	}, nil
}

// tenderView — данные тендера для сообщения; lastN — сколько записей хронологии показать.
func (s *TenderRegistryBotService) tenderView(t repository.RegistryTender, entries []chronoEntry, now time.Time, lastN int) telegram.TenderView {
	loc := s.loc
	status := ResolveDashboardStatus(t.StatusName, t.DashboardStatus, t.IsArchived)
	v := telegram.TenderView{
		ID: t.ID, Number: strings.TrimSpace(strOrEmpty(t.TenderNumber)), Title: t.Title, Client: t.ClientName,
		Status: statusLabel(t.StatusName, status), Scope: strOrEmpty(t.ScopeName),
		Area: fmtAmount(t.Area, "м²"), Cost: fmtAmount(t.TotalCost, "₽"),
		Submission: fmtDateTime(t.SubmissionDate, loc), Invitation: fmtDate(t.InvitationDate, loc),
		SiteStart: fmtDate(t.ConstructionStart, loc), Commission: fmtDate(t.CommissionDate, loc),
		Address: strOrEmpty(t.ObjectAddress), Package: packageSummary(t.TenderPackage),
		Entries: latestEntries(entries, lastN, loc),
	}
	if status == dashSent {
		if ev := evalCall(t, entries, now, loc); ev.Known {
			from := "подачи КП"
			if ev.FromCall {
				from = "последнего звонка"
			}
			v.CallStatus = fmt.Sprintf("%s с %s (%s)", telegram.PluralRu(ev.Days, "день", "дня", "дней"), from, fmtDate(&ev.Since, loc))
			if ev.Due {
				v.CallStatus += " — пора звонить"
			}
		}
	}
	if strings.HasPrefix(s.appBaseURL, "https://") {
		v.Link = s.appBaseURL + "/tenders"
	}
	return v
}
