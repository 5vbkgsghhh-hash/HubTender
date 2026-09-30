package services

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/su10/hubtender/backend/internal/repository"
)

type fakeRegistryStore struct {
	tenders     []repository.RegistryTender
	claimed     map[int64]map[repository.ReminderKey]bool
	appended    []repository.ChronologyEntry
	pruneBefore string
}

func (f *fakeRegistryStore) ListTenders(context.Context) ([]repository.RegistryTender, error) {
	return f.tenders, nil
}
func (f *fakeRegistryStore) GetTender(_ context.Context, id string) (*repository.RegistryTender, error) {
	for _, t := range f.tenders {
		if t.ID == id {
			return &t, nil
		}
	}
	return nil, repository.ErrTenderRegistryNotFound
}
func (f *fakeRegistryStore) ClaimReminders(_ context.Context, chatID int64, keys []repository.ReminderKey, pruneBefore string) ([]repository.ReminderKey, error) {
	f.pruneBefore = pruneBefore
	if f.claimed == nil {
		f.claimed = map[int64]map[repository.ReminderKey]bool{}
	}
	if f.claimed[chatID] == nil {
		f.claimed[chatID] = map[repository.ReminderKey]bool{}
	}
	var out []repository.ReminderKey
	for i := len(keys) - 1; i >= 0; i-- { // обратный порядок: сервис не должен полагаться на порядок БД
		if k := keys[i]; !f.claimed[chatID][k] {
			f.claimed[chatID][k] = true
			out = append(out, k)
		}
	}
	return out, nil
}
func (f *fakeRegistryStore) AppendChronology(_ context.Context, id string, e repository.ChronologyEntry) (string, bool, error) {
	t, err := f.GetTender(context.Background(), id)
	if err != nil {
		return "", false, err
	}
	for _, a := range f.appended {
		if a == e {
			return t.Title, false, nil
		}
	}
	f.appended = append(f.appended, e)
	return t.Title, true, nil
}

const (
	rbSentID = "0f8fad5b-d9cb-469f-a165-70867728950e"
	rbPDID   = "1f8fad5b-d9cb-469f-a165-70867728950e"
	rbArchID = "2f8fad5b-d9cb-469f-a165-70867728950e"
	rbChat   = int64(-100500)
)

// rbFixture — «Направлено» с подачей 22.09, «Ожидание ПД» с записью 30.08, архив.
func rbFixture() (*TenderRegistryBotService, *fakeRegistryStore, *time.Time) {
	store := &fakeRegistryStore{tenders: []repository.RegistryTender{
		{ID: rbSentID, Title: "ЖК Ода", ClientName: "ООО Ромашка", TenderNumber: tbPtr("330"), DashboardStatus: tbPtr("sent"),
			SubmissionDate: tbPtr(tbAt(2026, 9, 22, 12, 0)),
			Chronology:     []byte(`[{"date":"2026-09-20T09:00:00.000Z","text":"Отправили КП","type":"default"}]`)},
		{ID: rbPDID, Title: "Башня", ClientName: "АО Стройка", StatusName: tbPtr("Ожидаем тендерный пакет"),
			CreatedAt: tbPtr(tbAt(2026, 2, 11, 12, 0)), Chronology: []byte(`[{"date":"2026-08-30T09:00:00.000Z","text":"Ждём ПД"}]`)},
		{ID: rbArchID, Title: "Архивный", ClientName: "Кто-то", StatusName: tbPtr("Проиграли"),
			SubmissionDate: tbPtr(tbAt(2026, 1, 1, 12, 0))},
	}}
	svc := NewTenderRegistryBotService(store, "https://tender.example")
	svc.loc = tbMSK
	now := tbAt(2026, 9, 30, 10, 0)
	svc.now = func() time.Time { return now }
	return svc, store, &now
}

func TestRegistryBotClaimWindowAndDedup(t *testing.T) {
	svc, store, now := rbFixture()
	ctx := context.Background()

	*now = tbAt(2026, 9, 30, 8, 59)
	res, err := svc.ClaimReminders(ctx, rbChat)
	if err != nil || res.Window.Open || res.Items == nil || len(res.Items) != 0 || store.claimed != nil {
		t.Fatalf("до 9:00 ничего не выдаём: %+v %v", res, err)
	}

	*now = tbAt(2026, 9, 30, 9, 0)
	res, _ = svc.ClaimReminders(ctx, rbChat)
	if !res.Window.Open || res.Window.Timezone != "Europe/Moscow" || len(res.Items) != 2 {
		t.Fatalf("в 9:00 — звонок и ПД: %+v", res)
	}
	call, pd := res.Items[0], res.Items[1]
	if call.Kind != "call" || call.DueOn != "2026-09-30" || call.Tender.ID != rbSentID || call.Tender.DashboardStatus != "sent" ||
		!strings.Contains(call.Message.Text, "прошло более 7 дней: 8 дней с подачи КП (22.09.2026)") ||
		call.Message.ParseMode != "HTML" || call.Message.ReplyMarkup == nil ||
		call.Message.ReplyMarkup.InlineKeyboard[0][0].CallbackData != "tc:"+rbSentID {
		t.Fatalf("напоминание о звонке: %+v", call)
	}
	if pd.Kind != "waiting_pd" || pd.DueOn != "2026-09-30" ||
		!strings.Contains(pd.Message.Text, "прошёл 1 месяц с последней записи в хронологии (30.08.2026)") {
		t.Fatalf("напоминание ПД: %+v", pd)
	}
	if store.pruneBefore != "2026-07-02" {
		t.Fatalf("журнал чистится старше 90 дней: %s", store.pruneBefore)
	}

	*now = tbAt(2026, 9, 30, 15, 0)
	if res, _ = svc.ClaimReminders(ctx, rbChat); len(res.Items) != 0 {
		t.Fatalf("в тот же день повторно не выдаём: %+v", res.Items)
	}
	if res, _ = svc.ClaimReminders(ctx, rbChat-1); len(res.Items) != 2 {
		t.Fatalf("другой чат получает свои напоминания: %d", len(res.Items))
	}
	*now = tbAt(2026, 10, 1, 9, 10)
	if res, _ = svc.ClaimReminders(ctx, rbChat); len(res.Items) != 1 || res.Items[0].Kind != "call" {
		t.Fatalf("на следующий день — только «Направлено»: %+v", res.Items)
	}
	*now = tbAt(2026, 10, 2, 21, 0)
	if res, _ = svc.ClaimReminders(ctx, rbChat); res.Window.Open || len(res.Items) != 0 {
		t.Fatal("после 21:00 не выдаём")
	}
	var in *RegistryInputError
	if _, err := svc.ClaimReminders(ctx, 0); !errors.As(err, &in) {
		t.Fatalf("без chat_id — ошибка ввода: %v", err)
	}
}

