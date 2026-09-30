package services

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/su10/hubtender/backend/internal/notify/telegram"
	"github.com/su10/hubtender/backend/internal/repository"
)

type fakeReminderRow struct {
	status   string
	attempts int
	deferred bool
}

type fakePrompt struct{ registryID, entryType string }

type fakeTenderBotStore struct {
	tenders   []repository.BotTender
	reminders map[repository.ReminderKey]*fakeReminderRow
	order     []repository.ReminderKey
	prompts   map[[2]int64]fakePrompt
	appended  []repository.ChronologyEntry
	offset    int64
}

func newFakeTenderBotStore() *fakeTenderBotStore {
	return &fakeTenderBotStore{reminders: map[repository.ReminderKey]*fakeReminderRow{}, prompts: map[[2]int64]fakePrompt{}}
}

func (f *fakeTenderBotStore) ListTenders(context.Context) ([]repository.BotTender, error) {
	return f.tenders, nil
}
func (f *fakeTenderBotStore) GetTender(_ context.Context, id string) (*repository.BotTender, error) {
	for _, t := range f.tenders {
		if t.ID == id {
			return &t, nil
		}
	}
	return nil, repository.ErrTenderRegistryNotFound
}
func (f *fakeTenderBotStore) EnqueueReminders(_ context.Context, keys []repository.ReminderKey) (int64, error) {
	var n int64
	for _, k := range keys {
		if _, ok := f.reminders[k]; !ok {
			f.reminders[k] = &fakeReminderRow{status: "pending"}
			f.order = append(f.order, k)
			n++
		}
	}
	return n, nil
}
func (f *fakeTenderBotStore) ClaimReminders(context.Context, int, time.Duration) ([]repository.ClaimedReminder, error) {
	var out []repository.ClaimedReminder
	for _, k := range f.order {
		if r := f.reminders[k]; r.status == "pending" && !r.deferred {
			r.attempts++
			out = append(out, repository.ClaimedReminder{ReminderKey: k, Attempts: r.attempts})
		}
	}
	return out, nil
}
func (f *fakeTenderBotStore) FinishReminder(_ context.Context, k repository.ReminderKey, status string, _ int64, _ string) error {
	f.reminders[k].status = status
	return nil
}
func (f *fakeTenderBotStore) DeferReminder(_ context.Context, k repository.ReminderKey, _ string, _ time.Duration) error {
	f.reminders[k].deferred = true
	return nil
}
func (f *fakeTenderBotStore) PruneReminders(context.Context, time.Duration) (int64, error) {
	return 0, nil
}
func (f *fakeTenderBotStore) InsertPrompt(_ context.Context, chat, msgID int64, registryID, entryType string, _ time.Duration) error {
	f.prompts[[2]int64{chat, msgID}] = fakePrompt{registryID, entryType}
	return nil
}
func (f *fakeTenderBotStore) ConsumePrompt(_ context.Context, chat, msgID int64, text, date string) (*repository.ConsumedPrompt, error) {
	p, ok := f.prompts[[2]int64{chat, msgID}]
	if !ok {
		return nil, repository.ErrNoPrompt
	}
	delete(f.prompts, [2]int64{chat, msgID})
	t, err := f.GetTender(context.Background(), p.registryID)
	if err != nil {
		return nil, err
	}
	f.appended = append(f.appended, repository.ChronologyEntry{Date: date, Text: text, Type: p.entryType})
	return &repository.ConsumedPrompt{Title: t.Title, EntryType: p.entryType}, nil
}
func (f *fakeTenderBotStore) GetOffset(context.Context) (int64, error)   { return f.offset, nil }
func (f *fakeTenderBotStore) SetOffset(_ context.Context, o int64) error { f.offset = o; return nil }

type tbSent struct {
	chat, replyTo int64
	html          string
	kb            [][]telegram.InlineButton
}

type fakeTenderBotClient struct {
	sent    []tbSent
	prompts []tbSent
	answers []string
	errs    []error // ошибки SendMessage по очереди; nil — успех
	nextID  int64
	updates func() ([]telegram.Update, error)
}

