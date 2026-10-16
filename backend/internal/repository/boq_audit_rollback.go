package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/su10/hubtender/backend/internal/calc"
)

// ErrAuditRollback carries an HTTP status for the handler to dispatch.
type ErrAuditRollback struct {
	HTTPStatus int
	Message    string
}

func (e *ErrAuditRollback) Error() string { return e.Message }

// BoqAuditRollbackRepo restores a boq_item's USER INPUTS from a server-side
// audit snapshot (UPDATE rollback / DELETE restore) and recomputes every derived
// value authoritatively — in ONE transaction.
type BoqAuditRollbackRepo struct {
	pool *pgxpool.Pool
}

// NewBoqAuditRollbackRepo creates a BoqAuditRollbackRepo.
func NewBoqAuditRollbackRepo(pool *pgxpool.Pool) *BoqAuditRollbackRepo {
	return &BoqAuditRollbackRepo{pool: pool}
}

// BoqAuditRollbackResult identifies what a successful rollback changed. TenderID
// lets the service invalidate exactly the affected tender's cache — known from
// the operation, never inferred.
type BoqAuditRollbackResult struct {
	ItemID    string `json:"id"`
	TenderID  string `json:"tender_id"`
	Operation string `json:"operation"` // source audit operation: UPDATE | DELETE
}

