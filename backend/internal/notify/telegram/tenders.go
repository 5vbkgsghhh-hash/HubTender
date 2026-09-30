package telegram

import (
	"fmt"
	"html"
	"strings"
)

// Кнопки «Перечня тендеров»: ti — карточка, tc — записать звонок, te — записать событие.
const (
	ActionTenderInfo  = "ti"
	ActionTenderCall  = "tc"
	ActionTenderEvent = "te"
)

// InlineKeyboard — reply_markup сообщения Telegram.
type InlineKeyboard struct {
	InlineKeyboard [][]InlineButton `json:"inline_keyboard"`
}

// MessagePayload — готовое сообщение для sendMessage: бот подставляет chat_id
// и отправляет как есть.
type MessagePayload struct {
	Text        string          `json:"text"`
	ParseMode   string          `json:"parse_mode"`
	ReplyMarkup *InlineKeyboard `json:"reply_markup,omitempty"`
}

// Payload — HTML-сообщение; без кнопок reply_markup не выводится.
func Payload(text string, keyboard [][]InlineButton) MessagePayload {
	m := MessagePayload{Text: text, ParseMode: "HTML"}
	if len(keyboard) > 0 {
		m.ReplyMarkup = &InlineKeyboard{InlineKeyboard: keyboard}
	}
	return m
}

// MaxSearchResults — сколько найденных тендеров показывать кнопками.
const MaxSearchResults = 10

// Лимиты свободного текста: сообщение Telegram — до 4096 символов.
const (
	maxTitleRunes  = 200
	maxFieldRunes  = 200
	maxEntryRunes  = 300
	maxButtonRunes = 60
)

// TenderCallbackData — «tc:<uuid строки перечня>», 39 байт при лимите 64.
// Кнопки обрабатывает внешний бот (api/REGISTRY.md).
func TenderCallbackData(action, registryID string) string { return action + ":" + registryID }

// TenderEntry — запись хронологии для вывода.
type TenderEntry struct {
	Date string // «02.01.2006»; пусто — запись без даты
	Call bool   // звонок (call_follow_up), иначе событие
	Text string
}

// TenderView — тендер из перечня; значения уже отформатированы, пустые не выводятся.
type TenderView struct {
	ID         string
	Number     string
	Title      string
	Client     string
	Status     string
	CallStatus string // для «Направлено»: сколько дней без звонка
	Scope      string
	Area       string
	Cost       string
	Submission string
	Invitation string
	SiteStart  string
	Commission string
	Address    string
	Package    string
	Entries    []TenderEntry // новые первыми
	Link       string        // ссылка на перечень; кнопка — только для https
}

// CallReminder — «По тендеру надо позвонить».
type CallReminder struct {
	Tender    TenderView
	Days      int
	Threshold int
	Since     string // контрольная дата
	FromCall  bool   // контрольная дата — последний звонок, иначе подача КП
}

// PDReminder — «Ожидание ПД»: прошёл очередной месяц.
type PDReminder struct {
	Tender      TenderView
	Months      int
	Since       string
	FromCreated bool // в хронологии нет записей с датой — отсчёт от добавления в перечень
}

// esc — обрезка и экранирование: сначала обрезка, чтобы не разрезать «&amp;».
func esc(s string, n int) string { return html.EscapeString(truncateRunes(strings.TrimSpace(s), n)) }

// PluralRu — число со словом в нужной форме: PluralRu(2, "день", "дня", "дней") = «2 дня».
func PluralRu(n int, one, few, many string) string {
	m := n % 100
	if m < 0 {
		m = -m
	}
	word := many
	switch {
	case m >= 11 && m <= 14:
	case m%10 == 1:
		word = one
	case m%10 >= 2 && m%10 <= 4:
		word = few
	}
	return fmt.Sprintf("%d %s", n, word)
}

// tenderRef — «ЖК Ода» (№330, ООО «Ромашка»).
func tenderRef(v TenderView) string {
	ref := "«<b>" + esc(v.Title, maxTitleRunes) + "</b>»"
	var extra []string
	if v.Number != "" {
		extra = append(extra, "№"+esc(v.Number, 40))
	}
	if v.Client != "" {
		extra = append(extra, esc(v.Client, maxTitleRunes))
	}
	if len(extra) > 0 {
		ref += " (" + strings.Join(extra, ", ") + ")"
	}
	return ref
}

func writeEntries(b *strings.Builder, title string, entries []TenderEntry) {
	if len(entries) == 0 {
		b.WriteString("\n\n<i>В хронологии пока нет записей.</i>")
		return
	}
	fmt.Fprintf(b, "\n\n<b>%s</b>", title)
	for _, e := range entries {
		icon, kind := "📝", "Событие"
		if e.Call {
			icon, kind = "📞", "Звонок"
		}
		date := e.Date
		if date == "" {
			date = "без даты"
		}
		fmt.Fprintf(b, "\n%s %s · %s — %s", icon, date, kind, esc(e.Text, maxEntryRunes))
	}
}

