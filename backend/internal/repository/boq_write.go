package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/su10/hubtender/backend/internal/calc"
)

// loadTenderRates fetches the three currency multipliers from public.tenders
// inside an open transaction. Used by CreateBoqItem / UpdateBoqItem to drive
// the same total_amount calculation that the frontend used to do.
func loadTenderRates(ctx context.Context, tx pgx.Tx, tenderID string) (calc.CurrencyRates, error) {
	const q = `SELECT usd_rate, eur_rate, cny_rate FROM public.tenders WHERE id = $1`
	var rates calc.CurrencyRates
	if err := tx.QueryRow(ctx, q, tenderID).Scan(&rates.USDRate, &rates.EURRate, &rates.CNYRate); err != nil {
		return calc.CurrencyRates{}, fmt.Errorf("loadTenderRates: %w", err)
	}
	return rates, nil
}

// boqAmountInputFromRow projects a stored BoqItemRow into the calc input shape.
func boqAmountInputFromRow(b *BoqItemRow) calc.BoqItemAmountInput {
	in := calc.BoqItemAmountInput{
		BoqItemType:            b.BoqItemType,
		Quantity:               b.Quantity,
		UnitRate:               b.UnitRate,
		DeliveryAmount:         b.DeliveryAmount,
		ConsumptionCoefficient: b.ConsumptionCoefficient,
		ParentWorkItemID:       b.ParentWorkItemID,
		TotalAmount:            b.TotalAmount,
	}
	if b.CurrencyType != nil {
		in.CurrencyType = *b.CurrencyType
	}
	if b.DeliveryPriceType != nil {
		in.DeliveryPriceType = *b.DeliveryPriceType
	}
	return in
}

// ---------------------------------------------------------------------------
// Write input types
// ---------------------------------------------------------------------------

// CreateBoqItemInput holds validated fields for inserting a boq_item.
type CreateBoqItemInput struct {
	ClientPositionID       string
	TenderID               string
	BoqItemType            string
	MaterialType           *string
	Description            *string
	UnitCode               *string
	Quantity               *float64
	BaseQuantity           *float64
	ConversionCoefficient  *float64
	UnitRate               *float64
	CurrencyType           *string
	DeliveryPriceType      *string
	DeliveryAmount         *float64
	ConsumptionCoefficient *float64
	DetailCostCategoryID   *string
	MaterialNameID         *string
	WorkNameID             *string
	ParentWorkItemID       *string
	SortNumber             *int
	QuoteLink              *string
	QuotePriceDate         *string // YYYY-MM-DD; metadata-only (1.3)
	QuoteValidUntil        *string // YYYY-MM-DD; metadata-only (1.3)
	CreatedBy              string  // app users UUID for audit (changed_by)
}

// UpdateBoqItemInput holds validated patch fields for a boq_item.
type UpdateBoqItemInput struct {
	BoqItemType            *string
	MaterialType           *string
	Description            *string
	UnitCode               *string
	Quantity               *float64
	BaseQuantity           *float64
	ConversionCoefficient  *float64
	UnitRate               *float64
	CurrencyType           *string
	DeliveryPriceType      *string
	DeliveryAmount         *float64
	ConsumptionCoefficient *float64
	DetailCostCategoryID   *string
	MaterialNameID         *string
	WorkNameID             *string
	ParentWorkItemID       *string
	SortNumber             *int
	QuoteLink              *string
	QuotePriceDate         *string // YYYY-MM-DD; "" = очистить; metadata-only (1.3)
	QuoteValidUntil        *string // YYYY-MM-DD; "" = очистить; metadata-only (1.3)
	ChangedBy              string  // app users UUID for audit (changed_by)
}

// ---------------------------------------------------------------------------
// boq SELECT column list (must match BoqItemRow Scan order)
// ---------------------------------------------------------------------------

const boqScanCols = `
	id::text, client_position_id::text, tender_id::text,
	boq_item_type::text, material_type::text, description,
	unit_code, quantity, base_quantity, conversion_coefficient, unit_rate,
	currency_type::text, delivery_price_type::text, delivery_amount,
	consumption_coefficient, total_amount, sort_number,
	detail_cost_category_id::text, parent_work_item_id::text,
	material_name_id::text, work_name_id::text, quote_link,
	to_char(quote_price_date, 'YYYY-MM-DD'), to_char(quote_valid_until, 'YYYY-MM-DD'),
	COALESCE(created_at,NOW()), COALESCE(updated_at,NOW())
`

// GetBoqItemByID fetches a single BoqItemRow by primary key.
func (r *BoqRepo) GetBoqItemByID(ctx context.Context, id string) (*BoqItemRow, error) {
	q := "SELECT " + boqScanCols + " FROM public.boq_items WHERE id = $1"
	row := r.pool.QueryRow(ctx, q, id)
	item, err := scanBoqItemRow(row)
	if err != nil {
		return nil, fmt.Errorf("boqRepo.GetBoqItemByID: scan: %w", err)
	}
	return item, nil
}

