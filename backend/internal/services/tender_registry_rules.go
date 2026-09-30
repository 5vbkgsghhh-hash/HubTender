package services

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/su10/hubtender/backend/internal/notify/telegram"
	"github.com/su10/hubtender/backend/internal/repository"
)

// Правила «Перечня тендеров» для API Telegram-бота — порт src/pages/Tenders/utils/tenderMonitor.ts.
// Держать в синхроне со страницей: статус (getDashboardStatus), контрольная дата
// звонка (getControlDate) и порог «Требуют звонка» (shouldShowCallAction).

// Статусы вкладок перечня (tender_registry.dashboard_status).
const (
	dashCalc      = "calc"
	dashSent      = "sent"
	dashWaitingPD = "waiting_pd"
	dashArchive   = "archive"
)

var dashLabels = map[string]string{
	dashCalc: "В расчете", dashSent: "Направлено", dashWaitingPD: "Ожидание ПД", dashArchive: "Архив",
}

// callThresholdDays — «Направлено» ждёт звонка, если с контрольной даты прошло больше 7 дней.
const callThresholdDays = 7

// pdCatchUpDays — сколько дней после «месячной годовщины» ещё догоняем пропущенное
// напоминание «Ожидание ПД» (сервер лежал); позже — ждём следующей годовщины.
const pdCatchUpDays = 7

// maxChronologyTextRunes — длина записи хронологии из бота.
const maxChronologyTextRunes = 2000

// standardPackageItems — STANDARD_PACKAGE_ITEMS страницы.
var standardPackageItems = []string{"ПД", "ВОР", "Договор", "ТЗ на СМР", "ТЗ на РД"}

func strOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ResolveDashboardStatus — getDashboardStatus: имя статуса важнее dashboard_status.
func ResolveDashboardStatus(statusName, dashboardStatus *string, isArchived bool) string {
	if s := dashboardStatusByName(strOrEmpty(statusName)); s != "" {
		return s
	}
	if d := strOrEmpty(dashboardStatus); d != "" {
		return d
	}
	if isArchived {
		return dashArchive
	}
	return dashCalc
}

func dashboardStatusByName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	switch {
	case n == "":
		return ""
	case strings.Contains(n, "выиграл"), strings.Contains(n, "проиграл"):
		return dashArchive
	case strings.Contains(n, "ожидаем тендерный пакет"):
		return dashWaitingPD
	case n == "в работе":
		return dashCalc
	case n == "направлено":
		return dashSent
	case strings.Contains(n, "ожида"):
		return dashWaitingPD
	}
	return ""
}

// statusLabel — подпись статуса как на странице (getTenderStatusDisplayLabel).
func statusLabel(statusName *string, status string) string {
	if s := strings.TrimSpace(strOrEmpty(statusName)); s != "" {
		return s
	}
	if l, ok := dashLabels[status]; ok {
		return l
	}
	return status
}

// chronoEntry — запись хронологии; At == nil — запись без даты (старый импорт).
type chronoEntry struct {
	At   *time.Time
	Text string
	Call bool
}

// parseChronology разбирает chronology_items. jsonb null, объект вместо массива
// и битые элементы пропускаются — у старых строк встречается всё это.
func parseChronology(raw []byte, loc *time.Location) []chronoEntry {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	out := make([]chronoEntry, 0, len(items))
	for _, it := range items {
		var v struct {
			Date *string `json:"date"`
			Text string  `json:"text"`
			Type *string `json:"type"`
		}
		if string(it) == "null" || json.Unmarshal(it, &v) != nil {
			continue
		}
		e := chronoEntry{Text: v.Text, Call: v.Type != nil && *v.Type == "call_follow_up"}
		if v.Date != nil {
			if at, ok := parseChronoDate(*v.Date, loc); ok {
				e.At = &at
			}
		}
		out = append(out, e)
	}
	return out
}