func (c *fakeTenderBotClient) GetUpdates(context.Context, int64, int) ([]telegram.Update, error) {
	return c.updates()
}
func (c *fakeTenderBotClient) send(chat, replyTo int64, html string, kb [][]telegram.InlineButton) (int64, error) {
	if len(c.errs) > 0 {
		err := c.errs[0]
		c.errs = c.errs[1:]
		if err != nil {
			return 0, err
		}
	}
	c.sent = append(c.sent, tbSent{chat, replyTo, html, kb})
	c.nextID++
	return c.nextID, nil
}
func (c *fakeTenderBotClient) SendMessage(_ context.Context, chat int64, html string, kb [][]telegram.InlineButton) (int64, error) {
	return c.send(chat, 0, html, kb)
}
func (c *fakeTenderBotClient) SendReply(_ context.Context, chat, replyTo int64, html string, kb [][]telegram.InlineButton) (int64, error) {
	return c.send(chat, replyTo, html, kb)
}
func (c *fakeTenderBotClient) SendForceReply(_ context.Context, chat int64, html, _ string) (int64, error) {
	c.nextID++
	c.prompts = append(c.prompts, tbSent{chat: chat, html: html})
	return c.nextID, nil
}
func (c *fakeTenderBotClient) AnswerCallback(_ context.Context, _ string, text string) error {
	c.answers = append(c.answers, text)
	return nil
}

func (c *fakeTenderBotClient) last() tbSent {
	if len(c.sent) == 0 {
		return tbSent{}
	}
	return c.sent[len(c.sent)-1]
}

const (
	tbSentID  = "0f8fad5b-d9cb-469f-a165-70867728950e"
	tbPDID    = "1f8fad5b-d9cb-469f-a165-70867728950e"
	tbArchID  = "2f8fad5b-d9cb-469f-a165-70867728950e"
	tbTeam    = int64(-100500)
	tbOther   = int64(-100777)
	tbBotName = "tenders_bot"
)

var tbBotUser = &telegram.User{ID: 1, IsBot: true, Username: tbBotName}

// tbFixture — «Направлено» с подачей 22.09, «Ожидание ПД» с записью 30.08, архив.
func tbFixture() (*TenderBotService, *fakeTenderBotStore, *fakeTenderBotClient, *time.Time) {
	store, client := newFakeTenderBotStore(), &fakeTenderBotClient{}
	store.tenders = []repository.BotTender{
		{ID: tbSentID, Title: "ЖК Ода", ClientName: "ООО Ромашка", TenderNumber: tbPtr("330"), DashboardStatus: tbPtr("sent"),
			SubmissionDate: tbPtr(tbAt(2026, 9, 22, 12, 0)),
			Chronology:     []byte(`[{"date":"2026-09-20T09:00:00.000Z","text":"Отправили КП","type":"default"}]`)},
		{ID: tbPDID, Title: "Башня", ClientName: "АО Стройка", StatusName: tbPtr("Ожидаем тендерный пакет"),
			CreatedAt: tbPtr(tbAt(2026, 2, 11, 12, 0)), Chronology: []byte(`[{"date":"2026-08-30T09:00:00.000Z","text":"Ждём ПД"}]`)},
		{ID: tbArchID, Title: "Архивный", ClientName: "Кто-то", StatusName: tbPtr("Проиграли"),
			SubmissionDate: tbPtr(tbAt(2026, 1, 1, 12, 0))},
	}
	cfg := DefaultTenderBotConfig()
	cfg.ChatID, cfg.BotUsername, cfg.Location, cfg.ChatGap, cfg.AppBaseURL = tbTeam, tbBotName, tbMSK, 0, "https://tender.example"
	svc := NewTenderBotService(store, client, cfg, zerolog.Nop())
	now := tbAt(2026, 9, 30, 10, 0)
	svc.now = func() time.Time { return now }
	return svc, store, client, &now
}

