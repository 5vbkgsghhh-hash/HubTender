package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"github.com/su10/hubtender/backend/internal/calc"
)

// RedistributionRecord is one persisted/loaded cost_redistribution_results row
// (transport shape for GET/POST responses). It is NOT a request DTO: since
// stage 0.1.2.3a the client cannot submit financial redistribution values —
// every row is server-generated.
type RedistributionRecord struct {
	BoqItemID        string  `json:"boq_item_id"`
	OriginalWorkCost float64 `json:"original_work_cost"`
	DeductedAmount   float64 `json:"deducted_amount"`
	AddedAmount      float64 `json:"added_amount"`
	FinalWorkCost    float64 `json:"final_work_cost"`
}

// Snapshot status values returned by LoadResults.
const (
	// RedistributionStatusCalculated — server-authoritative snapshot
	// (schema_version >= 2, calculation_source = "server"); results usable.
	RedistributionStatusCalculated = "calculated"
	// RedistributionStatusRequiresRecalculation — legacy client-calculated
	// snapshot; results must NOT be used as authoritative (Commerce/FI/exports).
	RedistributionStatusRequiresRecalculation = "requires_recalculation"
	// RedistributionStatusNotConfigured — no snapshot stored yet.
	RedistributionStatusNotConfigured = "not_configured"
)

// Stable reason codes for status = requires_recalculation (stage 0.1.2.3b.1).
// The frontend branches on these codes, never on message text.
const (
	// RedistributionReasonLegacySnapshot — client-calculated pre-0.1.2.3a snapshot.
	RedistributionReasonLegacySnapshot = "LEGACY_SNAPSHOT"
	// RedistributionReasonSetMismatch — the persisted snapshot does not match
	// the current BOQ set (missing/extra rows).
	RedistributionReasonSetMismatch = "SNAPSHOT_SET_MISMATCH"
	// RedistributionReasonInputChanged — positions/rules inputs changed so the
	// prepared projection can no longer be built from the snapshot.
	RedistributionReasonInputChanged = "PREPARED_INPUT_CHANGED"
	// RedistributionReasonInsuranceInvalid — the insurance configuration or its
	// allocation is invalid for the current state.
	RedistributionReasonInsuranceInvalid = "INSURANCE_ALLOCATION_INVALID"
	// RedistributionReasonPreparedFailed — any other prepared-calculation
	// failure (internal detail is logged, never exposed).
	RedistributionReasonPreparedFailed = "PREPARED_CALCULATION_FAILED"
	// RedistributionReasonInputRevisionChanged — stage 0-F2: the tender's
	// financial_input_revision moved past the revision the snapshot was built
	// for (catches stale snapshots even when the BOQ id set is unchanged).
	RedistributionReasonInputRevisionChanged = "INPUT_REVISION_CHANGED"
)

// ErrRedistributionTenderNotFound — the save targets a tender that does not exist.
var ErrRedistributionTenderNotFound = errors.New("тендер не найден")

// RedistributionRepo owns cost_redistribution_results.
type RedistributionRepo struct {
	pool *pgxpool.Pool
}

// NewRedistributionRepo creates a RedistributionRepo.
func NewRedistributionRepo(pool *pgxpool.Pool) *RedistributionRepo {
	return &RedistributionRepo{pool: pool}
}

// RedistributionSaveOutput is the server-calculated snapshot the save returns.
// Server-generated redistribution calculation result.
// Must never be populated from an HTTP request.
type RedistributionSaveOutput struct {
	SavedCount     int
	Results        []RedistributionRecord
	TotalDeducted  float64
	TotalAdded     float64
	IsBalanced     bool
	CanonicalRules json.RawMessage
	// PositionDeltas — server-validated cumulative position deltas (diagnostic).
	PositionDeltas map[string]float64
	// Prepared — the full server-generated prepared projection (stage 0.1.2.3b):
	// position adjustments + insurance + rounding + final rows + summary, built
	// by the SAME calc boundary the GET uses.
	Prepared *calc.PreparedRedistribution
	TenderID string
}