// parseChronoDate — ISO-строка страницы (dayjs().toISOString()); дата без зоны —
// местное время, как её прочитал бы dayjs.
func parseChronoDate(s string, loc *time.Location) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, true
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// latestEntries — n последних записей, новые первыми; записи без даты — после датированных.
func latestEntries(entries []chronoEntry, n int, loc *time.Location) []telegram.TenderEntry {
	idx := make([]int, len(entries))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ea, eb := entries[idx[a]], entries[idx[b]]
		switch {
		case ea.At == nil && eb.At == nil:
			return idx[a] > idx[b]
		case ea.At == nil:
			return false
		case eb.At == nil:
			return true
		case !ea.At.Equal(*eb.At):
			return ea.At.After(*eb.At)
		}
		return idx[a] > idx[b]
	})
	out := make([]telegram.TenderEntry, 0, min(n, len(idx)))
	for _, i := range idx[:min(n, len(idx))] {
		out = append(out, telegram.TenderEntry{Date: fmtDate(entries[i].At, loc), Call: entries[i].Call, Text: entries[i].Text})
	}
	return out
}

// civilDate — календарная дата t в loc. Полночь в UTC: разность дат не зависит
// от перехода на летнее время.
func civilDate(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, time.UTC)
}

// calendarDays — календарных дней от from до to в loc, как на странице:
// dayjs().startOf('day').diff(dayjs(from).startOf('day'), 'day').
func calendarDays(from, to time.Time, loc *time.Location) int {
	return int(civilDate(to, loc).Sub(civilDate(from, loc)) / (24 * time.Hour))
}

// localDate — календарная дата в loc как «YYYY-MM-DD».
func localDate(t time.Time, loc *time.Location) string { return t.In(loc).Format("2006-01-02") }

// callEval — звонок по тендеру «Направлено».
type callEval struct {
	Known    bool // есть контрольная дата
	Due      bool // пора звонить
	Days     int
	Since    time.Time
	FromCall bool // контрольная дата — последний звонок, иначе подача КП
}

// evalCall — getControlDate + shouldShowCallAction: контрольная дата — позднейшая
// из подачи КП и последнего звонка; звонить пора, если прошло больше
// callThresholdDays календарных дней. Обычное событие отсчёт не сбрасывает.
func evalCall(t repository.RegistryTender, entries []chronoEntry, now time.Time, loc *time.Location) callEval {
	var since *time.Time
	fromCall := false
	if t.SubmissionDate != nil {
		v := *t.SubmissionDate
		since = &v
	}
	for _, e := range entries {
		if e.Call && e.At != nil && (since == nil || e.At.After(*since)) {
			v := *e.At
			since, fromCall = &v, true
		}
	}
	if since == nil {
		return callEval{}
	}
	days := calendarDays(*since, now, loc)
	return callEval{Known: true, Due: days > callThresholdDays, Days: days, Since: *since, FromCall: fromCall}
}

// pdEval — напоминание «Ожидание ПД».
type pdEval struct {
	Due         bool
	DueOn       time.Time // «месячная годовщина», за которую напоминание
	Months      int
	Anchor      time.Time
	FromCreated bool // датированных записей нет — отсчёт от добавления в перечень
}

// evalPD — раз в месяц от дня последней датированной записи хронологии (нет
// таких — от добавления в перечень). Напоминание — в «месячную годовщину»
// (31-е → последний день короткого месяца); пропущенную догоняем не дольше
// pdCatchUpDays дней, чтобы первый запуск не завалил чат старыми тендерами.
func evalPD(t repository.RegistryTender, entries []chronoEntry, now time.Time, loc *time.Location) pdEval {
	var anchor *time.Time
	for _, e := range entries {
		if e.At != nil && (anchor == nil || e.At.After(*anchor)) {
			v := *e.At
			anchor = &v
		}
	}
	fromCreated := false
	if anchor == nil {
		if t.CreatedAt == nil {
			return pdEval{}
		}
		v := *t.CreatedAt
		anchor, fromCreated = &v, true
	}
	start, today := civilDate(*anchor, loc), civilDate(now, loc)
	months := (today.Year()-start.Year())*12 + int(today.Month()) - int(start.Month())
	if addMonthsClamped(start, months).After(today) {
		months--
	}
	res := pdEval{Months: months, Anchor: *anchor, FromCreated: fromCreated}
	if months < 1 {
		return res
	}
	res.DueOn = addMonthsClamped(start, months)
	res.Due = today.Sub(res.DueOn) <= pdCatchUpDays*24*time.Hour
	return res
}