func scanBoqItemRow(row interface{ Scan(...any) error }) (*BoqItemRow, error) {
	var b BoqItemRow
	if err := row.Scan(
		&b.ID, &b.ClientPositionID, &b.TenderID,
		&b.BoqItemType, &b.MaterialType, &b.Description,
		&b.UnitCode, &b.Quantity,
		&b.BaseQuantity, &b.ConversionCoefficient,
		&b.UnitRate,
		&b.CurrencyType, &b.DeliveryPriceType, &b.DeliveryAmount,
		&b.ConsumptionCoefficient, &b.TotalAmount, &b.SortNumber,
		&b.DetailCostCategoryID, &b.ParentWorkItemID,
		&b.MaterialNameID, &b.WorkNameID, &b.QuoteLink,
		&b.QuotePriceDate, &b.QuoteValidUntil,
		&b.CreatedAt, &b.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &b, nil
}

// boqRowJSON marshals a BoqItemRow to a JSON-compatible map.
func boqRowJSON(b *BoqItemRow) ([]byte, error) {
	return json.Marshal(b)
}

// changedFields computes which top-level fields differ between old and new row
// by comparing their JSON representations.
func changedFields(old, new *BoqItemRow) []string {
	type pair struct {
		name string
		o, n any
	}
	pairs := []pair{
		{"boq_item_type", old.BoqItemType, new.BoqItemType},
		{"material_type", old.MaterialType, new.MaterialType},
		{"description", old.Description, new.Description},
		{"unit_code", old.UnitCode, new.UnitCode},
		{"quantity", old.Quantity, new.Quantity},
		{"base_quantity", old.BaseQuantity, new.BaseQuantity},
		{"conversion_coefficient", old.ConversionCoefficient, new.ConversionCoefficient},
		{"unit_rate", old.UnitRate, new.UnitRate},
		{"currency_type", old.CurrencyType, new.CurrencyType},
		{"delivery_price_type", old.DeliveryPriceType, new.DeliveryPriceType},
		{"delivery_amount", old.DeliveryAmount, new.DeliveryAmount},
		{"consumption_coefficient", old.ConsumptionCoefficient, new.ConsumptionCoefficient},
		{"total_amount", old.TotalAmount, new.TotalAmount},
		{"sort_number", old.SortNumber, new.SortNumber},
		{"detail_cost_category_id", old.DetailCostCategoryID, new.DetailCostCategoryID},
		{"parent_work_item_id", old.ParentWorkItemID, new.ParentWorkItemID},
		{"material_name_id", old.MaterialNameID, new.MaterialNameID},
		{"work_name_id", old.WorkNameID, new.WorkNameID},
		{"quote_link", old.QuoteLink, new.QuoteLink},
		{"quote_price_date", old.QuotePriceDate, new.QuotePriceDate},
		{"quote_valid_until", old.QuoteValidUntil, new.QuoteValidUntil},
	}
	var out []string
	for _, p := range pairs {
		oj, _ := json.Marshal(p.o)
		nj, _ := json.Marshal(p.n)
		if string(oj) != string(nj) {
			out = append(out, p.name)
		}
	}
	return out
}

// setAuditUser propagates the acting user into the app.user_id GUC so the
// boq_items audit trigger (log_boq_items_changes → auth.uid()) records
// changed_by instead of writing a NULL-author "Системная операция" row.
// Transaction-local (set_config is_local=true); no-op for an empty userID.
// Call right after BeginTx, before any boq_items mutation in the same tx.
func setAuditUser(ctx context.Context, tx pgx.Tx, userID string) error {
	if userID == "" {
		return nil
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, userID); err != nil {
		return fmt.Errorf("setAuditUser: %w", err)
	}
	return nil
}

// skipBoqAuditTrigger tells the boq_items audit trigger (log_boq_items_changes)
// to stand down for the current transaction. Methods that write their own
// curated audit row(s) via insertAudit call this first; otherwise the trigger
// also fires and writes a duplicate row with a NULL author (the app.user_id
// GUC is not set on these paths). Transaction-local (set_config is_local=true).
func skipBoqAuditTrigger(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `SELECT set_config('app.skip_boq_audit', 'on', true)`); err != nil {
		return fmt.Errorf("skipBoqAuditTrigger: %w", err)
	}
	return nil
}

// insertAudit writes a single row to public.boq_items_audit inside an
// already-open transaction. Nil []byte values are sent as SQL NULL.
func insertAudit(
	ctx context.Context,
	tx pgx.Tx,
	boqItemID, operation, changedBy string,
	changedFlds []string,
	oldData, newData []byte,
) error {
	const q = `
		INSERT INTO public.boq_items_audit
		    (boq_item_id, operation_type, changed_by, changed_fields, old_data, new_data, changed_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
	`
	var changedByParam any
	if changedBy != "" {
		changedByParam = changedBy
	}
	// pgx v5 sends nil []byte as SQL NULL for jsonb columns.
	var oldParam, newParam any
	if len(oldData) > 0 {
		oldParam = oldData
	}
	if len(newData) > 0 {
		newParam = newData
	}
	_, err := tx.Exec(ctx, q,
		boqItemID, operation, changedByParam, changedFlds, oldParam, newParam, time.Now().UTC(),
	)
	return err
}

