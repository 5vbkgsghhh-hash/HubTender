package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Row types
// ---------------------------------------------------------------------------

// TenderRow mirrors the full public.tenders row — returned by ListTenders.
// Must stay aligned with Database['public']['Tables']['tenders']['Row'] on the frontend.
type TenderRow struct {
	ID                              string    `json:"id"`
	TenderNumber                    string    `json:"tender_number"`
	Title                           string    `json:"title"`
	ClientName                      string    `json:"client_name"`
	Description                     *string   `json:"description"`
	HousingClass                    *string   `json:"housing_class"`
	ConstructionScope               *string   `json:"construction_scope"`
	IsArchived                      bool      `json:"is_archived"`
	CachedGrandTotal                float64   `json:"cached_grand_total"`
	BaseTotal                       float64   `json:"base_total"`
	USDRate                         *float64  `json:"usd_rate"`
	EURRate                         *float64  `json:"eur_rate"`
	CNYRate                         *float64  `json:"cny_rate"`
	AreaClient                      *float64  `json:"area_client"`
	AreaSP                          *float64  `json:"area_sp"`
	SubmissionDeadline              *string   `json:"submission_deadline"`
	Version                         *int64    `json:"version"`
	VolumeTitle                     *string   `json:"volume_title"`
	MarkupTacticID                  *string   `json:"markup_tactic_id"`
	UploadFolder                    *string   `json:"upload_folder"`
	BsmLink                         *string   `json:"bsm_link"`
	TzLink                          *string   `json:"tz_link"`
	QaFormLink                      *string   `json:"qa_form_link"`
	ProjectFolderLink               *string   `json:"project_folder_link"`
	ApplySubcontractMaterialsGrowth *bool     `json:"apply_subcontract_materials_growth"`
	ApplySubcontractWorksGrowth     *bool     `json:"apply_subcontract_works_growth"`
	CreatedBy                       *string   `json:"created_by"`
	CreatedAt                       time.Time `json:"created_at"`
	UpdatedAt                       time.Time `json:"updated_at"`
	// 0-F2: financial calculation state (safe fields only).
	FinancialInputRevision           int64   `json:"financial_input_revision"`
	FinancialCalculationRevision     int64   `json:"financial_calculation_revision"`
	FinancialCalculationStatus       string  `json:"financial_calculation_status"`
	FinancialCalculatedAt            *string `json:"financial_calculated_at"`
	FinancialCalculationErrorCode    *string `json:"financial_calculation_error_code,omitempty"`
	FinancialCalculationErrorMessage *string `json:"financial_calculation_error_message,omitempty"`
}

// tenderFinancialCols — 0-F2 проекция состояния финансового расчёта. ЕДИНЫЙ
// источник: любой запрос, наполняющий TenderRow, обязан её включать.
// Политика на фронте (src/lib/financial/calculationState.ts) fail-closed, и
// пропущенная колонка приезжает не как null, а как "" — `?? 'stale'` её не
// ловит, статус проваливается в 'stale' и НАВСЕГДА блокирует финальный экспорт
// «Формы КП» (FINANCIAL_CALCULATION_NOT_READY) при полностью здоровой БД.
// Ровно это и случилось, когда ListTenders остался со своим списком колонок.
//
// financial_calculated_at ОБЯЗАН идти с ::text — колонка timestamptz, поле
// FinancialCalculatedAt имеет тип *string, без каста pgx падает на скане.
//
// Гарды: scripts/checks/financialRevisionSafety.check.mjs §11 +
// tender_list_query_test.go.
const tenderFinancialCols = `financial_input_revision, financial_calculation_revision,
	financial_calculation_status, financial_calculated_at::text,
	financial_calculation_error_code, financial_calculation_error_message
`

