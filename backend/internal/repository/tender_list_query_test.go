package repository

import (
	"strings"
	"testing"
)

// 0-F2 regression guard (август 2026). У ListTenders был собственный список
// колонок без шести financial_*: GET /api/v1/tenders отдавал
// "financial_calculation_status": "" и нулевые ревизии, fail-closed политика
// фронта (src/lib/financial/calculationState.ts) читала это как 'stale', и
// «Форма КП» блокировала финальный экспорт для КАЖДОГО тендера, хотя в БД все
// были 'calculated'. Тест не требует тестовой БД — идёт в обычном go test ./...
var tenderFinancialProjectionCols = []string{
	"financial_input_revision",
	"financial_calculation_revision",
	"financial_calculation_status",
	"financial_calculated_at::text", // FinancialCalculatedAt — *string, каст обязателен
	"financial_calculation_error_code",
	"financial_calculation_error_message",
}

func TestTenderFinancialCols_SingleSourceOfTruth(t *testing.T) {
	for _, col := range tenderFinancialProjectionCols {
		if !strings.Contains(tenderFinancialCols, col) {
			t.Fatalf("tenderFinancialCols потерял %q", col)
		}
	}
	if !strings.Contains(tenderScanCols, tenderFinancialCols) {
		t.Fatal("tenderScanCols обязан строиться из tenderFinancialCols — иначе проекция снова раздвоится")
	}
}

func TestBuildTenderListQuery_ProjectsFinancialColumns(t *testing.T) {
	q, args := buildTenderListQuery(TenderListParams{})

	for _, col := range tenderFinancialProjectionCols {
		if !strings.Contains(q, col) {
			t.Fatalf("проекция ListTenders потеряла %q — список отдал бы пустой статус расчёта и заблокировал экспорт «Формы КП»", col)
		}
	}
	if len(args) != 1 {
		t.Fatalf("ожидался только аргумент LIMIT, получено %d", len(args))
	}
	if !strings.Contains(q, "ORDER BY updated_at DESC, id DESC") {
		t.Fatal("потеряна keyset-сортировка (updated_at DESC, id DESC)")
	}
}

// Курсор и фильтры не должны съезжать по номерам плейсхолдеров при добавлении
// колонок в проекцию: LIMIT всегда последний аргумент.
func TestBuildTenderListQuery_ArgOrder(t *testing.T) {
	archived := false
	q, args := buildTenderListQuery(TenderListParams{
		IsArchived:   &archived,
		HousingClass: "комфорт",
		Search:       "офис",
		Limit:        25,
	})

	if len(args) != 4 {
		t.Fatalf("ожидалось 3 фильтра + LIMIT, получено %d", len(args))
	}
	if args[3] != 25 {
		t.Fatalf("LIMIT обязан быть последним аргументом, получено %v", args[3])
	}
	if !strings.Contains(q, "LIMIT $4") {
		t.Fatalf("LIMIT должен ссылаться на $4, запрос:\n%s", q)
	}
}

// Регрессия 13.10.2026: base_total считался в списке коррелированным
// подзапросом SUM(boq_items.total_amount) на каждый тендер — список зовут все
// страницы и на каждое realtime-событие `tenders`, и серия записей в ВОР
// забивала CPU базы копиями этого запроса (API отдавал 504 четырнадцать минут).
// Сумма хранится в tenders.cached_base_total (пишет RecalculateTenderGrandTotalTx);
// список обязан читать её и не трогать boq_items.
func TestBuildTenderListQuery_BaseTotalIsStoredColumn(t *testing.T) {
	q, _ := buildTenderListQuery(TenderListParams{})

	if !strings.Contains(q, "cached_base_total AS base_total") {
		t.Fatalf("base_total обязан читаться из tenders.cached_base_total, запрос:\n%s", q)
	}
	if strings.Contains(q, "boq_items") {
		t.Fatalf("список тендеров не должен обращаться к boq_items, запрос:\n%s", q)
	}
}
