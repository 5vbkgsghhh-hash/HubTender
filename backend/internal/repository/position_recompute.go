package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/su10/hubtender/backend/internal/calc"
)

// ─── recompute position totals ──────────────────────────────────────────────

// RecomputePositionTotals re-aggregates boq_items by type and writes the
// totals onto client_positions. Single UPDATE-FROM, idempotent.
func (r *PositionRepo) RecomputePositionTotals(ctx context.Context, positionID string) error {
	if _, err := r.pool.Exec(ctx, `
		UPDATE public.client_positions cp
		SET total_material = COALESCE(s.tm, 0),
		    total_works    = COALESCE(s.tw, 0),
		    updated_at     = NOW()
		FROM (
			SELECT
				SUM(total_amount) FILTER (WHERE boq_item_type::text IN ('мат','суб-мат','мат-комп.')) AS tm,
				SUM(total_amount) FILTER (WHERE boq_item_type::text IN ('раб','суб-раб','раб-комп.')) AS tw
			FROM public.boq_items
			WHERE client_position_id = $1
		) s
		WHERE cp.id = $1
	`, positionID); err != nil {
		return fmt.Errorf("positionRepo.RecomputePositionTotals: %w", err)
	}
	return nil
}

// recomputePositionTotalsByIDsTx re-aggregates total_material / total_works of
// the given positions inside the caller's transaction, zeroing the ones left
// without items (the tender-wide RecomputePositionTotalsForTenderTx only touches
// positions that have items). IDs may repeat. Unchanged positions are not
// rewritten, so a patch that does not move money does not bump updated_at or
// emit a realtime event.
func recomputePositionTotalsByIDsTx(ctx context.Context, tx pgx.Tx, positionIDs []string) error {
	if len(positionIDs) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE public.client_positions cp
		SET total_material = COALESCE(s.tm, 0),
		    total_works    = COALESCE(s.tw, 0),
		    updated_at     = NOW()
		FROM (
			SELECT p.id,
				SUM(b.total_amount) FILTER (WHERE b.boq_item_type::text IN ('мат','суб-мат','мат-комп.')) AS tm,
				SUM(b.total_amount) FILTER (WHERE b.boq_item_type::text IN ('раб','суб-раб','раб-комп.')) AS tw
			FROM (SELECT DISTINCT id FROM unnest($1::uuid[]) AS u(id)) p
			LEFT JOIN public.boq_items b ON b.client_position_id = p.id
			GROUP BY p.id
		) s
		WHERE cp.id = s.id
		  AND (cp.total_material IS DISTINCT FROM COALESCE(s.tm, 0)
		       OR cp.total_works IS DISTINCT FROM COALESCE(s.tw, 0))
	`, positionIDs); err != nil {
		return fmt.Errorf("recomputePositionTotalsByIDsTx: %w", err)
	}
	return nil
}

// ─── recompute linked materials ─────────────────────────────────────────────

// ErrWorkNotFound is returned when the parent work_item is missing.
var ErrWorkNotFound = errors.New("работа не найдена")

// RecomputeLinkedMaterialsForWork updates quantity + total_amount on every
// boq_item whose parent_work_item_id = workID, in one transaction with
// per-row audit rows. Quantity is workQuantity * (conv_coeff||1) *
// (cons_coeff||1); total_amount is recomputed via the shared calc helper
// (same formula the frontend used to apply client-side).
func (r *BoqRepo) RecomputeLinkedMaterialsForWork(
	ctx context.Context, workID, changedBy string,
) (int, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("boqRepo.RecomputeLinkedMaterialsForWork: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := skipBoqAuditTrigger(ctx, tx); err != nil {
		return 0, fmt.Errorf("boqRepo.RecomputeLinkedMaterialsForWork: %w", err)
	}

	work, err := scanBoqItemRow(tx.QueryRow(ctx, "SELECT "+boqScanCols+" FROM public.boq_items WHERE id=$1 FOR UPDATE", workID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrWorkNotFound
		}
		return 0, err
	}
	if !calc.IsWorkBoqType(work.BoqItemType) {
		return 0, ErrDirectParentInvalid
	}
	workTenderID := work.TenderID

	// 0-F2 (category A): one user command → one revision bump for the tender;
	// the children's quantity/total change here, commercial recalc is async.
	if _, err := MarkTenderFinancialInputsChangedTx(ctx, tx, workTenderID, "recompute_linked_materials"); err != nil {
		return 0, fmt.Errorf("boqRepo.RecomputeLinkedMaterialsForWork: %w", err)
	}

	rates, err := loadTenderRates(ctx, tx, workTenderID)
	if err != nil {
		return 0, fmt.Errorf("boqRepo.RecomputeLinkedMaterialsForWork: %w", err)
	}

	updated, err := recomputeLinkedMaterialsTx(ctx, tx, work, changedBy, rates, false)
	if err != nil {
		return 0, err
	}
	if err := recomputePositionTotalsByIDsTx(ctx, tx, []string{work.ClientPositionID}); err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("boqRepo.RecomputeLinkedMaterialsForWork: commit: %w", err)
	}
	return len(updated), nil
}

// recomputeLinkedMaterialsTx shares the VOR recipe with direct MCP writes.
// The caller locks the parent and owns revision, audit trigger and totals.
func recomputeLinkedMaterialsTx(ctx context.Context, tx pgx.Tx, work *BoqItemRow, changedBy string, rates calc.CurrencyRates, noWait bool) ([]*BoqItemRow, error) {
	query := `SELECT ` + boqScanCols + `
		 FROM public.boq_items
		 WHERE parent_work_item_id = $1
		 ORDER BY id FOR UPDATE`
	if noWait {
		query += " NOWAIT"
	}
	rows, err := tx.Query(ctx, query, work.ID)
	if err != nil {
		return nil, fmt.Errorf("boqRepo.RecomputeLinkedMaterialsForWork: children: %w", err)
	}
	children := make([]*BoqItemRow, 0)
	for rows.Next() {
		c, scanErr := scanBoqItemRow(rows)
		if scanErr != nil {
			rows.Close()
			return nil, fmt.Errorf("boqRepo.RecomputeLinkedMaterialsForWork: child scan: %w", scanErr)
		}
		children = append(children, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("boqRepo.RecomputeLinkedMaterialsForWork: children rows: %w", err)
	}

	const updQ = `
		UPDATE public.boq_items
		SET quantity = $1, total_amount = $2, updated_at = NOW()
		WHERE id = $3
		RETURNING ` + boqScanCols

	updated := make([]*BoqItemRow, 0, len(children))
	positionIDs := make([]string, 0, len(children))
	for _, c := range children {
		if c.TenderID != work.TenderID || !calc.IsMaterialBoqType(c.BoqItemType) {
			return nil, ErrDirectParentInvalid
		}
		if work.Quantity == nil {
			return nil, fmt.Errorf("parent work quantity is missing")
		}
		newQty, err := calc.CalculateLinkedMaterialQuantity(*work.Quantity, c.ConversionCoefficient, c.ConsumptionCoefficient)
		if err != nil {
			return nil, err
		}

		// Recompute total via the shared calc using the new quantity.
		amtIn := boqAmountInputFromRow(c)
		amtIn.Quantity = &newQty
		newTotal, err := calc.CalculateBoqItemTotalAmount(amtIn, rates)
		if err != nil {
			// Blocking: a missing FX rate must fail the whole recompute. The
			// deferred tx.Rollback preserves existing correct values — no
			// partial/zero write. Error propagates to the caller's logger.
			return nil, fmt.Errorf("boqRepo.RecomputeLinkedMaterialsForWork: %w", err)
		}

		oldJSON, _ := boqRowJSON(c)
		newItem, err := scanBoqItemRow(tx.QueryRow(ctx, updQ, newQty, newTotal, c.ID))
		if err != nil {
			return nil, fmt.Errorf("boqRepo.RecomputeLinkedMaterialsForWork: update: %w", err)
		}
		newJSON, _ := boqRowJSON(newItem)
		if err := insertAudit(ctx, tx, c.ID, "UPDATE", changedBy,
			changedFields(c, newItem), oldJSON, newJSON); err != nil {
			return nil, fmt.Errorf("boqRepo.RecomputeLinkedMaterialsForWork: audit: %w", err)
		}
		updated = append(updated, newItem)
		// A linked material may sit in another position than its work.
		positionIDs = append(positionIDs, c.ClientPositionID)
	}

	if err := recomputePositionTotalsByIDsTx(ctx, tx, positionIDs); err != nil {
		return nil, fmt.Errorf("boqRepo.RecomputeLinkedMaterialsForWork: %w", err)
	}

	return updated, nil
}

// ─── update specific position fields ────────────────────────────────────────

// UpdatePositionFieldsInput targets only the fields the legacy ItemActions
// hook patches (manual_volume / manual_note / work_name / unit_code).
type UpdatePositionFieldsInput struct {
	ManualVolume *float64
	ManualNote   *string
	WorkName     *string
	UnitCode     *string
}

// UpdatePositionFields applies non-nil patch fields to a client_position.
func (r *PositionRepo) UpdatePositionFields(ctx context.Context, id string, in UpdatePositionFieldsInput) error {
	args := []any{}
	setClauses := ""
	add := func(col string, val any) {
		if setClauses != "" {
			setClauses += ", "
		}
		setClauses += fmt.Sprintf("%s = $%d", col, len(args)+1)
		args = append(args, val)
	}
	if in.ManualVolume != nil {
		add("manual_volume", *in.ManualVolume)
	}
	if in.ManualNote != nil {
		add("manual_note", *in.ManualNote)
	}
	if in.WorkName != nil {
		add("work_name", *in.WorkName)
	}
	if in.UnitCode != nil {
		add("unit_code", *in.UnitCode)
	}
	if setClauses == "" {
		return nil
	}
	setClauses += ", updated_at = NOW()"
	args = append(args, id)
	q := fmt.Sprintf(
		`UPDATE public.client_positions SET %s WHERE id = $%d`, setClauses, len(args))
	if _, err := r.pool.Exec(ctx, q, args...); err != nil {
		return fmt.Errorf("positionRepo.UpdatePositionFields: %w", err)
	}
	return nil
}