// SaveAuthoritative recalculates and persists the redistribution snapshot for
// (tenderID, tacticID) from the tender's CURRENT authoritative state — in ONE
// transaction:
//
//	verify tender + active tactic → typed rules validation (DB-confirmed
//	category/position references) → materialize current commercial values
//	(calc; fail-closed MissingFXRateError) → load the FULL tender BOQ →
//	calc.CalculateRedistribution → invariants (balance, exact set, row
//	identity) → position-adjustment validation on the server-generated base →
//	canonical rules JSON (schema_version=2, calculation_source="server") →
//	atomic batched replace (DELETE + one UNNEST INSERT, exact-set count) →
//	grand total exactly once → commit.
//
// The client contributes ONLY rules. No client-calculated record can reach
// this function. Fail-closed: on any error the whole tx rolls back — the old
// snapshot, commercial fields and grand total stay untouched.
func (r *RedistributionRepo) SaveAuthoritative(
	ctx context.Context,
	tenderID, tacticID string,
	rules calc.RedistributionRulesInput,
	createdBy string,
) (*RedistributionSaveOutput, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("redistributionRepo.SaveAuthoritative: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// ── planning: tender + active-tactic policy ──
	var activeTactic *string
	err = tx.QueryRow(ctx,
		`SELECT markup_tactic_id::text FROM public.tenders WHERE id = $1::uuid`, tenderID,
	).Scan(&activeTactic)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRedistributionTenderNotFound
		}
		return nil, fmt.Errorf("redistributionRepo.SaveAuthoritative: tender: %w", err)
	}
	active := ""
	if activeTactic != nil {
		active = *activeTactic
	}
	if active != tacticID {
		// Redistribution is built on total_commercial_work_cost, which the server
		// materializes for the tender's ACTIVE tactic — computing over another
		// tactic's values would silently mix configurations.
		return nil, &calc.RedistributionTacticMismatchError{
			TenderID: tenderID, RequestedTacticID: tacticID, ActiveTacticID: active,
		}
	}

	// 0-F2 (category B): one revision bump per save command. The snapshot below
	// is stamped with this revision (INPUT_REVISION_CHANGED detection) and the
	// full recalculation finishes with the success CAS before commit.
	revision, err := MarkTenderFinancialInputsChangedTx(ctx, tx, tenderID, "redistribution_save")
	if err != nil {
		return nil, fmt.Errorf("redistributionRepo.SaveAuthoritative: %w", err)
	}

	// ── authoritative commercial base, in THIS transaction ──
	// The grand total is recomputed exactly once below (stage 0.1.2.4a:
	// no per-row SQL triggers).
	if err := MaterializeCommercialForTenderTx(ctx, tx, tenderID); err != nil {
		// e.g. calc.MissingFXRateError — the whole save rolls back, no partially
		// updated commercial fields survive.
		return nil, fmt.Errorf("redistributionRepo.SaveAuthoritative: %w", err)
	}

	// ── the ONE snapshot engine: validation → calculation → invariants →
	// prepared projection → atomic persist, stamped with this revision ──
	built, err := rebuildRedistributionSnapshotTx(ctx, tx, tenderID, tacticID, rules, createdBy, revision)
	if err != nil {
		return nil, fmt.Errorf("redistributionRepo.SaveAuthoritative: %w", err)
	}

	// ── grand total exactly once (commercial values may have changed above) ──
	if _, err := RecalculateTenderGrandTotalTx(ctx, tx, tenderID); err != nil {
		return nil, fmt.Errorf("redistributionRepo.SaveAuthoritative: grand total: %w", err)
	}
	// Full sync recalculation done for this revision → success CAS (same tx).
	if err := MarkTenderCalculationSucceededTx(ctx, tx, tenderID, revision); err != nil {
		return nil, fmt.Errorf("redistributionRepo.SaveAuthoritative: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("redistributionRepo.SaveAuthoritative: commit: %w", err)
	}

	resp := &RedistributionSaveOutput{
		SavedCount:     len(built.Results),
		Results:        make([]RedistributionRecord, len(built.Results)),
		TotalDeducted:  built.TotalDeducted,
		TotalAdded:     built.TotalAdded,
		IsBalanced:     built.IsBalanced,
		CanonicalRules: built.CanonicalJSON,
		PositionDeltas: built.PositionDeltas,
		Prepared:       built.Prepared,
		TenderID:       tenderID,
	}
	for i, res := range built.Results {
		resp.Results[i] = RedistributionRecord{
			BoqItemID:        res.BoqItemID,
			OriginalWorkCost: res.OriginalWorkCost,
			DeductedAmount:   res.DeductedAmount,
			AddedAmount:      res.AddedAmount,
			FinalWorkCost:    res.FinalWorkCost,
		}
	}
	return resp, nil
}

