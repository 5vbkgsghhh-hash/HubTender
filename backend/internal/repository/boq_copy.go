package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// CopyResult is the outcome of CopyPositionItems.
type CopyResult struct {
	WorksCount     int `json:"works_count"`
	MaterialsCount int `json:"materials_count"`
	TotalCopied    int `json:"total_copied"`
	// TenderID is the ONE tender the copy actually changed. The service uses it to
	// invalidate exactly that tender's cache — the affected tender is known from
	// the operation, never inferred from whichever rows happened to be updated.
	TenderID string `json:"-"`
}

// ErrCopyTenderMismatch is returned when source/target positions belong to
// different tenders.
var ErrCopyTenderMismatch = errors.New("positions belong to different tenders")

// CopyPositionItems copies every boq_item from sourcePositionID into
// targetPositionID in one transaction, preserving parent_work_item_id
// relationships via index mapping. It also refreshes total_material /
// total_works on the target position and writes one INSERT audit row per
// new item. Returns a per-row count summary.
func (r *BoqRepo) CopyPositionItems(
	ctx context.Context, sourcePositionID, targetPositionID, changedBy string,
) (*CopyResult, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("boqRepo.CopyPositionItems: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := setAuditUser(ctx, tx, changedBy); err != nil {
		return nil, err
	}

	if err := skipBoqAuditTrigger(ctx, tx); err != nil {
		return nil, fmt.Errorf("boqRepo.CopyPositionItems: %w", err)
	}

	// Validate positions + same-tender constraint inside the tx.
	var srcTender, tgtTender string
	if err := tx.QueryRow(ctx,
		`SELECT tender_id::text FROM public.client_positions WHERE id = $1`, sourcePositionID,
	).Scan(&srcTender); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("boqRepo.CopyPositionItems: source not found")
		}
		return nil, fmt.Errorf("boqRepo.CopyPositionItems: source lookup: %w", err)
	}
	if err := tx.QueryRow(ctx,
		`SELECT tender_id::text FROM public.client_positions WHERE id = $1`, targetPositionID,
	).Scan(&tgtTender); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("boqRepo.CopyPositionItems: target not found")
		}
		return nil, fmt.Errorf("boqRepo.CopyPositionItems: target lookup: %w", err)
	}
	if srcTender != tgtTender {
		return nil, ErrCopyTenderMismatch
	}

	// 0-F2 (category B): one revision bump for the whole copy command; the full
	// recalculation below finishes with the success CAS before commit.
	revision, err := MarkTenderFinancialInputsChangedTx(ctx, tx, tgtTender, "copy_position_items")
	if err != nil {
		return nil, fmt.Errorf("boqRepo.CopyPositionItems: %w", err)
	}

	// Read source items in stable order — CLASS A (source inputs) ONLY.
	// total_amount / commercial_markup / total_commercial_* are CALCULATED values:
	// they are deliberately NOT selected, so they cannot be copied as authoritative.
	// The target row's derived values are recomputed below from the target tender's
	// FX rates and configuration.
	type srcRow struct {
		ID                     string
		BoqItemType            string
		MaterialType           *string
		MaterialNameID         *string
		WorkNameID             *string
		UnitCode               *string
		Quantity               *float64
		BaseQuantity           *float64
		ConsumptionCoefficient *float64
		ConversionCoefficient  *float64
		ParentWorkItemID       *string
		DeliveryPriceType      *string
		DeliveryAmount         *float64
		CurrencyType           *string
		UnitRate               *float64
		DetailCostCategoryID   *string
		QuoteLink              *string
		Description            *string
		QuotePriceDate         *string
		QuoteValidUntil        *string
	}
	rows, err := tx.Query(ctx, `
		SELECT id::text, boq_item_type::text, material_type::text,
		       material_name_id::text, work_name_id::text, unit_code,
		       quantity, base_quantity, consumption_coefficient, conversion_coefficient,
		       parent_work_item_id::text, delivery_price_type::text, delivery_amount,
		       currency_type::text, unit_rate,
		       detail_cost_category_id::text, quote_link, description,
		       to_char(quote_price_date, 'YYYY-MM-DD'), to_char(quote_valid_until, 'YYYY-MM-DD')
		FROM public.boq_items
		WHERE client_position_id = $1
		ORDER BY sort_number ASC, id ASC
	`, sourcePositionID)
	if err != nil {
		return nil, fmt.Errorf("boqRepo.CopyPositionItems: source items: %w", err)
	}
	var src []srcRow
	func() {
		defer rows.Close()
		for rows.Next() {
			var s srcRow
			if err = rows.Scan(
				&s.ID, &s.BoqItemType, &s.MaterialType,
				&s.MaterialNameID, &s.WorkNameID, &s.UnitCode,
				&s.Quantity, &s.BaseQuantity, &s.ConsumptionCoefficient, &s.ConversionCoefficient,
				&s.ParentWorkItemID, &s.DeliveryPriceType, &s.DeliveryAmount,
				&s.CurrencyType, &s.UnitRate,
				&s.DetailCostCategoryID, &s.QuoteLink, &s.Description,
				&s.QuotePriceDate, &s.QuoteValidUntil,
			); err != nil {
				return
			}
			src = append(src, s)
		}
		err = rows.Err()
	}()
	if err != nil {
		return nil, fmt.Errorf("boqRepo.CopyPositionItems: source scan: %w", err)
	}
	if len(src) == 0 {
		return nil, fmt.Errorf("boqRepo.CopyPositionItems: source has no items")
	}

	// Validate every parent link against the copied set BEFORE inserting anything:
	// a declared parent must resolve to a copied WORK row. An unresolvable /
	// non-work / self parent is a blocking InvalidBoqParentError — never a silent
	// cleared link, and never a source UUID leaking into the target.
	refs := make([]CopiedParentRef, len(src))
	for i, s := range src {
		refs[i] = CopiedParentRef{ID: s.ID, ParentID: s.ParentWorkItemID, ItemType: s.BoqItemType}
	}
	parentIdx, err := ResolveCopiedParents(refs)
	if err != nil {
		return nil, fmt.Errorf("boqRepo.CopyPositionItems: %w", err)
	}

	// Insert clones (parent_work_item_id = NULL for now). Derived money columns are
	// NOT in the column list at all — they stay NULL until the authoritative
	// recompute below, inside this same (uncommitted) transaction.
	newIDs := make([]string, len(src))
	insertQ := `
		INSERT INTO public.boq_items (
		    tender_id, client_position_id, sort_number,
		    boq_item_type, material_type, material_name_id, work_name_id,
		    unit_code, quantity, base_quantity,
		    consumption_coefficient, conversion_coefficient,
		    parent_work_item_id, delivery_price_type, delivery_amount,
		    currency_type, unit_rate,
		    detail_cost_category_id, quote_link, description, quote_price_date, quote_valid_until
		) VALUES (
		    $1, $2, $3,
		    $4::boq_item_type, $5::material_type, $6, $7,
		    $8, $9, $10,
		    $11, $12,
		    NULL, $13::delivery_price_type, $14,
		    $15::currency_type, $16,
		    $17, $18, $19, $20::date, $21::date
		)
		RETURNING id::text
	`
	for i, s := range src {
		sortNum := i + 1
		if err := tx.QueryRow(ctx, insertQ,
			tgtTender, targetPositionID, sortNum,
			s.BoqItemType, s.MaterialType, s.MaterialNameID, s.WorkNameID,
			s.UnitCode, s.Quantity, s.BaseQuantity,
			s.ConsumptionCoefficient, s.ConversionCoefficient,
			s.DeliveryPriceType, s.DeliveryAmount,
			s.CurrencyType, s.UnitRate,
			s.DetailCostCategoryID, s.QuoteLink, s.Description,
			s.QuotePriceDate, s.QuoteValidUntil,
		).Scan(&newIDs[i]); err != nil {
			return nil, fmt.Errorf("boqRepo.CopyPositionItems: insert %d: %w", i, err)
		}
	}

	// Restore parent_work_item_id from the VALIDATED plan (target UUIDs only), in
	// ONE bulk UPDATE.
	childIDs := make([]string, 0, len(src))
	parentIDs := make([]string, 0, len(src))
	for i := range src {
		if parentIdx[i] < 0 {
			continue
		}
		childIDs = append(childIDs, newIDs[i])
		parentIDs = append(parentIDs, newIDs[parentIdx[i]])
	}
	if len(childIDs) > 0 {
		if _, err := tx.Exec(ctx, `
			UPDATE public.boq_items b
			SET parent_work_item_id = u.new_parent
			FROM UNNEST($1::uuid[], $2::uuid[]) AS u(child_id, new_parent)
			WHERE b.id = u.child_id
		`, childIDs, parentIDs); err != nil {
			return nil, fmt.Errorf("boqRepo.CopyPositionItems: restore parent links: %w", err)
		}
	}

	// AUTHORITATIVE total_amount: recomputed from the persisted target inputs +
	// the TARGET tender's FX rates (calc.CalculateBoqItemTotalAmount). Parent links
	// are already restored, so calc sees the same effective parent the row keeps.
	// Fail-closed on a missing FX rate → the whole tx rolls back.
	if _, err := RecomputeBoqTotalAmountsTx(ctx, tx, tgtTender, newIDs); err != nil {
		return nil, fmt.Errorf("boqRepo.CopyPositionItems: %w", err)
	}

	// Recompute target position totals.
	if _, err := tx.Exec(ctx, `
		UPDATE public.client_positions cp
		   SET total_material = COALESCE(agg.mat, 0),
		       total_works    = COALESCE(agg.wrk, 0),
		       updated_at     = NOW()
		  FROM (
		    SELECT
		      SUM(CASE WHEN bi.boq_item_type::text IN ('мат','суб-мат','мат-комп.')
		               THEN COALESCE(bi.total_amount, 0) ELSE 0 END) AS mat,
		      SUM(CASE WHEN bi.boq_item_type::text IN ('раб','суб-раб','раб-комп.')
		               THEN COALESCE(bi.total_amount, 0) ELSE 0 END) AS wrk
		    FROM public.boq_items bi
		    WHERE bi.client_position_id = $1
		  ) agg
		 WHERE cp.id = $1
	`, targetPositionID); err != nil {
		return nil, fmt.Errorf("boqRepo.CopyPositionItems: recompute totals: %w", err)
	}

	// Audit: one INSERT row per new boq_item. Best-effort — capture a minimal
	// payload identifying the source for traceability.
	for i, newID := range newIDs {
		payload := []byte(fmt.Sprintf(
			`{"id":"%s","client_position_id":"%s","tender_id":"%s","source_item_id":"%s","boq_item_type":"%s"}`,
			newID, targetPositionID, tgtTender, src[i].ID, src[i].BoqItemType,
		))
		if err := insertAudit(ctx, tx, newID, "INSERT", changedBy, nil, nil, payload); err != nil {
			return nil, fmt.Errorf("boqRepo.CopyPositionItems: audit: %w", err)
		}
	}

	// AUTHORITATIVE commercial values: computed by the server (calc) from the
	// TARGET tender's configuration and written through the internal writer — in
	// THIS transaction. Nothing commercial was copied from the source. The async
	// recalc queue is NOT the source of correctness.
	if err := MaterializeCommercialForTenderTx(ctx, tx, tgtTender); err != nil {
		return nil, fmt.Errorf("boqRepo.CopyPositionItems: %w", err)
	}

	// Grand total of the ONE affected tender (same-tender copy → the source tender
	// is the target tender), recomputed exactly once, in this tx.
	if _, err := RecalculateTenderGrandTotalTx(ctx, tx, tgtTender); err != nil {
		return nil, fmt.Errorf("boqRepo.CopyPositionItems: grand total: %w", err)
	}
	// Full sync recalculation done for this revision → success CAS (same tx).
	if err := MarkTenderCalculationSucceededTx(ctx, tx, tgtTender, revision); err != nil {
		return nil, fmt.Errorf("boqRepo.CopyPositionItems: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("boqRepo.CopyPositionItems: commit: %w", err)
	}

	res := &CopyResult{TotalCopied: len(newIDs), TenderID: tgtTender}
	for _, s := range src {
		if s.WorkNameID != nil && *s.WorkNameID != "" {
			res.WorksCount++
		}
		if s.MaterialNameID != nil && *s.MaterialNameID != "" {
			res.MaterialsCount++
		}
	}
	return res, nil
}