// addMonthsClamped — та же дата через n месяцев; 31-е → последний день короткого месяца.
func addMonthsClamped(d time.Time, n int) time.Time {
	total := d.Year()*12 + int(d.Month()) - 1 + n
	y, m := total/12, time.Month(total%12+1)
	last := time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
	return time.Date(y, m, min(d.Day(), last), 0, 0, 0, 0, time.UTC)
}

// normalizeSearch — регистр, «ё» = «е», без кавычек и «№», пробелы схлопнуты.
func normalizeSearch(s string) string {
	s = strings.ReplaceAll(strings.ToLower(s), "ё", "е")
	s = strings.Map(func(r rune) rune {
		switch r {
		case '«', '»', '"', '\'', '“', '”', '„', '№', '#':
			return ' '
		}
		if unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// matchTenders — тендеры по запросу: сначала точный номер, затем начало номера
// или названия, затем вхождение в номер, название или заказчика.
func matchTenders(tenders []repository.RegistryTender, query string) []repository.RegistryTender {
	q := normalizeSearch(query)
	if q == "" {
		return nil
	}
	var exact, prefix, contains []repository.RegistryTender
	for _, t := range tenders {
		num, title, client := normalizeSearch(strOrEmpty(t.TenderNumber)), normalizeSearch(t.Title), normalizeSearch(t.ClientName)
		switch {
		case num != "" && num == q:
			exact = append(exact, t)
		case (num != "" && strings.HasPrefix(num, q)) || strings.HasPrefix(title, q):
			prefix = append(prefix, t)
		case strings.Contains(num, q) || strings.Contains(title, q) || strings.Contains(client, q):
			contains = append(contains, t)
		}
	}
	return append(append(exact, prefix...), contains...)
}

func fmtDate(t *time.Time, loc *time.Location) string {
	if t == nil {
		return ""
	}
	return t.In(loc).Format("02.01.2006")
}

// fmtDateTime — дата со временем; полночь — без времени (как formatTime на странице).
func fmtDateTime(t *time.Time, loc *time.Location) string {
	if t == nil {
		return ""
	}
	l := t.In(loc)
	if l.Hour() == 0 && l.Minute() == 0 {
		return l.Format("02.01.2006")
	}
	return l.Format("02.01.2006 15:04")
}

// fmtNumber — два знака после запятой, тысячи через пробел (ru-RU).
func fmtNumber(v float64) string {
	s := strconv.FormatFloat(math.Abs(v), 'f', 2, 64)
	intPart, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	if v < 0 {
		b.WriteByte('-')
	}
	for i, r := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	b.WriteString("," + frac)
	return b.String()
}

// fmtAmount — число с единицей; пусто и ноль не выводим.
func fmtAmount(v *float64, unit string) string {
	if v == nil || *v == 0 {
		return ""
	}
	return fmtNumber(*v) + " " + unit
}

// packageSummary — «3 из 5 (ПД, ВОР, Договор)», как getPackageSummary; пусто — пакета нет.
func packageSummary(raw []byte) string {
	var items []struct {
		Text *string `json:"text"`
	}
	if json.Unmarshal(raw, &items) != nil || len(items) == 0 {
		return ""
	}
	texts := make([]string, 0, len(items))
	for _, it := range items {
		if it.Text != nil {
			texts = append(texts, strings.ToLower(*it.Text))
		}
	}
	var have []string
	for _, s := range standardPackageItems {
		ls := strings.ToLower(s)
		for _, t := range texts {
			if strings.Contains(t, ls) {
				have = append(have, s)
				break
			}
		}
	}
	res := fmt.Sprintf("%d из %d", len(have), len(standardPackageItems))
	if len(have) > 0 {
		res += " (" + strings.Join(have, ", ") + ")"
	}
	if extra := len(items) - len(have); extra > 0 {
		res += fmt.Sprintf(", ещё %d", extra)
	}
	return res
}

// chronologyDate — дата новой записи в формате страницы (dayjs().toISOString()).
func chronologyDate(now time.Time) string { return now.UTC().Format("2006-01-02T15:04:05.000Z") }
