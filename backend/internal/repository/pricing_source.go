package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/su10/hubtender/backend/internal/calc"
	"github.com/su10/hubtender/backend/internal/pricing"
)

type pricingReadDB interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Keep all fields that contributed to the selected source version stable
// until commit. NOWAIT prevents lock-order cycles with portal mutations.
func validateDirectSourceTx(ctx context.Context, tx pgx.Tx, op pricing.DirectOperation) error {
	src := op.SourceRef
	if src.Version == "" {
		return ErrDirectPricingStale
	}
	var version string
	switch op.SourceKind {
	case "library":
		if src.LibraryID == nil {
			return ErrDirectPricingStale
		}
		kind := "material"
		if calc.IsWorkBoqType(op.ProposedPayload.BoqItemType) {
			kind = "work"
		}
		item, err := getLibraryPricingItem(ctx, tx, *src.LibraryID, kind, true)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDirectPricingStale
		}
		if err != nil {
			return err
		}
		version = item.SourceVersion
	case "archive":
		if src.ItemID == nil {
			return ErrDirectPricingStale
		}
		var id string
		if err := tx.QueryRow(ctx, `SELECT id::text FROM public.boq_items WHERE id=$1 FOR SHARE NOWAIT`, *src.ItemID).Scan(&id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrDirectPricingStale
			}
			return err
		}
		rows, err := rawArchiveCandidatesByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			return ErrDirectPricingStale
		}
		item := rows[0]
		locks := []struct {
			query string
			id    *string
		}{
			{`SELECT id::text FROM public.tenders WHERE id=$1 FOR SHARE NOWAIT`, &item.TenderID},
			{`SELECT id::text FROM public.client_positions WHERE id=$1 FOR SHARE NOWAIT`, &item.PositionID},
			{`SELECT id::text FROM public.work_names WHERE id=$1 FOR SHARE NOWAIT`, item.WorkNameID},
			{`SELECT id::text FROM public.material_names WHERE id=$1 FOR SHARE NOWAIT`, item.MaterialNameID},
			{`SELECT id::text FROM public.detail_cost_categories WHERE id=$1 FOR SHARE NOWAIT`, item.DetailCostCategoryID},
		}
		for _, lock := range locks {
			if lock.id == nil {
				continue
			}
			if err := tx.QueryRow(ctx, lock.query, *lock.id).Scan(&id); err != nil {
				return err
			}
		}
		// A name/unit or source-context edit may have committed before its lock
		// was acquired. Re-read only after every contributing row is locked.
		rows, err = rawArchiveCandidatesByID(ctx, tx, *src.ItemID)
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			return ErrDirectPricingStale
		}
		version = rows[0].SourceVersion
	default:
		return ErrDirectTargetInvalid
	}
	if version == "" || version != src.Version {
		return ErrDirectPricingStale
	}
	return nil
}
