package services

import (
	"strings"
	"testing"
	"time"

	"github.com/su10/hubtender/backend/internal/repository"
)

func tbPtr[T any](v T) *T { return &v }

var tbMSK = moscowLocation()

// tbAt — момент по Москве.
func tbAt(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, tbMSK)
}

func TestResolveDashboardStatusMatchesPage(t *testing.T) {
	cases := []struct {
		name, dash string
		archived   bool
		want       string
	}{
		{"Направлено", "", false, dashSent},
		{"  направлено ", "", false, dashSent},
		{"Ожидаем тендерный пакет", "", false, dashWaitingPD},
		{"Ожидание ответа", "", false, dashWaitingPD},
		{"В работе", "sent", false, dashCalc},
		{"Выиграли", "", false, dashArchive},
		{"Проиграли", "sent", false, dashArchive},
		{"", "sent", true, dashSent},
		{"Неизвестный", "waiting_pd", false, dashWaitingPD},
		{"", "", true, dashArchive},
		{"", "", false, dashCalc},
	}
	for _, c := range cases {
		var name, dash *string
		if c.name != "" {
			name = tbPtr(c.name)
		}
		if c.dash != "" {
			dash = tbPtr(c.dash)
		}
		if got := ResolveDashboardStatus(name, dash, c.archived); got != c.want {
			t.Fatalf("%q/%q/%v → %q, ожидался %q", c.name, c.dash, c.archived, got, c.want)
		}
	}
	if statusLabel(nil, dashWaitingPD) != "Ожидание ПД" || statusLabel(tbPtr(" Ожидаем тендерный пакет "), dashWaitingPD) != "Ожидаем тендерный пакет" {
		t.Fatal("подпись статуса")
	}
}

func TestParseChronologyTolerant(t *testing.T) {
	for _, raw := range []string{"", "null", "{}", `"x"`, "[]"} {
		if got := parseChronology([]byte(raw), tbMSK); len(got) != 0 {
			t.Fatalf("%q → %+v", raw, got)
		}
	}
	got := parseChronology([]byte(`[null, {"date":null,"text":"старое"}, {"date":"bad","text":"x","type":"default"},
		{"date":"2026-09-22T08:41:17.512Z","text":"звонок","type":"call_follow_up"}, {"text":5},
		{"date":"2026-09-23","text":"без времени"}]`), tbMSK)
	if len(got) != 4 {
		t.Fatalf("записей %d: %+v", len(got), got)
	}
	if got[0].At != nil || got[1].At != nil || got[1].Call {
		t.Fatalf("записи без даты / с битой датой: %+v", got[:2])
	}
	if got[2].At == nil || !got[2].Call || !got[2].At.Equal(time.Date(2026, 9, 22, 8, 41, 17, 512e6, time.UTC)) {
		t.Fatalf("звонок: %+v", got[2])
	}
	if got[3].At == nil || !got[3].At.Equal(tbAt(2026, 9, 23, 0, 0)) {
		t.Fatalf("дата без времени — полночь по Москве: %+v", got[3])
	}
}

