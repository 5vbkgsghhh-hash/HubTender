package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CloneResult mirrors the JSONB returned by
// public.clone_tender_as_new_version. Field names are verbatim from the
// SQL function's jsonb_build_object.
type CloneResult struct {
	TenderID                    string `json:"tenderId"`
	Version                     int    `json:"version"`
	PositionsCopied             int    `json:"positionsCopied"`
	PositionParentLinksRestored int    `json:"positionParentLinksRestored"`
	BoqItemsCopied              int    `json:"boqItemsCopied"`
	ParentLinksRestored         int    `json:"parentLinksRestored"`
	CostVolumesCopied           int    `json:"costVolumesCopied"`
	InsuranceRowsCopied         int    `json:"insuranceRowsCopied"`
	SubcontractExclusionsCopied int    `json:"subcontractExclusionsCopied"`
	PricingDistributionCopied   int    `json:"pricingDistributionCopied"`
	MarkupPercentageCopied      int    `json:"markupPercentageCopied"`
	DocumentsCopied             int    `json:"documentsCopied"`
	NotesCopied                 int    `json:"notesCopied"`
	GroupsCopied                int    `json:"groupsCopied"`
	UserFiltersTransferred      int    `json:"userFiltersTransferred"`
}

// ErrClone is a typed error carrying an HTTP status so the handler can
// dispatch 404 vs 500 without string matching at the handler layer.
type ErrClone struct {
	HTTPStatus int
	Message    string
}

func (e *ErrClone) Error() string { return e.Message }

// CloneRepo invokes the public.clone_tender_as_new_version SQL function
// inside an explicit transaction (see CloneTender for why).
type CloneRepo struct {
	pool *pgxpool.Pool
}

// NewCloneRepo creates a CloneRepo.
func NewCloneRepo(pool *pgxpool.Pool) *CloneRepo {
	return &CloneRepo{pool: pool}
}

// CloneTender calls public.clone_tender_as_new_version(p_source_tender_id)
// and decodes its JSONB result. A missing source tender surfaces as the
// function's RAISE EXCEPTION ('Source tender % not found') → mapped to 404.
//
// The call runs inside an explicit transaction so we can suppress the per-row
// grand-total recompute (O(N²) over boq_items) for the duration of the bulk
// copy and recompute the total once before commit — mirroring
// TransferRepo.ExecuteVersionTransfer.
func (r *CloneRepo) CloneTender(ctx context.Context, sourceTenderID string) (*CloneResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("cloneRepo.CloneTender: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := setAuditUser(ctx, tx, ""); err != nil {
		return nil, err
	}

	// Suppress the per-row grand-total recompute (trg_boq_items_grand_total,
	// FOR EACH ROW) during the bulk INSERT inside the SQL function; recomputed
	// once below. SET LOCAL is transaction-scoped, so it cannot leak across
	// PgBouncer-pooled connections.

	var raw []byte
	err = tx.QueryRow(ctx,
		`SELECT public.clone_tender_as_new_version($1::uuid)`,
		sourceTenderID,
	).Scan(&raw)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			if strings.Contains(strings.ToLower(pgErr.Message), "not found") {
				return nil, &ErrClone{HTTPStatus: 404, Message: pgErr.Message}
			}
			return nil, &ErrClone{HTTPStatus: 500, Message: pgErr.Message}
		}
		return nil, fmt.Errorf("cloneRepo.CloneTender: %w", err)
	}

	var res CloneResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("cloneRepo.CloneTender: decode result: %w", err)
	}

	// Recompute cached_grand_total once for the new tender via the canonical
	// Go/calc helper (stage 0.1.2.4a: the SQL function is a tombstone and the
	// per-row triggers are gone).
	if _, err := RecalculateTenderGrandTotalTx(ctx, tx, res.TenderID); err != nil {
		return nil, fmt.Errorf("cloneRepo.CloneTender: recompute grand total: %w", err)
	}

	// Carry every user's saved position filter onto the cloned version. A clone
	// is a 1:1 copy, so old→new is a direct join on position_number (+ is_additional
	// + work_name to disambiguate). This covers ДОП positions too. The helper's
	// section re-expansion is idempotent here (no new/deleted rows).
	oldToNew := make(map[string]string)
	{
		mapRows, err := tx.Query(ctx, `
			SELECT o.id::text, n.id::text
			FROM public.client_positions o
			JOIN public.client_positions n
			  ON n.tender_id = $2::uuid
			 AND n.position_number = o.position_number
			 AND COALESCE(n.is_additional, false) = COALESCE(o.is_additional, false)
			 AND n.work_name = o.work_name
			WHERE o.tender_id = $1::uuid
		`, sourceTenderID, res.TenderID)
		if err != nil {
			return nil, fmt.Errorf("cloneRepo.CloneTender: build position map: %w", err)
		}
		for mapRows.Next() {
			var oldID, newID string
			if err := mapRows.Scan(&oldID, &newID); err != nil {
				mapRows.Close()
				return nil, fmt.Errorf("cloneRepo.CloneTender: scan position map: %w", err)
			}
			oldToNew[oldID] = newID
		}
		mapRows.Close()
		if err := mapRows.Err(); err != nil {
			return nil, fmt.Errorf("cloneRepo.CloneTender: iterate position map: %w", err)
		}
	}
	res.UserFiltersTransferred, err = transferUserPositionFilters(ctx, tx, sourceTenderID, res.TenderID, oldToNew)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("cloneRepo.CloneTender: commit: %w", err)
	}
	return &res, nil
}