// CreateBoqItem inserts a new boq_item and writes an INSERT audit row, all in
// one transaction. total_amount is computed server-side via calc on the same
// inputs the frontend used to compute it from, so the row is never written
// without a price.
func (r *BoqRepo) CreateBoqItem(ctx context.Context, in CreateBoqItemInput) (*BoqItemRow, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("boqRepo.CreateBoqItem: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := skipBoqAuditTrigger(ctx, tx); err != nil {
		return nil, fmt.Errorf("boqRepo.CreateBoqItem: %w", err)
	}

	// 0-F2 (category A): one revision bump; commercial recalc follows async.
	if _, err := MarkTenderFinancialInputsChangedTx(ctx, tx, in.TenderID, "boq_create"); err != nil {
		return nil, fmt.Errorf("boqRepo.CreateBoqItem: %w", err)
	}

	// 1.3: quote source dates — та же валидация, что и в UpdateBoqItem.
	if err := validateQuoteDates(in.QuotePriceDate, in.QuoteValidUntil, &BoqItemRow{}); err != nil {
		return nil, err
	}
	qpd, qvu := any(nil), any(nil)
	if in.QuotePriceDate != nil {
		qpd = nullIfEmptyDate(*in.QuotePriceDate)
	}
	if in.QuoteValidUntil != nil {
		qvu = nullIfEmptyDate(*in.QuoteValidUntil)
	}

	sortNum := 0
	if in.SortNumber != nil {
		sortNum = *in.SortNumber
	}

	rates, err := loadTenderRates(ctx, tx, in.TenderID)
	if err != nil {
		return nil, fmt.Errorf("boqRepo.CreateBoqItem: %w", err)
	}

	calcIn := calc.BoqItemAmountInput{
		BoqItemType:            in.BoqItemType,
		Quantity:               in.Quantity,
		UnitRate:               in.UnitRate,
		DeliveryAmount:         in.DeliveryAmount,
		ConsumptionCoefficient: in.ConsumptionCoefficient,
		ParentWorkItemID:       in.ParentWorkItemID,
	}
	if in.CurrencyType != nil {
		calcIn.CurrencyType = *in.CurrencyType
	}
	if in.DeliveryPriceType != nil {
		calcIn.DeliveryPriceType = *in.DeliveryPriceType
	}
	totalAmount, err := calc.CalculateBoqItemTotalAmount(calcIn, rates)
	if err != nil {
		return nil, fmt.Errorf("boqRepo.CreateBoqItem: %w", err)
	}

	// NOTE: public.boq_items in the live Yandex schema has no `created_by`
	// column — see db/yandex/sql/03_tables.sql. The authoring user is recorded
	// via the audit row (boq_items_audit.changed_by) below.
	q := `
		INSERT INTO public.boq_items
		    (client_position_id, tender_id, boq_item_type, material_type, description,
		     unit_code, quantity, base_quantity, conversion_coefficient,
		     unit_rate, currency_type, delivery_price_type,
		     delivery_amount, consumption_coefficient, total_amount,
		     detail_cost_category_id, material_name_id, work_name_id,
		     parent_work_item_id, sort_number, quote_link,
		     quote_price_date, quote_valid_until)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,
		        $22::date, $23::date)
		RETURNING ` + boqScanCols
	row := tx.QueryRow(ctx, q,
		in.ClientPositionID, in.TenderID, in.BoqItemType,
		in.MaterialType, in.Description,
		in.UnitCode, in.Quantity, in.BaseQuantity, in.ConversionCoefficient,
		in.UnitRate, in.CurrencyType, in.DeliveryPriceType,
		in.DeliveryAmount, in.ConsumptionCoefficient, totalAmount,
		in.DetailCostCategoryID,
		in.MaterialNameID, in.WorkNameID,
		in.ParentWorkItemID, sortNum, in.QuoteLink,
		qpd, qvu,
	)
	item, err := scanBoqItemRow(row)
	if err != nil {
		return nil, fmt.Errorf("boqRepo.CreateBoqItem: scan: %w", err)
	}

	newJSON, _ := boqRowJSON(item)
	if err := insertAudit(ctx, tx, item.ID, "INSERT", in.CreatedBy, nil, nil, newJSON); err != nil {
		return nil, fmt.Errorf("boqRepo.CreateBoqItem: audit: %w", err)
	}

	// Position totals are derived from its rows — refreshed in the same tx.
	if err := recomputePositionTotalsByIDsTx(ctx, tx, []string{item.ClientPositionID}); err != nil {
		return nil, fmt.Errorf("boqRepo.CreateBoqItem: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("boqRepo.CreateBoqItem: commit: %w", err)
	}
	return item, nil
}

