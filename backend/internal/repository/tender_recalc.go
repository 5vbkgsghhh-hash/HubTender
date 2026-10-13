package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/su10/hubtender/backend/internal/calc"
)

// Stage 0.1.2.4a/0.1.2.4a.1: transaction-aware orchestration of
// tenders.cached_grand_total. The FORMULA lives ONLY in
// calc.CalculateCachedTenderGrandTotal (insurance —
// calc.CalculateInsuranceTotalDecimal); SQL here does nothing financial: one
// aggregate read over boq_items, one insurance read, one exact write of the
// server-calculated result.
//
// DECIMAL-EXACT boundary (0.1.2.4a.1): every participating column is
// PostgreSQL NUMERIC, so the aggregates and insurance fields are read as
// EXACT decimal strings (::text) — never through float64 — and the final
// value is bound back as the canonical decimal string. No SQL ROUND, no SQL
// insurance expression, no epsilon, no binary rounding on the authoritative
// path; public.recalculate_tender_grand_total stays a fail-closed tombstone
// and the per-row triggers stay removed.

// CachedGrandTotalTenderNotFoundError — the UPDATE matched no tender row.
type CachedGrandTotalTenderNotFoundError struct {
	TenderID string
}

func (e *CachedGrandTotalTenderNotFoundError) Error() string {
	return "CACHED_GRAND_TOTAL_TENDER_NOT_FOUND: " + e.TenderID
}

// CachedGrandTotalWriteMismatchError — the exact-write invariant failed
// (RowsAffected != 1).
type CachedGrandTotalWriteMismatchError struct {
	TenderID string
	Updated  int64
}

func (e *CachedGrandTotalWriteMismatchError) Error() string {
	return fmt.Sprintf("CACHED_GRAND_TOTAL_WRITE_MISMATCH: tender %s, updated %d rows (want 1)", e.TenderID, e.Updated)
}

// RecalculateTenderGrandTotalTx recomputes and persists cached_grand_total for
// ONE tender inside the caller's transaction (or any Querier boundary):
//
//  1. aggregate the tender's materialized commercial totals (ONE query,
//     material and work summed separately, returned as numeric::text — no
//     per-BOQ loop, no float64);
//  2. load the insurance row as exact decimal strings and compute the total
//     via calc.CalculateInsuranceTotalDecimal (never an SQL expression);
//  3. compute the final value via calc.CalculateCachedTenderGrandTotal
//     (exact big.Rat arithmetic + single DECIMAL half-away-from-zero
//     rounding);
//  4. write the ready-made canonical decimal string (string→numeric bind is
//     exact; the column has no scale, so PostgreSQL performs no re-rounding)
//     and verify RowsAffected==1.
//
// Fail-closed: any malformed aggregate/insurance input is a typed error and
// the UPDATE never runs; the caller's transaction owns commit/rollback.
func RecalculateTenderGrandTotalTx(
	ctx context.Context,
	q Querier,
	tenderID string,
) (*calc.CachedTenderGrandTotalResult, error) {
	if tenderID == "" {
		return nil, &CachedGrandTotalTenderNotFoundError{TenderID: tenderID}
	}

	// 1. Aggregated materialized commercial totals — EXACT decimal strings.
	//    The same pass also sums total_amount (прямые затраты, «ПЗ»): it is
	//    persisted as tenders.cached_base_total so the tender list no longer
	//    aggregates boq_items on every request (см. buildTenderListQuery).
	var materialTotal, workTotal, baseTotal string
	err := q.QueryRow(ctx, `
		SELECT COALESCE(SUM(COALESCE(total_commercial_material_cost, 0)), 0)::text,
		       COALESCE(SUM(COALESCE(total_commercial_work_cost, 0)), 0)::text,
		       COALESCE(SUM(COALESCE(total_amount, 0)), 0)::text
		FROM public.boq_items
		WHERE tender_id = $1::uuid
	`, tenderID).Scan(&materialTotal, &workTotal, &baseTotal)
	if err != nil {
		return nil, fmt.Errorf("recalcTenderGrandTotalTx: aggregate: %w", err)
	}

	// 2. Insurance configuration as exact decimal strings → calc kernel
	//    (the formula is NOT duplicated here or in SQL).
	var ins calc.InsuranceDecimalInput
	haveInsurance := true
	err = q.QueryRow(ctx, `
		SELECT COALESCE(judicial_pct, 0)::text, COALESCE(total_pct, 0)::text,
		       COALESCE(apt_price_m2, 0)::text, COALESCE(apt_area, 0)::text,
		       COALESCE(parking_price_m2, 0)::text, COALESCE(parking_area, 0)::text,
		       COALESCE(storage_price_m2, 0)::text, COALESCE(storage_area, 0)::text
		FROM public.tender_insurance
		WHERE tender_id = $1::uuid
		LIMIT 1
	`, tenderID).Scan(&ins.JudicialPct, &ins.TotalPct,
		&ins.AptPriceM2, &ins.AptArea,
		&ins.ParkingPriceM2, &ins.ParkingArea,
		&ins.StoragePriceM2, &ins.StorageArea)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("recalcTenderGrandTotalTx: insurance: %w", err)
		}
		haveInsurance = false
	}
	var insurancePtr *calc.InsuranceDecimalInput
	if haveInsurance {
		insurancePtr = &ins
	}
	insuranceTotal, err := calc.CalculateInsuranceTotalDecimal(insurancePtr)
	if err != nil {
		// Materialized configuration is broken — fail closed, never write 0.
		return nil, fmt.Errorf("recalcTenderGrandTotalTx: tender %s: %w", tenderID, err)
	}
	// Exact (unrounded) decimal handoff — insurance participates in the ONE
	// final rounding inside the kernel, never a rounding of its own.
	insuranceDecimal, err := calc.ExactDecimalString("insurance_total", insuranceTotal)
	if err != nil {
		return nil, fmt.Errorf("recalcTenderGrandTotalTx: tender %s: %w", tenderID, err)
	}

	// 3. The ONE formula (exact decimal arithmetic, single final rounding).
	result, err := calc.CalculateCachedTenderGrandTotal(calc.CachedTenderGrandTotalInput{
		CommercialMaterialTotal: materialTotal,
		CommercialWorkTotal:     workTotal,
		InsuranceTotalDecimal:   insuranceDecimal,
	})
	if err != nil {
		return nil, fmt.Errorf("recalcTenderGrandTotalTx: tender %s: %w", tenderID, err)
	}

	// 4. Exact write of the canonical decimal string (never a binary float bind).
	//    cached_base_total is a plain SUM — written as the numeric::text it was
	//    read as, no rounding of its own.
	tag, err := q.Exec(ctx, `
		UPDATE public.tenders
		SET cached_grand_total = $1::numeric,
		    cached_base_total  = $3::numeric
		WHERE id = $2::uuid
	`, result.RoundedTotalDecimal, tenderID, baseTotal)
	if err != nil {
		return nil, fmt.Errorf("recalcTenderGrandTotalTx: update: %w", err)
	}
	switch tag.RowsAffected() {
	case 1:
		return result, nil
	case 0:
		return nil, &CachedGrandTotalTenderNotFoundError{TenderID: tenderID}
	default:
		return nil, &CachedGrandTotalWriteMismatchError{TenderID: tenderID, Updated: tag.RowsAffected()}
	}
}