// TenderKeyboard — кнопки под карточкой и напоминаниями.
func TenderKeyboard(v TenderView) [][]InlineButton {
	kb := [][]InlineButton{{
		{Text: "📞 Звонок", CallbackData: TenderCallbackData(ActionTenderCall, v.ID)},
		{Text: "📝 Событие", CallbackData: TenderCallbackData(ActionTenderEvent, v.ID)},
	}}
	if strings.HasPrefix(v.Link, "https://") {
		kb = append(kb, []InlineButton{{Text: "Открыть перечень", URL: v.Link}})
	}
	return kb
}

// RenderTenderCard — карточка тендера, как вкладка «Информация» на странице перечня.
func RenderTenderCard(v TenderView) (string, [][]InlineButton) {
	var b strings.Builder
	b.WriteString("🏗 <b>" + esc(v.Title, maxTitleRunes) + "</b>")
	if v.Number != "" {
		b.WriteString(" · №" + esc(v.Number, 40))
	}
	field := func(label, value string) {
		if value != "" {
			fmt.Fprintf(&b, "\n%s: %s", label, esc(value, maxFieldRunes))
		}
	}
	field("Заказчик", v.Client)
	field("Статус", v.Status)
	field("Контроль звонка", v.CallStatus)
	field("Объём строительства", v.Scope)
	field("Площадь по СП", v.Area)
	field("Стоимость КП", v.Cost)
	field("Подача КП", v.Submission)
	field("Приглашение", v.Invitation)
	field("Выход на площадку", v.SiteStart)
	field("Ввод в эксплуатацию", v.Commission)
	field("Адрес", v.Address)
	field("Тендерный пакет", v.Package)
	writeEntries(&b, "Хронология, последние записи", v.Entries)
	return b.String(), TenderKeyboard(v)
}

// RenderCallReminder — напоминание о звонке по тендеру «Направлено».
func RenderCallReminder(r CallReminder) (string, [][]InlineButton) {
	from := "подачи КП"
	if r.FromCall {
		from = "последнего звонка"
	}
	var b strings.Builder
	b.WriteString("📞 <b>Надо позвонить</b>\n")
	fmt.Fprintf(&b, "По тендеру %s надо позвонить, так как прошло более %s: %s с %s (%s).",
		tenderRef(r.Tender), PluralRu(r.Threshold, "дня", "дней", "дней"),
		PluralRu(r.Days, "день", "дня", "дней"), from, esc(r.Since, 20))
	writeEntries(&b, "Последняя запись в хронологии", r.Tender.Entries)
	return b.String(), TenderKeyboard(r.Tender)
}

// RenderPDReminder — ежемесячное напоминание по тендеру «Ожидание ПД».
func RenderPDReminder(r PDReminder) (string, [][]InlineButton) {
	verb := "прошло"
	if r.Months%10 == 1 && r.Months%100 != 11 {
		verb = "прошёл"
	}
	var b strings.Builder
	b.WriteString("📄 <b>Ожидание ПД</b>\n")
	if r.FromCreated {
		fmt.Fprintf(&b, "По тендеру %s ждём ПД: %s %s с добавления в перечень (%s) — записей с датой в хронологии нет.",
			tenderRef(r.Tender), verb, PluralRu(r.Months, "месяц", "месяца", "месяцев"), esc(r.Since, 20))
	} else {
		fmt.Fprintf(&b, "По тендеру %s ждём ПД: %s %s с последней записи в хронологии (%s).",
			tenderRef(r.Tender), verb, PluralRu(r.Months, "месяц", "месяца", "месяцев"), esc(r.Since, 20))
	}
	b.WriteString(" Уточните у заказчика сроки.")
	writeEntries(&b, "Последняя запись в хронологии", r.Tender.Entries)
	return b.String(), TenderKeyboard(r.Tender)
}

// RenderSearchResults — найденные тендеры кнопками; total — сколько найдено всего.
func RenderSearchResults(query string, found []TenderView, total int) (string, [][]InlineButton) {
	text := fmt.Sprintf("Найдено %s по запросу «%s» — выберите:",
		PluralRu(total, "тендер", "тендера", "тендеров"), esc(query, 100))
	if total > len(found) {
		text += fmt.Sprintf("\n<i>Показаны первые %d — уточните запрос.</i>", len(found))
	}
	kb := make([][]InlineButton, 0, len(found))
	for _, v := range found {
		label := strings.TrimSpace(v.Title)
		if v.Number != "" {
			label = "№" + strings.TrimSpace(v.Number) + " · " + label
		}
		if v.Client != "" {
			label += " · " + strings.TrimSpace(v.Client)
		}
		kb = append(kb, []InlineButton{{
			Text:         truncateRunes(label, maxButtonRunes),
			CallbackData: TenderCallbackData(ActionTenderInfo, v.ID),
		}})
	}
	return text, kb
}

// RenderNotFound — по запросу ничего не нашлось.
func RenderNotFound(query string) string {
	return fmt.Sprintf("В перечне тендеров нет совпадений с «%s». "+
		"Попробуйте номер тендера или часть названия либо заказчика.", esc(query, 100))
}

// RenderEntrySaved — подтверждение записи в хронологию.
func RenderEntrySaved(title string, call bool, text string) string {
	kind := "📝 Событие"
	if call {
		kind = "📞 Звонок"
	}
	return fmt.Sprintf("✅ Записал в хронологию «%s»: %s — %s",
		esc(title, maxTitleRunes), kind, esc(text, maxEntryRunes))
}
