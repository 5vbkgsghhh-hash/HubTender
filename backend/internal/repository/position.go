package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Write input types
// ---------------------------------------------------------------------------

// CreatePositionInput holds validated fields for inserting a client_position.
type CreatePositionInput struct {
	TenderID         string
	PositionNumber   int
	WorkName         string
	UnitCode         *string
	Volume           *float64
	ParentPositionID *string
	HierarchyLevel   *int
	IsAdditional     *bool
	ItemNo           *string
	CreatedBy        string
}

// UpdatePositionInput holds validated patch fields for a client_position.
type UpdatePositionInput struct {
	PositionNumber   *int
	WorkName         *string
	UnitCode         *string
	Volume           *float64
	ParentPositionID *string
	HierarchyLevel   *int
	IsAdditional     *bool
	ItemNo           *string
}

// ---------------------------------------------------------------------------
// Row types
// ---------------------------------------------------------------------------

// PositionRow mirrors the columns returned by ListPositions.
//
// PositionNumber is float64 because public.client_positions.position_number
// is `numeric` and ~0.7% of real rows (e.g. 4.10, 794.10, 1099.10) are
// fractional — used as a dotted hierarchy notation by the BOQ-builder. The
// PrevPhase 6 default int caused pgx scan errors (cannot convert &{… exp=-2}
// to integer) on every tender that contained such positions. See
// docs/yandex-migration/40_TENDER_POSITIONS_OVERVIEW_FIX_RESULT.md.
type PositionRow struct {
	ID               string   `json:"id"`
	TenderID         string   `json:"tender_id"`
	PositionNumber   float64  `json:"position_number"`
	WorkName         string   `json:"work_name"`
	UnitCode         *string  `json:"unit_code"`
	Volume           *float64 `json:"volume"`
	HierarchyLevel   *int     `json:"hierarchy_level"`
	ParentPositionID *string  `json:"parent_position_id"`
	IsAdditional     *bool    `json:"is_additional"`
	ItemNo           *string  `json:"item_no"`
	TotalMaterial    *float64 `json:"total_material"`
	TotalWorks       *float64 `json:"total_works"`
	// Поля заказчика/ручного ввода — нужны машинному доступу (TenderConnector):
	// примечание заказчика идёт в промпт, section_number/position_name — для
	// сопоставления с ВОР. Все nullable.
	ClientNote    *string   `json:"client_note"`
	SectionNumber *string   `json:"section_number"`
	PositionName  *string   `json:"position_name"`
	ManualVolume  *float64  `json:"manual_volume"`
	ManualNote    *string   `json:"manual_note"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// Repository
// ---------------------------------------------------------------------------

// PositionRepo handles read-only database access for client_positions.
type PositionRepo struct {
	pool *pgxpool.Pool
}

// NewPositionRepo creates a PositionRepo.
func NewPositionRepo(pool *pgxpool.Pool) *PositionRepo {
	return &PositionRepo{pool: pool}
}

// positionScanCols is the common SELECT column list for PositionRow scans.
// Список не квалифицирован по таблице: он же идёт в RETURNING и в проекцию
// ListPositions поверх подзапроса (position_list.go) — там имена латеральных
// колонок подобраны так, чтобы не конфликтовать.
const positionScanCols = `
	id::text, tender_id::text, position_number, work_name,
	unit_code, volume, hierarchy_level,
	parent_position_id::text, is_additional, item_no,
	total_material, total_works,
	client_note, section_number, position_name, manual_volume, manual_note,
	COALESCE(created_at, NOW()), COALESCE(updated_at, NOW())
`

// positionScanTargets — адресаты Scan в порядке positionScanCols.
func positionScanTargets(p *PositionRow) []any {
	return []any{
		&p.ID, &p.TenderID, &p.PositionNumber, &p.WorkName,
		&p.UnitCode, &p.Volume, &p.HierarchyLevel,
		&p.ParentPositionID, &p.IsAdditional, &p.ItemNo,
		&p.TotalMaterial, &p.TotalWorks,
		&p.ClientNote, &p.SectionNumber, &p.PositionName, &p.ManualVolume, &p.ManualNote,
		&p.CreatedAt, &p.UpdatedAt,
	}
}

func scanPositionRow(row interface{ Scan(...any) error }) (*PositionRow, error) {
	var p PositionRow
	if err := row.Scan(positionScanTargets(&p)...); err != nil {
		return nil, err
	}
	return &p, nil
}

// GetPositionByID fetches a single PositionRow by primary key.
func (r *PositionRepo) GetPositionByID(ctx context.Context, id string) (*PositionRow, error) {
	q := "SELECT " + positionScanCols + " FROM public.client_positions WHERE id = $1"
	row := r.pool.QueryRow(ctx, q, id)
	p, err := scanPositionRow(row)
	if err != nil {
		return nil, fmt.Errorf("positionRepo.GetPositionByID: scan: %w", err)
	}
	return p, nil
}

// CreatePosition inserts a new client_position and returns the created row.
func (r *PositionRepo) CreatePosition(ctx context.Context, in CreatePositionInput) (*PositionRow, error) {
	q := `
		INSERT INTO public.client_positions
		    (tender_id, position_number, work_name, unit_code, volume,
		     parent_position_id, hierarchy_level, is_additional, item_no, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING ` + positionScanCols
	row := r.pool.QueryRow(ctx, q,
		in.TenderID, in.PositionNumber, in.WorkName,
		in.UnitCode, in.Volume,
		in.ParentPositionID, in.HierarchyLevel, in.IsAdditional, in.ItemNo,
		in.CreatedBy,
	)
	p, err := scanPositionRow(row)
	if err != nil {
		return nil, fmt.Errorf("positionRepo.CreatePosition: scan: %w", err)
	}
	return p, nil
}

// jsonbOrNil returns nil for an empty or literal-"null" JSON payload so the
// column is written as SQL NULL rather than jsonb 'null'.
func jsonbOrNil(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return raw
}

// BulkPositionInsert is one row of the BOQ upload (initial tender positions).
type BulkPositionInsert struct {
	TenderID         string          `json:"tender_id"`
	PositionNumber   float64         `json:"position_number"`
	WorkName         string          `json:"work_name"`
	UnitCode         *string         `json:"unit_code"`
	Volume           *float64        `json:"volume"`
	ClientNote       *string         `json:"client_note"`
	ItemNo           *string         `json:"item_no"`
	HierarchyLevel   *int            `json:"hierarchy_level"`
	IsAdditional     *bool           `json:"is_additional"`
	ParentPositionID *string         `json:"parent_position_id"`
	RichRuns         json.RawMessage `json:"rich_runs"` // зачёркивание из Excel; NULL если нет
}

// BulkInsertPositions atomically inserts every input row. Defaults for the
// schema-default columns (totals, *_per_unit) are left to PostgreSQL.
func (r *PositionRepo) BulkInsertPositions(ctx context.Context, rows []BulkPositionInsert) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("positionRepo.BulkInsertPositions: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	const q = `
		INSERT INTO public.client_positions
			(tender_id, position_number, work_name, unit_code, volume,
			 client_note, item_no, hierarchy_level, is_additional, parent_position_id,
			 rich_runs)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, COALESCE($8, 0),
		        COALESCE($9, false), $10::uuid, $11::jsonb)
	`
	for _, row := range rows {
		if _, err := tx.Exec(ctx, q,
			row.TenderID, row.PositionNumber, row.WorkName, row.UnitCode, row.Volume,
			row.ClientNote, row.ItemNo, row.HierarchyLevel,
			row.IsAdditional, row.ParentPositionID, jsonbOrNil(row.RichRuns),
		); err != nil {
			return 0, fmt.Errorf("positionRepo.BulkInsertPositions: insert: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("positionRepo.BulkInsertPositions: commit: %w", err)
	}
	return len(rows), nil
}

// BulkDeletePositions deletes the given client_positions and all their
// boq_items in one transaction (replicating the legacy two-step batched
// delete in usePositionDelete — raw delete, no audit, same as before).
func (r *PositionRepo) BulkDeletePositions(ctx context.Context, positionIDs []string, changedBy string) error {
	if len(positionIDs) == 0 {
		return nil
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("positionRepo.BulkDeletePositions: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := setAuditUser(ctx, tx, changedBy); err != nil {
		return fmt.Errorf("positionRepo.BulkDeletePositions: %w", err)
	}

	// Категория A (0.1.2.4a): каскадное удаление BOQ меняет состав
	// cached_grand_total — затронутые тендеры определяем ДО удаления и
	// пересчитываем каждый РОВНО ОДИН РАЗ (не per-row) в этой же транзакции.
	affectedTenders, err := affectedTenderIDs(ctx, tx, positionIDs)
	if err != nil {
		return fmt.Errorf("positionRepo.BulkDeletePositions: %w", err)
	}

	// 0-F2 (category A): ONE revision bump per affected tender for the whole
	// batch command (never per row); commercial recalc follows async.
	for _, tenderID := range affectedTenders {
		if _, err := MarkTenderFinancialInputsChangedTx(ctx, tx, tenderID, "bulk_delete_positions"); err != nil {
			return fmt.Errorf("positionRepo.BulkDeletePositions: %w", err)
		}
	}

	if _, err := tx.Exec(ctx,
		`DELETE FROM public.boq_items WHERE client_position_id = ANY($1::uuid[])`,
		positionIDs,
	); err != nil {
		return fmt.Errorf("positionRepo.BulkDeletePositions: delete boq_items: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM public.client_positions WHERE id = ANY($1::uuid[])`,
		positionIDs,
	); err != nil {
		return fmt.Errorf("positionRepo.BulkDeletePositions: delete positions: %w", err)
	}
	for _, tenderID := range affectedTenders {
		if _, err := RecalculateTenderGrandTotalTx(ctx, tx, tenderID); err != nil {
			return fmt.Errorf("positionRepo.BulkDeletePositions: grand total: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("positionRepo.BulkDeletePositions: commit: %w", err)
	}
	return nil
}

// affectedTenderIDs returns the distinct tenders owning the given positions —
// resolved BEFORE a cascade delete so the grand total can be recomputed once
// per affected tender.
func affectedTenderIDs(ctx context.Context, q Querier, positionIDs []string) ([]string, error) {
	rows, err := q.Query(ctx,
		`SELECT DISTINCT tender_id::text FROM public.client_positions WHERE id = ANY($1::uuid[])`,
		positionIDs)
	if err != nil {
		return nil, fmt.Errorf("affectedTenderIDs: %w", err)
	}
	defer rows.Close()
	out := make([]string, 0, 1)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("affectedTenderIDs scan: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// BoqNameEmbed mirrors the work_names(name) / material_names(name) embed.
type BoqNameEmbed struct {
	Name string `json:"name"`
}

// BoqPreviewRow is the existing-items preview shape consumed by
// useMassBoqImport.loadExistingItems.
type BoqPreviewRow struct {
	ID               string        `json:"id"`
	ClientPositionID string        `json:"client_position_id"`
	BoqItemType      *string       `json:"boq_item_type"`
	Quantity         *float64      `json:"quantity"`
	TotalAmount      *float64      `json:"total_amount"`
	WorkNames        *BoqNameEmbed `json:"work_names"`
	MaterialNames    *BoqNameEmbed `json:"material_names"`
}

// ListBoqPreviewByPositions returns boq_items (subset + name embeds) for the
// given positions, ordered by sort_number.
func (r *PositionRepo) ListBoqPreviewByPositions(ctx context.Context, positionIDs []string) ([]BoqPreviewRow, error) {
	if len(positionIDs) == 0 {
		return []BoqPreviewRow{}, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT bi.id::text, bi.client_position_id::text, bi.boq_item_type::text,
		       bi.quantity, bi.total_amount, wn.name, mn.name
		FROM public.boq_items bi
		LEFT JOIN public.work_names wn ON wn.id = bi.work_name_id
		LEFT JOIN public.material_names mn ON mn.id = bi.material_name_id
		WHERE bi.client_position_id = ANY($1::uuid[])
		ORDER BY bi.sort_number
	`, positionIDs)
	if err != nil {
		return nil, fmt.Errorf("positionRepo.ListBoqPreviewByPositions: %w", err)
	}
	defer rows.Close()
	out := make([]BoqPreviewRow, 0)
	for rows.Next() {
		var b BoqPreviewRow
		var wnName, mnName *string
		if err := rows.Scan(&b.ID, &b.ClientPositionID, &b.BoqItemType,
			&b.Quantity, &b.TotalAmount, &wnName, &mnName); err != nil {
			return nil, fmt.Errorf("positionRepo.ListBoqPreviewByPositions scan: %w", err)
		}
		if wnName != nil {
			b.WorkNames = &BoqNameEmbed{Name: *wnName}
		}
		if mnName != nil {
			b.MaterialNames = &BoqNameEmbed{Name: *mnName}
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// UpdatePositionsNote sets manual_note on every given position (single or
// bulk paste of "примечание ГП").
func (r *PositionRepo) UpdatePositionsNote(ctx context.Context, ids []string, note string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE public.client_positions SET manual_note = $2, updated_at = NOW()
		 WHERE id = ANY($1::uuid[])`,
		ids, note,
	)
	if err != nil {
		return fmt.Errorf("positionRepo.UpdatePositionsNote: %w", err)
	}
	return nil
}

// ClearPositionsBoq deletes all boq_items of the given positions and zeroes
// their totals — one tx (replicates the legacy delete-then-zero two-step).
func (r *PositionRepo) ClearPositionsBoq(ctx context.Context, ids []string, changedBy string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("positionRepo.ClearPositionsBoq: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := setAuditUser(ctx, tx, changedBy); err != nil {
		return fmt.Errorf("positionRepo.ClearPositionsBoq: %w", err)
	}

	// Категория A (0.1.2.4a): удаление BOQ-строк с commercial values — пересчёт
	// cached_grand_total один раз на каждый затронутый тендер в этой же tx.
	affectedTenders, err := affectedTenderIDs(ctx, tx, ids)
	if err != nil {
		return fmt.Errorf("positionRepo.ClearPositionsBoq: %w", err)
	}

	// 0-F2 (category A): ONE revision bump per affected tender for the whole
	// batch command (never per row); commercial recalc follows async.
	for _, tenderID := range affectedTenders {
		if _, err := MarkTenderFinancialInputsChangedTx(ctx, tx, tenderID, "clear_positions_boq"); err != nil {
			return fmt.Errorf("positionRepo.ClearPositionsBoq: %w", err)
		}
	}

	if _, err := tx.Exec(ctx,
		`DELETE FROM public.boq_items WHERE client_position_id = ANY($1::uuid[])`,
		ids,
	); err != nil {
		return fmt.Errorf("positionRepo.ClearPositionsBoq: delete boq_items: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE public.client_positions
		 SET total_material = 0, total_works = 0, updated_at = NOW()
		 WHERE id = ANY($1::uuid[])`,
		ids,
	); err != nil {
		return fmt.Errorf("positionRepo.ClearPositionsBoq: zero totals: %w", err)
	}
	for _, tenderID := range affectedTenders {
		if _, err := RecalculateTenderGrandTotalTx(ctx, tx, tenderID); err != nil {
			return fmt.Errorf("positionRepo.ClearPositionsBoq: grand total: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("positionRepo.ClearPositionsBoq: commit: %w", err)
	}
	return nil
}

// ShiftPositionsLevel adds delta to hierarchy_level (floored at 0) for the
// given positions, in a single statement (replaces the legacy
// select-then-loop in handleBulkLevelChange).
func (r *PositionRepo) ShiftPositionsLevel(ctx context.Context, ids []string, delta int) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE public.client_positions
		 SET hierarchy_level = GREATEST(COALESCE(hierarchy_level, 0) + $2, 0),
		     updated_at = NOW()
		 WHERE id = ANY($1::uuid[])`,
		ids, delta,
	)
	if err != nil {
		return fmt.Errorf("positionRepo.ShiftPositionsLevel: %w", err)
	}
	return nil
}

// UpdatePosition applies non-nil fields from in to the position with the
// given id and returns the updated row.
func (r *PositionRepo) UpdatePosition(ctx context.Context, id string, in UpdatePositionInput) (*PositionRow, error) {
	args := []any{}
	argN := 1
	setClauses := ""

	set := func(col string, val any) {
		if setClauses != "" {
			setClauses += ", "
		}
		setClauses += fmt.Sprintf("%s = $%d", col, argN)
		args = append(args, val)
		argN++
	}

	if in.PositionNumber != nil {
		set("position_number", *in.PositionNumber)
	}
	if in.WorkName != nil {
		set("work_name", *in.WorkName)
	}
	if in.UnitCode != nil {
		set("unit_code", *in.UnitCode)
	}
	if in.Volume != nil {
		set("volume", *in.Volume)
	}
	if in.ParentPositionID != nil {
		set("parent_position_id", *in.ParentPositionID)
	}
	if in.HierarchyLevel != nil {
		set("hierarchy_level", *in.HierarchyLevel)
	}
	if in.IsAdditional != nil {
		set("is_additional", *in.IsAdditional)
	}
	if in.ItemNo != nil {
		set("item_no", *in.ItemNo)
	}

	if setClauses == "" {
		return r.GetPositionByID(ctx, id)
	}

	setClauses += fmt.Sprintf(", updated_at = NOW()")
	args = append(args, id)

	q := fmt.Sprintf("UPDATE public.client_positions SET %s WHERE id = $%d RETURNING "+positionScanCols,
		setClauses, argN)
	row := r.pool.QueryRow(ctx, q, args...)
	p, err := scanPositionRow(row)
	if err != nil {
		return nil, fmt.Errorf("positionRepo.UpdatePosition: scan: %w", err)
	}
	return p, nil
}