func TestRegistryBotSearchAndCard(t *testing.T) {
	svc, _, _ := rbFixture()
	ctx := context.Background()

	res, err := svc.Search(ctx, " №330 ")
	if err != nil || res.Total != 1 || len(res.Items) != 1 || res.Query != "№330" ||
		!strings.Contains(res.Message.Text, "🏗 <b>ЖК Ода</b> · №330") ||
		!strings.Contains(res.Message.Text, "Контроль звонка: 8 дней с подачи КП (22.09.2026) — пора звонить") {
		t.Fatalf("одно совпадение — карточка: %+v %v", res, err)
	}
	res, _ = svc.Search(ctx, "о")
	if res.Total != 3 || len(res.Items) != 3 || !strings.Contains(res.Message.Text, "Найдено 3 тендера") ||
		res.Message.ReplyMarkup == nil || !strings.HasPrefix(res.Message.ReplyMarkup.InlineKeyboard[0][0].CallbackData, "ti:") {
		t.Fatalf("несколько — список кнопками: %+v", res)
	}
	res, _ = svc.Search(ctx, "нет такого")
	if res.Total != 0 || res.Items == nil || res.Message.ReplyMarkup != nil || !strings.Contains(res.Message.Text, "нет совпадений") {
		t.Fatalf("ничего не найдено: %+v", res)
	}
	var in *RegistryInputError
	for _, q := range []string{"  ", strings.Repeat("я", maxSearchQueryRunes+1)} {
		if _, err := svc.Search(ctx, q); !errors.As(err, &in) {
			t.Fatalf("запрос %q — ошибка ввода: %v", q, err)
		}
	}

	card, err := svc.Card(ctx, rbPDID)
	if err != nil || card.Tender.Status != "Ожидаем тендерный пакет" || card.Tender.DashboardStatus != "waiting_pd" ||
		!strings.Contains(card.Message.Text, "Башня") || card.Message.ReplyMarkup == nil {
		t.Fatalf("карточка: %+v %v", card, err)
	}
	if _, err := svc.Card(ctx, rbArchID[:35]+"f"); !errors.Is(err, repository.ErrTenderRegistryNotFound) {
		t.Fatalf("нет тендера: %v", err)
	}
}

func TestRegistryBotAppendChronology(t *testing.T) {
	svc, store, _ := rbFixture()
	ctx := context.Background()

	res, err := svc.AppendChronology(ctx, rbSentID, "  Позвонили, ждут решения ", "call_follow_up", "2026-09-30T09:59:05.987+03:00")
	if err != nil || !res.Created || res.Title != "ЖК Ода" || res.Entry.Date != "2026-09-30T06:59:05.987Z" ||
		res.Entry.Text != "Позвонили, ждут решения" || !strings.Contains(res.Message.Text, "📞 Звонок — Позвонили, ждут решения") {
		t.Fatalf("запись звонка: %+v %v", res, err)
	}
	res, _ = svc.AppendChronology(ctx, rbSentID, "Позвонили, ждут решения", "call_follow_up", "2026-09-30T06:59:05.987Z")
	if res.Created || len(store.appended) != 1 {
		t.Fatalf("повтор запроса второй записи не даёт: %+v", res)
	}
	res, _ = svc.AppendChronology(ctx, rbPDID, "Прислали ПД", "default", "2026-09-30T06:30:00Z")
	if !res.Created || res.Entry.Type != "default" || !strings.Contains(res.Message.Text, "📝 Событие — Прислали ПД") {
		t.Fatalf("запись события: %+v", res)
	}
	var in *RegistryInputError
	for name, c := range map[string][3]string{
		"пустой текст":     {"  ", "default", "2026-09-30T06:00:00Z"},
		"длинный текст":    {strings.Repeat("я", maxChronologyTextRunes+1), "default", "2026-09-30T06:00:00Z"},
		"неизвестный тип":  {"x", "call", "2026-09-30T06:00:00Z"},
		"дата не RFC3339":  {"x", "default", "30.09.2026"},
		"дата старше 30 д": {"x", "default", "2026-08-30T09:00:00Z"},
		"дата в будущем":   {"x", "default", "2026-09-30T07:11:00Z"},
	} {
		if _, err := svc.AppendChronology(ctx, rbSentID, c[0], c[1], c[2]); !errors.As(err, &in) {
			t.Fatalf("%s — ошибка ввода: %v", name, err)
		}
	}
	if _, err := svc.AppendChronology(ctx, rbArchID[:35]+"f", "x", "default", "2026-09-30T06:00:00Z"); !errors.Is(err, repository.ErrTenderRegistryNotFound) {
		t.Fatalf("нет тендера: %v", err)
	}
}