func TestEvalCall(t *testing.T) {
	now := tbAt(2026, 9, 30, 10, 0)
	chrono := func(raw string) []chronoEntry { return parseChronology([]byte(raw), tbMSK) }
	cases := []struct {
		name       string
		submission *time.Time
		chrono     string
		due        bool
		days       int
		fromCall   bool
	}{
		{"8 дней с подачи КП", tbPtr(tbAt(2026, 9, 22, 12, 0)), `[]`, true, 8, false},
		{"7 дней — ещё рано", tbPtr(tbAt(2026, 9, 23, 18, 0)), `[]`, false, 7, false},
		{"звонок сбрасывает отсчёт", tbPtr(tbAt(2026, 9, 1, 12, 0)),
			`[{"date":"2026-09-27T09:00:00.000Z","text":"позвонили","type":"call_follow_up"}]`, false, 3, true},
		{"событие отсчёт не сбрасывает", tbPtr(tbAt(2026, 9, 22, 12, 0)),
			`[{"date":"2026-09-29T09:00:00.000Z","text":"письмо","type":"default"}]`, true, 8, false},
		{"звонок раньше подачи не считается", tbPtr(tbAt(2026, 9, 22, 12, 0)),
			`[{"date":"2026-09-01T09:00:00.000Z","text":"звонок","type":"call_follow_up"}]`, true, 8, false},
		{"граница суток по Москве", tbPtr(time.Date(2026, 9, 22, 21, 30, 0, 0, time.UTC)), `[]`, false, 7, false},
		{"только звонок без подачи", nil,
			`[{"date":"2026-09-10T09:00:00.000Z","text":"звонок","type":"call_follow_up"}]`, true, 20, true},
	}
	for _, c := range cases {
		ev := evalCall(repository.RegistryTender{SubmissionDate: c.submission}, chrono(c.chrono), now, tbMSK)
		if !ev.Known || ev.Due != c.due || ev.Days != c.days || ev.FromCall != c.fromCall {
			t.Fatalf("%s: %+v", c.name, ev)
		}
	}
	if ev := evalCall(repository.RegistryTender{}, nil, now, tbMSK); ev.Known || ev.Due {
		t.Fatalf("без дат напоминать нечего: %+v", ev)
	}
}

func TestEvalPD(t *testing.T) {
	entries := parseChronology([]byte(`[{"date":"2026-08-01T09:00:00.000Z","text":"раньше"},
		{"date":"2026-08-15T09:00:00.000Z","text":"ждём ПД"}, {"date":null,"text":"старое"}]`), tbMSK)
	tender := repository.RegistryTender{CreatedAt: tbPtr(tbAt(2026, 2, 11, 12, 0))}
	cases := []struct {
		now    time.Time
		due    bool
		months int
		dueOn  string
	}{
		{tbAt(2026, 9, 14, 12, 0), false, 0, ""},
		{tbAt(2026, 9, 15, 9, 0), true, 1, "2026-09-15"},
		{tbAt(2026, 9, 22, 9, 0), true, 1, "2026-09-15"},
		{tbAt(2026, 9, 23, 9, 0), false, 1, "2026-09-15"},
		{tbAt(2026, 10, 15, 9, 0), true, 2, "2026-10-15"},
	}
	for _, c := range cases {
		ev := evalPD(tender, entries, c.now, tbMSK)
		dueOn := ""
		if !ev.DueOn.IsZero() {
			dueOn = ev.DueOn.Format("2006-01-02")
		}
		if ev.Due != c.due || ev.Months != c.months || dueOn != c.dueOn || ev.FromCreated {
			t.Fatalf("%s: %+v", c.now, ev)
		}
	}

	endOfMonth := parseChronology([]byte(`[{"date":"2026-01-31T09:00:00.000Z","text":"x"}]`), tbMSK)
	if ev := evalPD(tender, endOfMonth, tbAt(2026, 2, 28, 9, 0), tbMSK); !ev.Due || ev.DueOn.Format("2006-01-02") != "2026-02-28" {
		t.Fatalf("31 января → 28 февраля: %+v", ev)
	}
	if ev := evalPD(tender, endOfMonth, tbAt(2026, 3, 31, 9, 0), tbMSK); !ev.Due || ev.Months != 2 || ev.DueOn.Format("2006-01-02") != "2026-03-31" {
		t.Fatalf("31 января → 31 марта: %+v", ev)
	}

	noDates := parseChronology([]byte(`[{"date":null,"text":"старое"}]`), tbMSK)
	if ev := evalPD(tender, noDates, tbAt(2026, 9, 11, 9, 0), tbMSK); !ev.Due || !ev.FromCreated || ev.Months != 7 {
		t.Fatalf("без датированных записей — от добавления в перечень: %+v", ev)
	}
	if ev := evalPD(repository.RegistryTender{}, noDates, tbAt(2026, 9, 11, 9, 0), tbMSK); ev.Due {
		t.Fatalf("без дат вообще напоминать не от чего: %+v", ev)
	}
}