func TestTenderBotRunOnceWindowAndDedup(t *testing.T) {
	svc, store, client, now := tbFixture()
	ctx := context.Background()

	*now = tbAt(2026, 9, 30, 8, 59)
	svc.RunOnce(ctx)
	if len(client.sent) != 0 || len(store.reminders) != 0 {
		t.Fatalf("до 9:00 ничего не шлём: %d/%d", len(client.sent), len(store.reminders))
	}

	*now = tbAt(2026, 9, 30, 9, 0)
	svc.RunOnce(ctx)
	if len(client.sent) != 2 {
		t.Fatalf("ожидалось 2 сообщения в чат (звонок и ПД), отправлено %d", len(client.sent))
	}
	for _, m := range client.sent {
		if m.chat != tbTeam {
			t.Fatalf("напоминание ушло не в чат команды: %d", m.chat)
		}
	}
	call, pd := client.sent[0], client.sent[1]
	if !strings.Contains(call.html, "прошло более 7 дней: 8 дней с подачи КП (22.09.2026)") || !strings.Contains(call.html, "Отправили КП") {
		t.Fatalf("напоминание о звонке: %s", call.html)
	}
	if len(call.kb) != 2 || call.kb[0][0].CallbackData != "tc:"+tbSentID || call.kb[1][0].URL != "https://tender.example/tenders" {
		t.Fatalf("кнопки напоминания: %+v", call.kb)
	}
	if !strings.Contains(pd.html, "прошёл 1 месяц с последней записи в хронологии (30.08.2026)") {
		t.Fatalf("напоминание ПД: %s", pd.html)
	}

	*now = tbAt(2026, 9, 30, 15, 0)
	svc.RunOnce(ctx)
	if len(client.sent) != 2 {
		t.Fatalf("в тот же день напоминания не повторяются: %d", len(client.sent))
	}

	*now = tbAt(2026, 10, 1, 9, 10)
	svc.RunOnce(ctx)
	if len(client.sent) != 3 || !strings.Contains(client.last().html, "Надо позвонить") {
		t.Fatalf("на следующий день — снова только «Направлено», всего %d", len(client.sent))
	}

	store.reminders, store.order = map[repository.ReminderKey]*fakeReminderRow{}, nil
	*now = tbAt(2026, 10, 1, 21, 0)
	svc.RunOnce(ctx)
	svc.cfg.ChatID, *now = 0, tbAt(2026, 10, 1, 10, 0)
	svc.RunOnce(ctx)
	if len(client.sent) != 3 || len(store.reminders) != 0 {
		t.Fatal("после 21:00 и без TENDER_BOT_CHAT_ID не шлём")
	}
}

func TestTenderBotSendReminderOutcomes(t *testing.T) {
	today := repository.ReminderKey{ChatID: tbTeam, RegistryID: tbSentID, Kind: repository.ReminderKindCall, DueOn: "2026-09-30"}
	other := today
	other.ChatID = tbOther
	yesterday := today
	yesterday.DueOn = "2026-09-29"
	cases := []struct {
		name     string
		key      repository.ReminderKey
		mutate   func(*fakeTenderBotStore)
		errs     []error
		attempts int
		status   string
		deferred bool
		goOn     bool
	}{
		{"доставлено", today, nil, nil, 1, "sent", false, true},
		{"вчерашнее — устарело", yesterday, nil, nil, 1, "skipped", false, true},
		{"чат бота сменился", other, nil, nil, 1, "skipped", false, true},
		{"звонок уже отмечен", today, func(s *fakeTenderBotStore) {
			s.tenders[0].Chronology = []byte(`[{"date":"2026-09-30T05:00:00.000Z","text":"позвонили","type":"call_follow_up"}]`)
		}, nil, 1, "skipped", false, true},
		{"статус сменился", today, func(s *fakeTenderBotStore) { s.tenders[0].StatusName = tbPtr("Выиграли") }, nil, 1, "skipped", false, true},
		{"тендер удалён", today, func(s *fakeTenderBotStore) { s.tenders = s.tenders[1:] }, nil, 1, "skipped", false, true},
		{"бота удалили из чата", today, nil, []error{&telegram.APIError{Code: 403}}, 1, "failed", false, true},
		{"чат стал супергруппой", today, nil, []error{&telegram.APIError{Code: 400, MigrateToChatID: -100999}}, 1, "failed", false, true},
		{"429 — ждём", today, nil, []error{&telegram.APIError{Code: 429, RetryAfter: 3}}, 1, "pending", true, false},
		{"сеть — повтор", today, nil, []error{telegram.ErrTransport}, 1, "pending", true, true},
		{"сеть — попытки кончились", today, nil, []error{telegram.ErrTransport}, tenderReminderMaxAttempts, "failed", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store, client, now := tbFixture()
			if tc.mutate != nil {
				tc.mutate(store)
			}
			client.errs = tc.errs
			store.reminders[tc.key] = &fakeReminderRow{status: "pending"}
			byID := map[string]repository.BotTender{}
			for _, tr := range store.tenders {
				byID[tr.ID] = tr
			}
			var lastSent time.Time
			goOn := svc.sendReminder(context.Background(), repository.ClaimedReminder{ReminderKey: tc.key, Attempts: tc.attempts},
				byID, *now, &lastSent)
			row := store.reminders[tc.key]
			if row.status != tc.status || row.deferred != tc.deferred || goOn != tc.goOn {
				t.Fatalf("статус %q, отложено %v, дальше %v", row.status, row.deferred, goOn)
			}
		})
	}
}

func tbPress(svc *TenderBotService, chat int64, data string) {
	svc.handleUpdate(context.Background(), telegram.Update{CallbackQuery: &telegram.CallbackQuery{
		ID: "cb", Data: data, From: telegram.User{ID: 42, FirstName: "Иван"},
		Message: &telegram.Message{MessageID: 5, Chat: telegram.Chat{ID: chat, Type: "supergroup"}},
	}})
}

