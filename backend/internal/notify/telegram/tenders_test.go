package telegram

import (
	"encoding/json"
	"strings"
	"testing"
)

const registryID = "0f8fad5b-d9cb-469f-a165-70867728950e"

func TestTenderCallbackData(t *testing.T) {
	for _, action := range []string{ActionTenderInfo, ActionTenderCall, ActionTenderEvent} {
		if data := TenderCallbackData(action, registryID); len(data) > 64 || data != action+":"+registryID {
			t.Fatalf("callback_data: %q", data)
		}
	}
}

func TestPayloadJSON(t *testing.T) {
	raw, err := json.Marshal(Payload("x", nil))
	if err != nil || string(raw) != `{"text":"x","parse_mode":"HTML"}` {
		t.Fatalf("сообщение без кнопок: %s %v", raw, err)
	}
	raw, _ = json.Marshal(Payload("x", TenderKeyboard(TenderView{ID: registryID, Link: "https://tender.example/tenders"})))
	want := `{"text":"x","parse_mode":"HTML","reply_markup":{"inline_keyboard":[[` +
		`{"text":"📞 Звонок","callback_data":"tc:` + registryID + `"},{"text":"📝 Событие","callback_data":"te:` + registryID + `"}],` +
		`[{"text":"Открыть перечень","url":"https://tender.example/tenders"}]]}}`
	if string(raw) != want {
		t.Fatalf("сообщение с кнопками:\n%s\n%s", raw, want)
	}
}

func TestPluralRu(t *testing.T) {
	cases := map[int]string{0: "0 дней", 1: "1 день", 2: "2 дня", 4: "4 дня", 5: "5 дней", 11: "11 дней",
		14: "14 дней", 21: "21 день", 22: "22 дня", 111: "111 дней", 101: "101 день"}
	for n, want := range cases {
		if got := PluralRu(n, "день", "дня", "дней"); got != want {
			t.Fatalf("PluralRu(%d) = %q, ожидалось %q", n, got, want)
		}
	}
}

func fullView() TenderView {
	long := strings.Repeat("я", 1000)
	return TenderView{
		ID: registryID, Number: "330", Title: "ЖК <Ода> & " + long, Client: "ООО «Ромашка» " + long,
		Status: "Направлено", CallStatus: "12 дней с подачи КП (18.09.2026)", Scope: long, Area: "45 000,00 м²",
		Cost: "1 234 567,89 ₽", Submission: "18.09.2026 14:00", Invitation: "01.09.2026", SiteStart: "01.12.2026",
		Commission: "01.12.2028", Address: long, Package: long,
		Entries: []TenderEntry{
			{Date: "18.09.2026", Call: true, Text: "<script>" + long},
			{Date: "", Text: long},
			{Date: "01.09.2026", Text: long},
		},
		Link: "https://tender.example/tenders",
	}
}

func TestRenderTenderCardEscapesAndFits(t *testing.T) {
	text, kb := RenderTenderCard(fullView())
	if strings.Contains(text, "<Ода>") || strings.Contains(text, "<script>") || !strings.Contains(text, "&lt;Ода&gt; &amp;") {
		t.Fatalf("текст не экранирован: %.300s", text)
	}
	if n := len([]rune(text)); n > 4096 {
		t.Fatalf("карточка длиннее лимита Telegram: %d", n)
	}
	for _, want := range []string{"Заказчик:", "Статус: Направлено", "Контроль звонка:", "Стоимость КП: 1 234 567,89 ₽",
		"📞 18.09.2026 · Звонок", "без даты · Событие"} {
		if !strings.Contains(text, want) {
			t.Fatalf("в карточке нет %q", want)
		}
	}
	if len(kb) != 2 || kb[0][0].CallbackData != "tc:"+registryID || kb[0][1].CallbackData != "te:"+registryID ||
		kb[1][0].URL != "https://tender.example/tenders" {
		t.Fatalf("клавиатура: %+v", kb)
	}

	empty := TenderView{ID: registryID, Title: "ЖК", Link: "javascript:alert(1)"}
	text, kb = RenderTenderCard(empty)
	if strings.Contains(text, "Заказчик:") || !strings.Contains(text, "В хронологии пока нет записей") {
		t.Fatalf("пустые поля выведены: %s", text)
	}
	if len(kb) != 1 {
		t.Fatal("небезопасная ссылка попала в кнопку")
	}
}