// Rollback restores the user inputs captured in the given audit record and
// recomputes total_amount, position totals, commercial values and the tender
// grand total through backend/internal/calc — all inside one transaction.
//
// Semantics (stage 0.1.2.2b): a rollback restores INPUTS from history and
// reprices them with the CURRENT tender FX rates, CURRENT markup configuration
// and CURRENT calc engine. It is NOT a byte-exact replay of the historical
// financial result (that would require calculation_run/FX/config snapshots — a
// future stage). Derived money in the snapshot is never applied.
//
// Supported operations:
//   - UPDATE  — restore allowlisted inputs onto the existing row;
//   - DELETE  — re-insert the row (original id preserved so child parent links
//     survive — the documented existing contract), inputs only;
//   - INSERT  — unsupported (UnsupportedBoqAuditRollbackError), no mutation.
//
// Fail-closed: any snapshot/target/parent/FX/calc/DB error rolls the whole
// transaction back; no partial restore, no snapshot-money fallback, no async
// compensation.
func (r *BoqAuditRollbackRepo) Rollback(ctx context.Context, auditID, changedBy string) (*BoqAuditRollbackResult, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("boqAuditRollbackRepo: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Curated audit row is written via insertAudit below; the trigger must not
	// double-log. The grand total is recomputed exactly ONCE before commit via
	// the canonical Go/calc helper (stage 0.1.2.4a: no per-row SQL triggers).
	if err := skipBoqAuditTrigger(ctx, tx); err != nil {
		return nil, fmt.Errorf("boqAuditRollbackRepo: %w", err)
	}
	if err := setAuditUser(ctx, tx, changedBy); err != nil {
		return nil, fmt.Errorf("boqAuditRollbackRepo: %w", err)
	}

	// ── Phase 1: planning/validation. The audit record is loaded SERVER-SIDE —
	// the client supplies only the audit id, never a snapshot.
	var itemID, opType string
	var oldData []byte
	err = tx.QueryRow(ctx, `
		SELECT boq_item_id::text, operation_type, old_data
		FROM public.boq_items_audit
		WHERE id = $1::uuid
	`, auditID).Scan(&itemID, &opType, &oldData)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, &ErrAuditRollback{HTTPStatus: 404, Message: "audit record not found"}
		}
		return nil, fmt.Errorf("boqAuditRollbackRepo: load audit: %w", err)
	}

	var (
		tenderID   string
		positionID string
		oldItem    *BoqItemRow // pre-rollback state (nil for a DELETE restore)
	)
	switch opType {
	case "UPDATE":
		oldItem, err = r.applyUpdateRollback(ctx, tx, auditID, itemID, oldData)
		if err != nil {
			return nil, err
		}
		tenderID, positionID = oldItem.TenderID, oldItem.ClientPositionID
	case "DELETE":
		tenderID, positionID, err = r.applyDeleteRestore(ctx, tx, auditID, itemID, oldData)
		if err != nil {
			return nil, err
		}
	default:
		return nil, &UnsupportedBoqAuditRollbackError{AuditID: auditID, Operation: opType}
	}

	if _, err := tx.Exec(ctx, `SELECT public.assert_position_pricing_owner($1,false)`, positionID); err != nil {
		return nil, err
	}
	// 0-F2 (category B): one revision bump for the whole rollback command —
	// the full recalculation below is performed for exactly this revision and
	// finishes with the success CAS before commit.
	revision, err := MarkTenderFinancialInputsChangedTx(ctx, tx, tenderID, "boq_audit_rollback")
	if err != nil {
		return nil, fmt.Errorf("boqAuditRollbackRepo: %w", err)
	}

	// ── Phase 2: authoritative recalculation, same transaction, shared kernels.
	// Order: inputs+parent applied → total_amount (calc, current FX, fail-closed)
	// → position totals → commercial (current tactic/percentages/distribution)
	// → grand total exactly once → rollback audit event → commit.
	if _, err := RecomputeBoqTotalAmountsTx(ctx, tx, tenderID, []string{itemID}); err != nil {
		return nil, fmt.Errorf("boqAuditRollbackRepo: %w", err)
	}
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
		    WHERE bi.client_position_id = $1::uuid
		  ) agg
		 WHERE cp.id = $1::uuid
	`, positionID); err != nil {
		return nil, fmt.Errorf("boqAuditRollbackRepo: recompute position totals: %w", err)
	}
	if err := MaterializeCommercialForTenderTx(ctx, tx, tenderID); err != nil {
		return nil, fmt.Errorf("boqAuditRollbackRepo: %w", err)
	}
	if _, err := RecalculateTenderGrandTotalTx(ctx, tx, tenderID); err != nil {
		return nil, fmt.Errorf("boqAuditRollbackRepo: grand total: %w", err)
	}
	// Full sync recalculation done for this revision → success CAS (same tx).
	if err := MarkTenderCalculationSucceededTx(ctx, tx, tenderID, revision); err != nil {
		return nil, fmt.Errorf("boqAuditRollbackRepo: %w", err)
	}

	// Rollback audit event: captures the state right before the rollback and the
	// FINAL authoritative state (post-calc). The original audit record stays
	// immutable. boq_items_audit has no metadata column for a rollback_of link
	// (and this stage adds no migrations), so the source audit id is not
	// persisted on the new event.
	newItem, err := scanBoqItemRow(tx.QueryRow(ctx,
		"SELECT "+boqScanCols+" FROM public.boq_items WHERE id = $1::uuid", itemID))
	if err != nil {
		return nil, fmt.Errorf("boqAuditRollbackRepo: read final row: %w", err)
	}
	newJSON, _ := boqRowJSON(newItem)
	if opType == "UPDATE" {
		oldJSON, _ := boqRowJSON(oldItem)
		err = insertAudit(ctx, tx, itemID, "UPDATE", changedBy, changedFields(oldItem, newItem), oldJSON, newJSON)
	} else {
		err = insertAudit(ctx, tx, itemID, "INSERT", changedBy, nil, nil, newJSON)
	}
	if err != nil {
		return nil, fmt.Errorf("boqAuditRollbackRepo: rollback audit event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("boqAuditRollbackRepo: commit: %w", err)
	}
	return &BoqAuditRollbackResult{ItemID: itemID, TenderID: tenderID, Operation: opType}, nil
}

// applyUpdateRollback restores allowlisted inputs onto the EXISTING row and
// returns its pre-rollback state. Identity/scope fields are verified, never
// applied: the item stays in its tender and client position (a rollback never
// moves an item), timestamps get fresh values, derived money is untouched here
// (recomputed by the caller).
func (r *BoqAuditRollbackRepo) applyUpdateRollback(
	ctx context.Context, tx pgx.Tx, auditID, itemID string, oldData []byte,
) (*BoqItemRow, error) {
	snap, err := parseBoqAuditSnapshot(auditID, itemID, oldData)
	if err != nil {
		return nil, fmt.Errorf("boqAuditRollbackRepo: %w", err)
	}

	cur, err := scanBoqItemRow(tx.QueryRow(ctx,
		"SELECT "+boqScanCols+" FROM public.boq_items WHERE id = $1::uuid FOR UPDATE", itemID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, &ErrAuditRollback{HTTPStatus: 404, Message: "BOQ item not found"}
		}
		return nil, fmt.Errorf("boqAuditRollbackRepo: lock item: %w", err)
	}

	// audit ↔ item ↔ tender ownership: a snapshot of another item or another
	// tender can never be applied here.
	if snap.ID != nil && *snap.ID != itemID {
		return nil, &BoqAuditTargetMismatchError{
			AuditID: auditID, ExpectedItemID: *snap.ID, ActualItemID: itemID,
		}
	}
	if snap.TenderID != nil && *snap.TenderID != cur.TenderID {
		return nil, &BoqAuditTargetMismatchError{
			AuditID: auditID, ExpectedItemID: itemID, ActualItemID: itemID,
			ExpectedTenderID: *snap.TenderID, ActualTenderID: cur.TenderID,
		}
	}

	// Parent integrity against the item's CURRENT scope — same invariant as
	// Template Insert / Copy / Transfer; an invalid historical parent BLOCKS the
	// rollback, it is never downgraded to a standalone material.
	if snap.Present("parent_work_item_id") {
		if err := validateRestoredParentTx(ctx, tx, cur.TenderID, cur.ClientPositionID, itemID, snap.ParentWorkItemID); err != nil {
			return nil, fmt.Errorf("boqAuditRollbackRepo: %w", err)
		}
	}

	// EXPLICIT column list — one assignment per allowlisted input actually
	// captured in the snapshot. Never a dynamic SET built from JSON keys.
	args := []any{}
	setClauses := ""
	set := func(col string, val any) {
		if setClauses != "" {
			setClauses += ", "
		}
		setClauses += fmt.Sprintf("%s = $%d", col, len(args)+1)
		args = append(args, val)
	}
	set("boq_item_type", snap.BoqItemType) // required, always present
	if snap.Present("material_type") {
		set("material_type", snap.MaterialType)
	}
	if snap.Present("description") {
		set("description", snap.Description)
	}
	if snap.Present("unit_code") {
		set("unit_code", snap.UnitCode)
	}
	if snap.Present("quantity") {
		set("quantity", snap.Quantity)
	}
	if snap.Present("base_quantity") {
		set("base_quantity", snap.BaseQuantity)
	}
	if snap.Present("conversion_coefficient") {
		set("conversion_coefficient", snap.ConversionCoefficient)
	}
	if snap.Present("unit_rate") {
		set("unit_rate", snap.UnitRate)
	}
	if snap.Present("currency_type") {
		set("currency_type", snap.CurrencyType)
	}
	if snap.Present("delivery_price_type") {
		set("delivery_price_type", snap.DeliveryPriceType)
	}
	if snap.Present("delivery_amount") {
		set("delivery_amount", snap.DeliveryAmount)
	}
	if snap.Present("consumption_coefficient") {
		set("consumption_coefficient", snap.ConsumptionCoefficient)
	}
	if snap.Present("detail_cost_category_id") {
		set("detail_cost_category_id", snap.DetailCostCategoryID)
	}
	if snap.Present("material_name_id") {
		set("material_name_id", snap.MaterialNameID)
	}
	if snap.Present("work_name_id") {
		set("work_name_id", snap.WorkNameID)
	}
	if snap.Present("parent_work_item_id") {
		set("parent_work_item_id", snap.ParentWorkItemID)
	}
	if snap.Present("sort_number") && snap.SortNumber != nil {
		set("sort_number", *snap.SortNumber)
	}
	if snap.Present("quote_link") {
		set("quote_link", snap.QuoteLink)
	}
	setClauses += ", updated_at = NOW()"
	args = append(args, itemID)

	q := fmt.Sprintf("UPDATE public.boq_items SET %s WHERE id = $%d", setClauses, len(args))
	if _, err := tx.Exec(ctx, q, args...); err != nil {
		return nil, r.mapWriteError(auditID, itemID, err, "restore inputs")
	}
	return cur, nil
}

// applyDeleteRestore re-inserts a DELETE'd row from its snapshot: allowlisted
// inputs only, identity per the documented contract (original id — so surviving
// children keep their parent links), scope verified against the CURRENT DB.
// Derived money columns are NOT in the INSERT list at all; they stay NULL until
// the caller's authoritative recompute inside this same transaction.
func (r *BoqAuditRollbackRepo) applyDeleteRestore(
	ctx context.Context, tx pgx.Tx, auditID, itemID string, oldData []byte,
) (tenderID, positionID string, err error) {
	snap, err := parseBoqAuditSnapshot(auditID, itemID, oldData)
	if err != nil {
		return "", "", fmt.Errorf("boqAuditRollbackRepo: %w", err)
	}

	// Identity contract: the snapshot must carry a consistent id + scope.
	if snap.ID == nil {
		return "", "", fmt.Errorf("boqAuditRollbackRepo: %w",
			&InvalidBoqAuditSnapshotError{AuditID: auditID, ItemID: itemID, Field: "id", Reason: RequiredFieldMissing})
	}
	if *snap.ID != itemID {
		return "", "", &BoqAuditTargetMismatchError{
			AuditID: auditID, ExpectedItemID: *snap.ID, ActualItemID: itemID,
		}
	}
	if snap.TenderID == nil {
		return "", "", fmt.Errorf("boqAuditRollbackRepo: %w",
			&InvalidBoqAuditSnapshotError{AuditID: auditID, ItemID: itemID, Field: "tender_id", Reason: RequiredFieldMissing})
	}
	if snap.ClientPositionID == nil {
		return "", "", fmt.Errorf("boqAuditRollbackRepo: %w",
			&InvalidBoqAuditSnapshotError{AuditID: auditID, ItemID: itemID, Field: "client_position_id", Reason: RequiredFieldMissing})
	}

	// Scope: the position must still exist and belong to the snapshot's tender.
	var posTender string
	err = tx.QueryRow(ctx,
		`SELECT tender_id::text FROM public.client_positions WHERE id = $1::uuid`,
		*snap.ClientPositionID).Scan(&posTender)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", &ErrAuditRollback{HTTPStatus: 409, Message: "Не удалось восстановить: позиция или тендер удалены"}
		}
		return "", "", fmt.Errorf("boqAuditRollbackRepo: position lookup: %w", err)
	}
	if posTender != *snap.TenderID {
		return "", "", &BoqAuditTargetMismatchError{
			AuditID: auditID, ExpectedItemID: itemID, ActualItemID: itemID,
			ExpectedTenderID: *snap.TenderID, ActualTenderID: posTender,
		}
	}

	// The original id must be free.
	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM public.boq_items WHERE id = $1::uuid)`, itemID).Scan(&exists); err != nil {
		return "", "", fmt.Errorf("boqAuditRollbackRepo: id conflict check: %w", err)
	}
	if exists {
		return "", "", &ErrAuditRollback{HTTPStatus: 409, Message: "Элемент с таким id уже существует"}
	}

	// Parent integrity against the restore scope. Blocking — no NULL fallback.
	if err := validateRestoredParentTx(ctx, tx, *snap.TenderID, *snap.ClientPositionID, itemID, snap.ParentWorkItemID); err != nil {
		return "", "", fmt.Errorf("boqAuditRollbackRepo: %w", err)
	}

	sortNum := 0
	if snap.SortNumber != nil {
		sortNum = *snap.SortNumber
	}
	// Derived money columns are DELIBERATELY absent from this column list.
	_, err = tx.Exec(ctx, `
		INSERT INTO public.boq_items (
		    id, tender_id, client_position_id, sort_number,
		    boq_item_type, material_type, description, unit_code,
		    quantity, base_quantity, conversion_coefficient, unit_rate,
		    currency_type, delivery_price_type, delivery_amount,
		    consumption_coefficient,
		    detail_cost_category_id, material_name_id, work_name_id,
		    parent_work_item_id, quote_link
		) VALUES (
		    $1::uuid, $2::uuid, $3::uuid, $4,
		    $5::boq_item_type, $6::material_type, $7, $8,
		    $9, $10, $11, $12,
		    $13::currency_type, $14::delivery_price_type, $15,
		    $16,
		    $17::uuid, $18::uuid, $19::uuid,
		    $20::uuid, $21
		)
	`,
		itemID, *snap.TenderID, *snap.ClientPositionID, sortNum,
		snap.BoqItemType, snap.MaterialType, snap.Description, snap.UnitCode,
		snap.Quantity, snap.BaseQuantity, snap.ConversionCoefficient, snap.UnitRate,
		snap.CurrencyType, snap.DeliveryPriceType, snap.DeliveryAmount,
		snap.ConsumptionCoefficient,
		snap.DetailCostCategoryID, snap.MaterialNameID, snap.WorkNameID,
		snap.ParentWorkItemID, snap.QuoteLink,
	)
	if err != nil {
		return "", "", r.mapWriteError(auditID, itemID, err, "re-insert")
	}
	return *snap.TenderID, *snap.ClientPositionID, nil
}