func tbSay(svc *TenderBotService, chat, msgID int64, text string, replyTo *telegram.Message) {
	chatType := "supergroup"
	if chat > 0 {
		chatType = "private"
	}
	svc.handleUpdate(context.Background(), telegram.Update{Message: &telegram.Message{
		MessageID: msgID, Chat: telegram.Chat{ID: chat, Type: chatType}, Text: text, ReplyToMessage: replyTo,
		From: &telegram.User{ID: 42, FirstName: "Иван"},
	}})
}

func TestTenderBotCallAndEntryFlow(t *testing.T) {
	svc, store, client, _ := tbFixture()

	tbPress(svc, tbTeam, "tc:"+tbSentID)
	if len(client.prompts) != 1 || !strings.Contains(client.prompts[0].html, "Звонок") ||
		!strings.Contains(client.prompts[0].html, `<a href="tg://user?id=42">Иван</a>, ответьте на это сообщение`) {
		t.Fatalf("запрос текста: %+v", client.prompts)
	}
	promptID := client.nextID
	if p, ok := store.prompts[[2]int64{tbTeam, promptID}]; !ok || p.entryType != "call_follow_up" || p.registryID != tbSentID {
		t.Fatalf("запрос записи: %+v", store.prompts)
	}
	if client.answers[0] != "Ответьте на сообщение бота текстом записи" {
		t.Fatal(client.answers)
	}

	prompt := &telegram.Message{MessageID: promptID, Chat: telegram.Chat{ID: tbTeam}, From: tbBotUser}
	tbSay(svc, tbTeam, 10, "  Позвонили, ждут решения  ", prompt)
	if len(store.appended) != 1 || store.appended[0].Text != "Позвонили, ждут решения" || store.appended[0].Type != "call_follow_up" ||
		!strings.HasSuffix(store.appended[0].Date, "Z") {
		t.Fatalf("запись в хронологию: %+v", store.appended)
	}
	if m := client.last(); m.replyTo != 10 || !strings.Contains(m.html, "✅ Записал в хронологию «ЖК Ода»: 📞 Звонок") {
		t.Fatalf("подтверждение: %+v", m)
	}

	// Второй ответ на тот же запрос (или повторная доставка) записи не задваивает.
	tbSay(svc, tbTeam, 10, "Позвонили, ждут решения", prompt)
	if len(store.appended) != 1 || client.last().html != telegram.TenderReplyHint {
		t.Fatalf("повторный ответ: %d / %s", len(store.appended), client.last().html)
	}

	reminder := &telegram.Message{MessageID: 3, Chat: telegram.Chat{ID: tbTeam}, From: tbBotUser}
	tbSay(svc, tbTeam, 11, "позвонил", reminder)
	if len(store.appended) != 1 || client.last().html != telegram.TenderReplyHint {
		t.Fatal("ответ на напоминание — подсказка про кнопки")
	}

	sent := len(client.sent)
	tbSay(svc, tbTeam, 12, "согласен", &telegram.Message{MessageID: 4, From: &telegram.User{ID: 7, FirstName: "Пётр"}})
	if len(client.sent) != sent {
		t.Fatal("бот ответил на переписку людей")
	}

	tbPress(svc, tbTeam, "te:"+tbPDID)
	eventPrompt := &telegram.Message{MessageID: client.nextID, From: tbBotUser}
	tbSay(svc, tbTeam, 20, "", eventPrompt)
	if !strings.Contains(client.last().html, "Нужен текст") {
		t.Fatal("фото вместо текста")
	}
	tbSay(svc, tbTeam, 21, strings.Repeat("я", maxChronologyTextRunes+1), eventPrompt)
	if !strings.Contains(client.last().html, "Слишком длинный") {
		t.Fatal("длинный текст")
	}
	tbSay(svc, tbTeam, 22, "Прислали ПД", eventPrompt)
	if len(store.appended) != 2 || store.appended[1].Type != "default" || !strings.Contains(client.last().html, "📝 Событие — Прислали ПД") {
		t.Fatalf("событие: %+v / %s", store.appended, client.last().html)
	}
}