// ClearAuthoritative deletes the redistribution snapshot for (tenderID,
// tacticID) — the «очистка снимка» operation that an empty-rules save refuses
// (RULES_EMPTY). Without it a user who removed every rule saw them come back
// on reload: the page had nothing it could send.
//
// One transaction, mirroring SaveAuthoritative: the tender row is locked, and
// only when a snapshot actually exists does the command bump the financial
// input revision (the redistributed figures the approval was given for are
// gone), re-materialize, delete, recompute the grand total and finish with the
// success CAS. An absent snapshot is a no-op: an idempotent repeat must not
// revoke a financial approval. The tactic is NOT required to be the active
// one — dropping another tactic's snapshot is always safe.
//
// Returns the number of deleted rows.
func (r *RedistributionRepo) ClearAuthoritative(
	ctx context.Context,
	tenderID, tacticID string,
) (int, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("redistributionRepo.ClearAuthoritative: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// FOR UPDATE serializes with a concurrent save (its revision bump updates
	// the same row), so the existence check below sees the committed state.
	var exists bool
	err = tx.QueryRow(ctx,
		`SELECT true FROM public.tenders WHERE id = $1::uuid FOR UPDATE`, tenderID,
	).Scan(&exists)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrRedistributionTenderNotFound
		}
		return 0, fmt.Errorf("redistributionRepo.ClearAuthoritative: tender: %w", err)
	}

	var hasSnapshot bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM public.cost_redistribution_results
			WHERE tender_id = $1::uuid AND markup_tactic_id = $2::uuid
		)
	`, tenderID, tacticID).Scan(&hasSnapshot); err != nil {
		return 0, fmt.Errorf("redistributionRepo.ClearAuthoritative: exists: %w", err)
	}
	if !hasSnapshot {
		return 0, nil
	}

	revision, err := MarkTenderFinancialInputsChangedTx(ctx, tx, tenderID, "redistribution_clear")
	if err != nil {
		return 0, fmt.Errorf("redistributionRepo.ClearAuthoritative: %w", err)
	}
	if err := MaterializeCommercialForTenderTx(ctx, tx, tenderID); err != nil {
		return 0, fmt.Errorf("redistributionRepo.ClearAuthoritative: %w", err)
	}

	tag, err := tx.Exec(ctx, `
		DELETE FROM public.cost_redistribution_results
		WHERE tender_id = $1::uuid AND markup_tactic_id = $2::uuid
	`, tenderID, tacticID)
	if err != nil {
		return 0, fmt.Errorf("redistributionRepo.ClearAuthoritative: delete: %w", err)
	}

	if _, err := RecalculateTenderGrandTotalTx(ctx, tx, tenderID); err != nil {
		return 0, fmt.Errorf("redistributionRepo.ClearAuthoritative: grand total: %w", err)
	}
	if err := MarkTenderCalculationSucceededTx(ctx, tx, tenderID, revision); err != nil {
		return 0, fmt.Errorf("redistributionRepo.ClearAuthoritative: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("redistributionRepo.ClearAuthoritative: commit: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// loadNameMap reads an id→name map.
func loadNameMap(ctx context.Context, tx pgx.Tx, q string) (map[string]string, error) {
	rows, err := tx.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

// loadRedistributionBoq loads the tender's full BOQ in deterministic order
// (by id ASC) with the just-materialized commercial costs.
func loadRedistributionBoq(ctx context.Context, tx pgx.Tx, tenderID string) ([]calc.BoqItemWithCosts, error) {
	rows, err := tx.Query(ctx, `
		SELECT id::text, client_position_id::text, detail_cost_category_id::text,
		       boq_item_type::text,
		       COALESCE(total_commercial_work_cost, 0),
		       COALESCE(total_commercial_material_cost, 0)
		FROM public.boq_items
		WHERE tender_id = $1::uuid
		ORDER BY id ASC
	`, tenderID)
	if err != nil {
		return nil, fmt.Errorf("redistributionRepo: load boq: %w", err)
	}
	defer rows.Close()
	items := make([]calc.BoqItemWithCosts, 0)
	for rows.Next() {
		var it calc.BoqItemWithCosts
		if err := rows.Scan(&it.ID, &it.ClientPositionID, &it.DetailCostCategoryID,
			&it.BoqItemType, &it.TotalCommercialWorkCost, &it.TotalCommercialMaterialCost); err != nil {
			return nil, fmt.Errorf("redistributionRepo: boq scan: %w", err)
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// ─── loader ──────────────────────────────────────────────────────────────────

// RedistributionLoad is the loader payload: all result rows for a
// (tender, tactic), the rules JSONB from the single holder row, the snapshot
// status, and — for a server-authoritative snapshot — the prepared projection
// built by the SAME calc boundary the save uses.
type RedistributionLoad struct {
	Results []RedistributionRecord `json:"results"`
	Rules   json.RawMessage        `json:"redistribution_rules"`
	// Status: calculated | requires_recalculation | not_configured.
	Status string `json:"status"`
	// Reason — stable machine-readable code, set ONLY for
	// requires_recalculation (LEGACY_SNAPSHOT / SNAPSHOT_SET_MISMATCH /
	// PREPARED_INPUT_CHANGED / INSURANCE_ALLOCATION_INVALID /
	// PREPARED_CALCULATION_FAILED). Never carries internal error details.
	Reason string `json:"reason,omitempty"`
	// Message — user-facing summary matching Reason.
	Message string `json:"message,omitempty"`
	// Prepared — nil unless Status == calculated.
	Prepared *calc.PreparedRedistribution `json:"prepared,omitempty"`
}

// rulesServerMetadata mirrors ONLY the server markers inside the rules JSONB.
type rulesServerMetadata struct {
	SchemaVersion     int    `json:"schema_version"`
	CalculationSource string `json:"calculation_source"`
	// FinancialInputRevision — 0-F2 §7: the tender input revision the server
	// snapshot was built for. nil on snapshots saved before 0-F2.
	FinancialInputRevision *int64 `json:"financial_input_revision"`
}

// stampRulesInputRevision enriches the canonical rules JSON with the input
// revision the snapshot was calculated for. Pure repo-level metadata: the calc
// canonical shape (and its Go↔TS golden fixtures) is untouched.
func stampRulesInputRevision(canonicalJSON []byte, revision int64) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(canonicalJSON, &m); err != nil {
		return nil, fmt.Errorf("stampRulesInputRevision: %w", err)
	}
	m["financial_input_revision"] = revision
	out, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("stampRulesInputRevision: %w", err)
	}
	return out, nil
}

// LoadResults returns every cost_redistribution_results row for the given
// (tender_id, markup_tactic_id), the rules JSONB from the single holder row,
// the snapshot status, and — for a server snapshot — the prepared projection
// rebuilt by the SAME calc boundary the save uses (§9: save/GET parity).
//
// Everything is read in ONE read-only transaction (consistent view; no
// per-position queries). A snapshot without the server markers
// (schema_version >= 2 AND calculation_source == "server") was calculated by a
// pre-0.1.2.3a client — status requires_recalculation, no prepared rows. If a
// server snapshot can no longer produce a valid prepared projection (inputs
// changed since the save — the stale-snapshot risk deferred to 0.1.3), the
// status degrades to requires_recalculation instead of returning a 500.
func (r *RedistributionRepo) LoadResults(
	ctx context.Context,
	tenderID, tacticID string,
) (*RedistributionLoad, error) {
	out := &RedistributionLoad{Results: []RedistributionRecord{}, Status: RedistributionStatusNotConfigured}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("redistributionRepo.LoadResults: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	const rulesQ = `
		SELECT redistribution_rules
		FROM public.cost_redistribution_results
		WHERE tender_id = $1 AND markup_tactic_id = $2
		  AND redistribution_rules IS NOT NULL
		ORDER BY created_at ASC
		LIMIT 1
	`
	var rawRules []byte
	if err := tx.QueryRow(ctx, rulesQ, tenderID, tacticID).Scan(&rawRules); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("redistributionRepo.LoadResults: rules: %w", err)
		}
	}
	if len(rawRules) > 0 {
		out.Rules = json.RawMessage(rawRules)
	}

	const resQ = `
		SELECT boq_item_id::text,
		       COALESCE(original_work_cost, 0),
		       deducted_amount,
		       added_amount,
		       COALESCE(final_work_cost, 0)
		FROM public.cost_redistribution_results
		WHERE tender_id = $1 AND markup_tactic_id = $2
		ORDER BY boq_item_id ASC
	`
	rows, err := tx.Query(ctx, resQ, tenderID, tacticID)
	if err != nil {
		return nil, fmt.Errorf("redistributionRepo.LoadResults: query: %w", err)
	}
	func() {
		defer rows.Close()
		for rows.Next() {
			var rec RedistributionRecord
			if err = rows.Scan(
				&rec.BoqItemID, &rec.OriginalWorkCost, &rec.DeductedAmount,
				&rec.AddedAmount, &rec.FinalWorkCost,
			); err != nil {
				return
			}
			out.Results = append(out.Results, rec)
		}
		err = rows.Err()
	}()
	if err != nil {
		return nil, fmt.Errorf("redistributionRepo.LoadResults: scan: %w", err)
	}

	if len(out.Results) == 0 {
		return out, nil // not_configured
	}

	out.Status = RedistributionStatusRequiresRecalculation
	out.Reason = RedistributionReasonLegacySnapshot
	out.Message = "Сохранённый расчёт создан старой версией и требует пересчёта на сервере."
	var rules calc.RedistributionRulesInput
	var meta rulesServerMetadata
	if len(rawRules) > 0 {
		if json.Unmarshal(rawRules, &meta) == nil &&
			meta.SchemaVersion >= calc.RedistributionSchemaVersion &&
			meta.CalculationSource == calc.RedistributionCalculationServer &&
			json.Unmarshal(rawRules, &rules) == nil {
			out.Status = RedistributionStatusCalculated
			out.Reason, out.Message = "", ""
		}
	}
	if out.Status != RedistributionStatusCalculated {
		return out, nil // legacy snapshot: no authoritative prepared rows
	}

	// 0-F2 §7: the snapshot must have been built for the CURRENT financial
	// input revision. This catches stale snapshots even when the BOQ id set is
	// unchanged (rates/markup/insurance edits) — the gap the exact-set check
	// could not see. A pre-0-F2 server snapshot (no marker) is trusted only
	// while the tender has had no revisions at all.
	var tenderInputRev int64
	if err := tx.QueryRow(ctx,
		`SELECT financial_input_revision FROM public.tenders WHERE id = $1::uuid`, tenderID,
	).Scan(&tenderInputRev); err != nil {
		return nil, fmt.Errorf("redistributionRepo.LoadResults: tender revision: %w", err)
	}
	snapshotRev := int64(0)
	if meta.FinancialInputRevision != nil {
		snapshotRev = *meta.FinancialInputRevision
	}
	if snapshotRev != tenderInputRev {
		out.Status = RedistributionStatusRequiresRecalculation
		out.Reason = RedistributionReasonInputRevisionChanged
		out.Message = "Финансовые данные тендера изменились после сохранения перераспределения — требуется пересчёт."
		out.Prepared = nil
		return out, nil
	}

	// Rebuild the prepared projection from the persisted server snapshot +
	// current server-side positions/BOQ/insurance — the same engine as save.
	items, err := loadRedistributionBoq(ctx, tx, tenderID)
	if err != nil {
		return nil, fmt.Errorf("redistributionRepo.LoadResults: %w", err)
	}
	categoryResults := make([]calc.RedistributionResult, len(out.Results))
	for i, rec := range out.Results {
		categoryResults[i] = calc.RedistributionResult{
			BoqItemID:        rec.BoqItemID,
			OriginalWorkCost: rec.OriginalWorkCost,
			DeductedAmount:   rec.DeductedAmount,
			AddedAmount:      rec.AddedAmount,
			FinalWorkCost:    rec.FinalWorkCost,
		}
	}
	adjustments := rules.PositionAdjustments
	if rules.LegacyPositionAdjustment != nil && len(adjustments) == 0 &&
		rules.LegacyPositionAdjustment.Amount > 0 {
		adjustments = []calc.PositionAdjustmentRuleInput{*rules.LegacyPositionAdjustment}
	}
	prepared, err := buildPreparedTx(ctx, tx, tenderID, items, categoryResults, adjustments)
	if err != nil {
		// The persisted snapshot can no longer produce a valid prepared
		// projection (stale/incomplete/invalid state — the 0.1.3 risk).
		// Degrade honestly with a STABLE reason code instead of a 500; the
		// typed context is logged server-side and never leaks into the API.
		out.Status = RedistributionStatusRequiresRecalculation
		out.Reason, out.Message = classifyPreparedFailure(err)
		out.Prepared = nil
		log.Warn().
			Err(err).
			Str("tender_id", tenderID).
			Str("markup_tactic_id", tacticID).
			Str("reason", out.Reason).
			Msg("redistribution prepared projection degraded to requires_recalculation")
		return out, nil
	}
	out.Prepared = prepared
	return out, nil
}

// classifyPreparedFailure maps a prepared-build error onto the stable reason
// codes + user-facing messages of the GET contract.
func classifyPreparedFailure(err error) (reason, message string) {
	var setErr *calc.RedistributionSnapshotSetMismatchError
	if errors.As(err, &setErr) {
		return RedistributionReasonSetMismatch,
			"Сохранённый расчёт не соответствует текущему составу BOQ. Выполните пересчёт."
	}
	var insAlloc *calc.InvalidInsuranceAllocationError
	var insConf *calc.InvalidInsuranceConfigurationError
	if errors.As(err, &insAlloc) || errors.As(err, &insConf) {
		return RedistributionReasonInsuranceInvalid,
			"Страхование не может быть распределено для текущего состояния. Проверьте конфигурацию и выполните пересчёт."
	}
	var rulesErr *calc.InvalidRedistributionRulesError
	var inputErr *calc.InvalidPreparedRedistributionInputError
	if errors.As(err, &rulesErr) || errors.As(err, &inputErr) {
		return RedistributionReasonInputChanged,
			"Входные данные перераспределения изменились. Выполните пересчёт."
	}
	return RedistributionReasonPreparedFailed,
		"Расчёт перераспределения устарел или неполон. Выполните пересчёт."
}