// mapWriteError converts constraint violations on restore writes into typed
// domain errors instead of generic 500s.
func (r *BoqAuditRollbackRepo) mapWriteError(auditID, itemID string, err error, op string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation
			return &ErrAuditRollback{HTTPStatus: 409, Message: "Элемент с таким id уже существует"}
		case "23503": // foreign_key_violation — a restored reference no longer resolves
			return fmt.Errorf("boqAuditRollbackRepo: %s: %w", op, &InvalidBoqAuditSnapshotError{
				AuditID: auditID, ItemID: itemID, Field: pgErr.ConstraintName, Reason: InvalidRelationReference,
			})
		case "23514": // check_violation — a snapshot value violates a domain CHECK
			return fmt.Errorf("boqAuditRollbackRepo: %s: %w", op, &InvalidBoqAuditSnapshotError{
				AuditID: auditID, ItemID: itemID, Field: pgErr.ConstraintName, Reason: InvalidFieldType,
			})
		}
	}
	return fmt.Errorf("boqAuditRollbackRepo: %s: %w", op, err)
}

// validateRestoredParentTx enforces the shared parent invariant for a restored
// parent_work_item_id: the parent must exist, live in the SAME tender and client
// position, be a WORK item and not be the item itself. An invalid historical
// parent is a blocking InvalidBoqParentError — never parent=NULL/standalone
// (that would change the money and hide snapshot corruption).
func validateRestoredParentTx(
	ctx context.Context, tx pgx.Tx, tenderID, positionID, itemID string, parentID *string,
) error {
	if parentID == nil {
		return nil
	}
	pid := *parentID
	if pid == itemID {
		return &InvalidBoqParentError{ItemID: itemID, ParentItemID: pid, Reason: BoqSelfParentReference}
	}
	var pType, pTender, pPos string
	err := tx.QueryRow(ctx, `
		SELECT boq_item_type::text, tender_id::text, client_position_id::text
		FROM public.boq_items WHERE id = $1::uuid
	`, pid).Scan(&pType, &pTender, &pPos)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &InvalidBoqParentError{ItemID: itemID, ParentItemID: pid, Reason: BoqParentNotFound}
		}
		return fmt.Errorf("validateRestoredParentTx: %w", err)
	}
	if pTender != tenderID || pPos != positionID {
		return &InvalidBoqParentError{ItemID: itemID, ParentItemID: pid, Reason: BoqParentNotInScope, ParentItemType: pType}
	}
	if !calc.IsWorkBoqType(pType) {
		return &InvalidBoqParentError{ItemID: itemID, ParentItemID: pid, Reason: BoqParentNotWorkItem, ParentItemType: pType}
	}
	return nil
}