func TestTenderBotCommands(t *testing.T) {
	svc, _, client, _ := tbFixture()

	tbSay(svc, tbTeam, 1, "/t 330", nil)
	card := client.last()
	if card.replyTo != 1 {
		t.Fatalf("карточка должна отвечать на команду: %+v", card)
	}
	for _, want := range []string{"🏗 <b>ЖК Ода</b> · №330", "Заказчик: ООО Ромашка", "Статус: Направлено",
		"Контроль звонка: 8 дней с подачи КП (22.09.2026) — пора звонить", "Подача КП: 22.09.2026 12:00", "Отправили КП"} {
		if !strings.Contains(card.html, want) {
			t.Fatalf("в карточке нет %q:\n%s", want, card.html)
		}
	}

	tbSay(svc, tbTeam, 2, "/t@"+tbBotName+" о", nil) // буква есть в нескольких тендерах — список кнопками
	if list := client.last(); !strings.Contains(list.html, "Найдено") || len(list.kb) < 2 || !strings.HasPrefix(list.kb[0][0].CallbackData, "ti:") {
		t.Fatalf("список найденного: %+v", list)
	}
	tbSay(svc, tbTeam, 3, "/t", nil)
	if client.last().html != telegram.TenderSearchUsage {
		t.Fatal("подсказка /t")
	}
	tbSay(svc, tbTeam, 4, "/t нет такого", nil)
	if !strings.Contains(client.last().html, "нет совпадений") {
		t.Fatal("ничего не найдено")
	}
	tbSay(svc, tbTeam, 5, "/help", nil)
	if client.last().html != telegram.TenderBotHelp {
		t.Fatal("справка")
	}
	tbSay(svc, tbTeam, 6, "/chatid@"+strings.ToUpper(tbBotName), nil)
	if !strings.Contains(client.last().html, "<code>-100500</code>") {
		t.Fatal("id чата")
	}

	sent := len(client.sent)
	tbSay(svc, tbTeam, 7, "/t@other_bot 330", nil)
	tbSay(svc, tbTeam, 8, "ода", nil)
	tbSay(svc, tbTeam, 9, "/unknown", nil)
	if len(client.sent) != sent {
		t.Fatal("бот ответил на чужую команду или обычный текст")
	}

	tbPress(svc, tbTeam, "ti:"+tbPDID)
	if m := client.last(); m.replyTo != 0 || !strings.Contains(m.html, "Башня") || !strings.Contains(m.html, "Ожидаем тендерный пакет") {
		t.Fatalf("карточка по кнопке: %+v", m)
	}
	if client.answers[len(client.answers)-1] != "" {
		t.Fatal("кнопка карточки закрывается без всплывашки")
	}
}

func TestTenderBotOutsideTeamChat(t *testing.T) {
	svc, store, client, _ := tbFixture()

	tbSay(svc, 42, 1, "/t 330", nil) // личные сообщения
	tbSay(svc, tbOther, 2, "ода", nil)
	if len(client.sent) != 0 {
		t.Fatal("бот ответил в личке или на обычный текст в чужом чате")
	}
	tbSay(svc, tbOther, 3, "/t 330", nil)
	if m := client.last(); !strings.Contains(m.html, "<code>-100777</code>") || strings.Contains(m.html, "ЖК Ода") {
		t.Fatalf("в чужом чате — только id чата: %+v", m)
	}
	tbPress(svc, tbOther, "tc:"+tbSentID)
	if client.answers[0] != "Бот работает только в чате команды" || len(store.prompts) != 0 {
		t.Fatalf("кнопка из чужого чата: %v", client.answers)
	}
	tbPress(svc, tbTeam, "a:"+tbSentID)
	tbPress(svc, tbTeam, "ti:"+tbArchID[:35]+"f")
	if client.answers[1] != "Кнопка устарела" || client.answers[2] != "Тендер удалён из перечня" {
		t.Fatalf("битая кнопка / удалённый тендер: %v", client.answers)
	}

	svc.cfg.ChatID = 0 // чат ещё не настроен: бот только подсказывает id
	tbSay(svc, tbTeam, 4, "/t 330", nil)
	if m := client.last(); !strings.Contains(m.html, "TENDER_BOT_CHAT_ID") || strings.Contains(m.html, "ЖК Ода") {
		t.Fatalf("без TENDER_BOT_CHAT_ID: %+v", m)
	}
}

func TestTenderBotPollerSavesOffset(t *testing.T) {
	svc, store, client, _ := tbFixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	client.updates = func() ([]telegram.Update, error) {
		calls++
		if calls > 1 {
			cancel()
			return nil, ctx.Err()
		}
		return []telegram.Update{{UpdateID: 77, Message: &telegram.Message{
			MessageID: 1, Chat: telegram.Chat{ID: tbTeam, Type: "group"}, Text: "/help",
		}}}, nil
	}
	svc.RunPoller(ctx)
	if store.offset != 78 || client.last().html != telegram.TenderBotHelp {
		t.Fatalf("offset %d, ответ %q", store.offset, client.last().html)
	}
}