// TenderOverviewRow is the aggregate returned by GetTenderOverview.
type TenderOverviewRow struct {
	ID                string    `json:"id"`
	TenderNumber      string    `json:"tender_number"`
	Title             string    `json:"title"`
	ClientName        string    `json:"client_name"`
	HousingClass      *string   `json:"housing_class"`
	ConstructionScope *string   `json:"construction_scope"`
	IsArchived        bool      `json:"is_archived"`
	CachedGrandTotal  float64   `json:"cached_grand_total"`
	USDRate           *float64  `json:"usd_rate"`
	EURRate           *float64  `json:"eur_rate"`
	CNYRate           *float64  `json:"cny_rate"`
	PositionCount     int64     `json:"position_count"`
	BoqItemCount      int64     `json:"boq_item_count"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// TenderListParams holds optional filters for ListTenders.
type TenderListParams struct {
	IsArchived *bool
	// HousingClass filters by housing_class enum value; empty string = no filter.
	HousingClass string
	// Search does a case-insensitive prefix/substring match on title and client_name.
	Search string
	// Cursor fields for keyset pagination.
	CursorUpdatedAt *time.Time
	CursorID        *string
	Limit           int
}

// ---------------------------------------------------------------------------
// Repository
// ---------------------------------------------------------------------------

// TenderRepo handles read-only database access for the tenders domain.
type TenderRepo struct {
	pool *pgxpool.Pool
}

// NewTenderRepo creates a TenderRepo.
func NewTenderRepo(pool *pgxpool.Pool) *TenderRepo {
	return &TenderRepo{pool: pool}
}

// buildTenderListQuery builds the ListTenders projection and its arguments.
// Pure (no pool, no ctx) — so the financial projection is assertable without a
// test database (tender_list_query_test.go).
//
// base_total (прямые затраты) читается из хранимой tenders.cached_base_total,
// которую пишет RecalculateTenderGrandTotalTx. Считать её здесь подзапросом
// SUM(boq_items.total_amount) НЕЛЬЗЯ: список зовут все страницы и на каждое
// realtime-событие `tenders`, а подзапрос проходил все строки ВОР всех
// тендеров (~700 тыс., 1,3–3 с на вызов) — 13.10.2026 серия записей в ВОР так
// положила API на 14 минут.
func buildTenderListQuery(p TenderListParams) (string, []any) {
	args := []any{}
	argN := 1

	// Build WHERE clauses dynamically.
	where := "WHERE 1=1"

	if p.IsArchived != nil {
		where += fmt.Sprintf(" AND is_archived = $%d", argN)
		args = append(args, *p.IsArchived)
		argN++
	}
	if p.HousingClass != "" {
		where += fmt.Sprintf(" AND housing_class = $%d", argN)
		args = append(args, p.HousingClass)
		argN++
	}
	if p.Search != "" {
		where += fmt.Sprintf(
			" AND (title ILIKE $%d OR client_name ILIKE $%d)",
			argN, argN,
		)
		args = append(args, "%"+p.Search+"%")
		argN++
	}
	if p.CursorUpdatedAt != nil && p.CursorID != nil {
		where += fmt.Sprintf(
			" AND (updated_at, id) < ($%d, $%d)",
			argN, argN+1,
		)
		args = append(args, *p.CursorUpdatedAt, *p.CursorID)
		argN += 2
	}

	limit := p.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	args = append(args, limit)

	q := fmt.Sprintf(`
		SELECT id::text, tender_number, title, client_name,
		       description,
		       housing_class::text, construction_scope::text,
		       is_archived, cached_grand_total,
		       cached_base_total AS base_total,
		       usd_rate, eur_rate, cny_rate,
		       area_client, area_sp,
		       submission_deadline::text,
		       version, volume_title,
		       markup_tactic_id::text,
		       upload_folder, bsm_link, tz_link, qa_form_link, project_folder_link,
		       apply_subcontract_materials_growth, apply_subcontract_works_growth,
		       created_by::text,
		       COALESCE(created_at, NOW()), COALESCE(updated_at, NOW()),
		       `+tenderFinancialCols+`
		FROM public.tenders
		%s
		ORDER BY updated_at DESC, id DESC
		LIMIT $%d
	`, where, argN)

	return q, args
}

// ListTenders returns a page of tenders ordered by (updated_at DESC, id DESC).
// Pagination is keyset-based via CursorUpdatedAt + CursorID.
func (r *TenderRepo) ListTenders(ctx context.Context, p TenderListParams) ([]TenderRow, error) {
	q, args := buildTenderListQuery(p)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("tenderRepo.ListTenders: query: %w", err)
	}
	defer rows.Close()

	var result []TenderRow
	for rows.Next() {
		var row TenderRow
		if err := rows.Scan(
			&row.ID, &row.TenderNumber, &row.Title, &row.ClientName,
			&row.Description,
			&row.HousingClass, &row.ConstructionScope,
			&row.IsArchived, &row.CachedGrandTotal,
			&row.BaseTotal,
			&row.USDRate, &row.EURRate, &row.CNYRate,
			&row.AreaClient, &row.AreaSP,
			&row.SubmissionDeadline,
			&row.Version, &row.VolumeTitle,
			&row.MarkupTacticID,
			&row.UploadFolder, &row.BsmLink, &row.TzLink, &row.QaFormLink, &row.ProjectFolderLink,
			&row.ApplySubcontractMaterialsGrowth, &row.ApplySubcontractWorksGrowth,
			&row.CreatedBy,
			&row.CreatedAt, &row.UpdatedAt,
			&row.FinancialInputRevision, &row.FinancialCalculationRevision,
			&row.FinancialCalculationStatus, &row.FinancialCalculatedAt,
			&row.FinancialCalculationErrorCode, &row.FinancialCalculationErrorMessage,
		); err != nil {
			return nil, fmt.Errorf("tenderRepo.ListTenders: scan: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tenderRepo.ListTenders: rows: %w", err)
	}
	return result, nil
}

// GetTenderOverview fetches header columns plus aggregate counts for one tender.
func (r *TenderRepo) GetTenderOverview(ctx context.Context, tenderID string) (*TenderOverviewRow, error) {
	// Two scalar subqueries instead of LEFT JOIN + COUNT(DISTINCT) + GROUP BY.
	// The original LEFT-JOIN form built a Cartesian product
	// (#positions × #boq_items per tender) and then deduplicated via
	// COUNT DISTINCT — on a real tender with 1181 positions and 5362
	// boq_items that meant 6.3 M intermediate rows, a 359 MB on-disk sort,
	// and ~13 s execution. Two indexed scalar counts return in single-digit
	// milliseconds because both tables have a tender_id index.
	// See docs/yandex-migration/40_TENDER_POSITIONS_OVERVIEW_FIX_RESULT.md.
	const q = `
		SELECT
		    t.id::text,
		    t.tender_number,
		    t.title,
		    t.client_name,
		    t.housing_class::text,
		    t.construction_scope::text,
		    t.is_archived,
		    t.cached_grand_total,
		    t.usd_rate,
		    t.eur_rate,
		    t.cny_rate,
		    (SELECT COUNT(*) FROM public.client_positions cp WHERE cp.tender_id = t.id) AS position_count,
		    (SELECT COUNT(*) FROM public.boq_items        bi WHERE bi.tender_id = t.id) AS boq_item_count,
		    COALESCE(t.created_at, NOW()),
		    COALESCE(t.updated_at, NOW())
		FROM public.tenders t
		WHERE t.id = $1
	`

	row := r.pool.QueryRow(ctx, q, tenderID)
	var ov TenderOverviewRow
	if err := row.Scan(
		&ov.ID, &ov.TenderNumber, &ov.Title, &ov.ClientName,
		&ov.HousingClass, &ov.ConstructionScope,
		&ov.IsArchived, &ov.CachedGrandTotal,
		&ov.USDRate, &ov.EURRate, &ov.CNYRate,
		&ov.PositionCount, &ov.BoqItemCount,
		&ov.CreatedAt, &ov.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("tenderRepo.GetTenderOverview: scan: %w", err)
	}
	return &ov, nil
}