func TestRenderCallReminder(t *testing.T) {
	v := TenderView{ID: registryID, Number: "330", Title: "ЖК Ода", Client: "ООО «Ромашка»",
		Entries: []TenderEntry{{Date: "18.09.2026", Call: true, Text: "Ждут решения"}}}
	text, kb := RenderCallReminder(CallReminder{Tender: v, Days: 12, Threshold: 7, Since: "18.09.2026", FromCall: true})
	for _, want := range []string{"«<b>ЖК Ода</b>» (№330, ООО «Ромашка»)", "надо позвонить, так как прошло более 7 дней",
		"12 дней с последнего звонка (18.09.2026)", "Последняя запись в хронологии", "Ждут решения"} {
		if !strings.Contains(text, want) {
			t.Fatalf("в напоминании нет %q:\n%s", want, text)
		}
	}
	if len(kb) != 1 || len(kb[0]) != 2 {
		t.Fatalf("клавиатура: %+v", kb)
	}
	text, _ = RenderCallReminder(CallReminder{Tender: TenderView{ID: registryID, Title: "ЖК"}, Days: 8, Threshold: 7, Since: "01.09.2026"})
	if !strings.Contains(text, "8 дней с подачи КП") || !strings.Contains(text, "В хронологии пока нет записей") {
		t.Fatalf("напоминание без хронологии: %s", text)
	}
}

func TestRenderPDReminder(t *testing.T) {
	v := TenderView{ID: registryID, Title: "ЖК Ода"}
	text, _ := RenderPDReminder(PDReminder{Tender: v, Months: 1, Since: "15.08.2026"})
	if !strings.Contains(text, "прошёл 1 месяц с последней записи в хронологии (15.08.2026)") {
		t.Fatalf("напоминание ПД: %s", text)
	}
	text, _ = RenderPDReminder(PDReminder{Tender: v, Months: 7, Since: "11.02.2026", FromCreated: true})
	if !strings.Contains(text, "прошло 7 месяцев с добавления в перечень (11.02.2026)") {
		t.Fatalf("напоминание ПД без записей: %s", text)
	}
}

func TestRenderSearchResults(t *testing.T) {
	found := []TenderView{
		{ID: registryID, Number: "330", Title: "ЖК Ода", Client: "ООО Ромашка"},
		{ID: "1f8fad5b-d9cb-469f-a165-70867728950e", Title: strings.Repeat("Длинное название ", 10)},
	}
	text, kb := RenderSearchResults("<ода>", found, 12)
	if strings.Contains(text, "<ода>") || !strings.Contains(text, "Найдено 12 тендеров") || !strings.Contains(text, "первые 2") {
		t.Fatalf("текст поиска: %s", text)
	}
	if len(kb) != 2 || kb[0][0].Text != "№330 · ЖК Ода · ООО Ромашка" || kb[0][0].CallbackData != "ti:"+registryID {
		t.Fatalf("кнопки поиска: %+v", kb)
	}
	if n := len([]rune(kb[1][0].Text)); n > maxButtonRunes+1 {
		t.Fatalf("надпись кнопки не обрезана: %d", n)
	}
}

func TestRenderEntrySaved(t *testing.T) {
	if s := RenderEntrySaved("ЖК", true, "a & b"); !strings.Contains(s, "📞 Звонок — a &amp; b") {
		t.Fatalf("подтверждение звонка: %s", s)
	}
	if s := RenderEntrySaved("ЖК <1>", false, "x"); !strings.Contains(s, "«ЖК &lt;1&gt;»: 📝 Событие — x") {
		t.Fatalf("подтверждение события: %s", s)
	}
}