func TestAddMonthsClamped(t *testing.T) {
	d := func(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }
	cases := []struct {
		from time.Time
		n    int
		want time.Time
	}{
		{d(2024, 1, 31), 1, d(2024, 2, 29)},
		{d(2026, 1, 31), 1, d(2026, 2, 28)},
		{d(2026, 12, 15), 1, d(2027, 1, 15)},
		{d(2026, 8, 31), 13, d(2027, 9, 30)},
	}
	for _, c := range cases {
		if got := addMonthsClamped(c.from, c.n); !got.Equal(c.want) {
			t.Fatalf("%s + %d мес. = %s, ожидалось %s", c.from.Format("2006-01-02"), c.n, got.Format("2006-01-02"), c.want.Format("2006-01-02"))
		}
	}
}

func TestMatchTenders(t *testing.T) {
	tenders := []repository.RegistryTender{
		{ID: "a", TenderNumber: tbPtr("3301"), Title: "Дом", ClientName: "Ёлка"},
		{ID: "b", TenderNumber: tbPtr("330"), Title: "ЖК «Ода»", ClientName: "ООО Ромашка"},
		{ID: "c", Title: "Ода-2 корпус", ClientName: "ИП"},
	}
	ids := func(q string) string {
		var out []string
		for _, t := range matchTenders(tenders, q) {
			out = append(out, t.ID)
		}
		return strings.Join(out, ",")
	}
	for q, want := range map[string]string{
		"330": "b,a", "№330": "b,a", "ода": "c,b", "ЖК ОДА": "b", "елка": "a", "ромашка": "b", "нет такого": "", "  ": "",
	} {
		if got := ids(q); got != want {
			t.Fatalf("%q → %q, ожидалось %q", q, got, want)
		}
	}
}

func TestLatestEntries(t *testing.T) {
	entries := parseChronology([]byte(`[{"date":null,"text":"старое"}, {"date":"2026-09-01T09:00:00.000Z","text":"первое"},
		{"date":"2026-09-20T09:00:00.000Z","text":"последнее","type":"call_follow_up"}, {"date":"2026-09-10T09:00:00.000Z","text":"среднее"}]`), tbMSK)
	got := latestEntries(entries, 3, tbMSK)
	if len(got) != 3 || got[0].Text != "последнее" || !got[0].Call || got[0].Date != "20.09.2026" ||
		got[1].Text != "среднее" || got[2].Text != "первое" {
		t.Fatalf("последние записи: %+v", got)
	}
	if all := latestEntries(entries, 10, tbMSK); len(all) != 4 || all[3].Text != "старое" || all[3].Date != "" {
		t.Fatalf("запись без даты — в конце: %+v", all)
	}
}

func TestFormatting(t *testing.T) {
	if got := fmtNumber(1234567.891); got != "1 234 567,89" {
		t.Fatal(got)
	}
	if got := fmtNumber(-1000); got != "-1 000,00" {
		t.Fatal(got)
	}
	if fmtAmount(nil, "₽") != "" || fmtAmount(tbPtr(0.0), "₽") != "" || fmtAmount(tbPtr(12.5), "м²") != "12,50 м²" {
		t.Fatal("fmtAmount")
	}
	if got := fmtDateTime(tbPtr(time.Date(2026, 9, 17, 21, 0, 0, 0, time.UTC)), tbMSK); got != "18.09.2026" {
		t.Fatalf("полночь по Москве — без времени: %q", got)
	}
	if got := fmtDateTime(tbPtr(tbAt(2026, 9, 18, 14, 5)), tbMSK); got != "18.09.2026 14:05" {
		t.Fatal(got)
	}
	if got := packageSummary([]byte(`[{"text":"ПД стадия П"},{"text":"ВОР"},{"text":"Прочее"}]`)); got != "2 из 5 (ПД, ВОР), ещё 1" {
		t.Fatal(got)
	}
	if packageSummary([]byte(`null`)) != "" || packageSummary(nil) != "" {
		t.Fatal("пустой пакет")
	}
	if got := chronologyDate(time.Date(2026, 9, 30, 7, 8, 9, 123456789, time.UTC)); got != "2026-09-30T07:08:09.123Z" {
		t.Fatal(got)
	}
}