// ─── audit list (useAuditHistory) ───────────────────────────────────────────

// AuditUserEmbed mirrors the user:changed_by(id,full_name,email) embed.
type AuditUserEmbed struct {
	ID       string  `json:"id"`
	FullName *string `json:"full_name"`
	Email    *string `json:"email"`
}

// BoqAuditRow is one boq_items_audit row + user embed.
type BoqAuditRow struct {
	ID            string          `json:"id"`
	BoqItemID     string          `json:"boq_item_id"`
	OperationType string          `json:"operation_type"`
	ChangedAt     string          `json:"changed_at"`
	ChangedBy     *string         `json:"changed_by"`
	ChangedFields []string        `json:"changed_fields"`
	OldData       json.RawMessage `json:"old_data"`
	NewData       json.RawMessage `json:"new_data"`
	User          *AuditUserEmbed `json:"user"`
}

// BoqAuditListFilter holds the optional query params for ListByPosition.
type BoqAuditListFilter struct {
	PositionID    string
	DateFrom      *string
	DateTo        *string
	UserID        *string
	OperationType *string
}

// ListByPosition returns boq_items_audit rows where the audited row's
// client_position_id (in new_data or old_data JSONB) matches positionID.
func (r *BoqAuditRollbackRepo) ListByPosition(ctx context.Context, f BoqAuditListFilter) ([]BoqAuditRow, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT bia.id::text, bia.boq_item_id::text, bia.operation_type,
		       to_char(bia.changed_at, 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
		       bia.changed_by::text, bia.changed_fields,
		       bia.old_data, bia.new_data,
		       u.id::text, u.full_name, u.email
		FROM public.boq_items_audit bia
		LEFT JOIN public.users u ON u.id = bia.changed_by
		WHERE (
		    (bia.new_data->>'client_position_id') = $1
		    OR (bia.old_data->>'client_position_id') = $1
		)
		  AND ($2::timestamptz IS NULL OR bia.changed_at >= $2::timestamptz)
		  AND ($3::timestamptz IS NULL OR bia.changed_at <= $3::timestamptz)
		  AND ($4::uuid        IS NULL OR bia.changed_by      = $4::uuid)
		  AND ($5::text        IS NULL OR bia.operation_type  = $5)
		  -- Hide commercial-cost recalculation noise: an UPDATE whose changed
		  -- fields are entirely commercial columns (or timestamps, or empty) is a
		  -- markup-driven recompute, not a user edit. INSERT/DELETE always kept.
		  -- Historical rows already in the table are filtered here; new ones are
		  -- no longer written (see log_boq_items_changes()).
		  AND (
		    bia.operation_type <> 'UPDATE'
		    OR NOT (COALESCE(bia.changed_fields, '{}') <@ ARRAY[
		        'commercial_markup',
		        'total_commercial_material_cost',
		        'total_commercial_work_cost',
		        'updated_at', 'created_at']::text[])
		  )
		ORDER BY bia.changed_at DESC
	`, f.PositionID, f.DateFrom, f.DateTo, f.UserID, f.OperationType)
	if err != nil {
		return nil, fmt.Errorf("boqAuditRollbackRepo.ListByPosition: %w", err)
	}
	defer rows.Close()
	out := make([]BoqAuditRow, 0)
	for rows.Next() {
		var a BoqAuditRow
		var uID, uName, uEmail *string
		var oldData, newData []byte
		if err := rows.Scan(&a.ID, &a.BoqItemID, &a.OperationType,
			&a.ChangedAt, &a.ChangedBy, &a.ChangedFields,
			&oldData, &newData,
			&uID, &uName, &uEmail); err != nil {
			return nil, fmt.Errorf("boqAuditRollbackRepo.ListByPosition scan: %w", err)
		}
		if len(oldData) > 0 {
			a.OldData = json.RawMessage(oldData)
		}
		if len(newData) > 0 {
			a.NewData = json.RawMessage(newData)
		}
		if uID != nil {
			a.User = &AuditUserEmbed{ID: *uID, FullName: uName, Email: uEmail}
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
