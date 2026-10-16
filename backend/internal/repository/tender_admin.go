package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// AdminTenderPatch is the admin-modal patch shape (no ETag check, all fields).
type AdminTenderPatch struct {
	Title              *string    `json:"title"`
	Description        *string    `json:"description"`
	ClientName         *string    `json:"client_name"`
	TenderNumber       *string    `json:"tender_number"`
	SubmissionDeadline *time.Time `json:"submission_deadline"`
	Version            *int       `json:"version"`
	AreaClient         *float64   `json:"area_client"`
	AreaSP             *float64   `json:"area_sp"`
	USDRate            *float64   `json:"usd_rate"`
	EURRate            *float64   `json:"eur_rate"`
	CNYRate            *float64   `json:"cny_rate"`
	UploadFolder       *string    `json:"upload_folder"`
	BSMLink            *string    `json:"bsm_link"`
	TZLink             *string    `json:"tz_link"`
	QAFormLink         *string    `json:"qa_form_link"`
	ProjectFolderLink  *string    `json:"project_folder_link"`
	HousingClass       *string    `json:"housing_class"`
	ConstructionScope  *string    `json:"construction_scope"`
	IsArchived         *bool      `json:"is_archived"`
	MarkupTacticID     *string    `json:"markup_tactic_id"`
	VolumeTitle        *string    `json:"volume_title"`
}

// AdminPatchTender applies the non-nil fields. Used by the admin tenders page
// (no optimistic concurrency check — the existing PATCH with ETag remains for
// other callers).
//
// Stage 0-F1: the admin path has the SAME fail-closed rate semantics as the
// regular update — a currency-rate change runs the UPDATE plus the full
// reprice pipeline in one transaction (see repriceTenderAfterRateChangeTx);
// it can not bypass the recompute or partially apply.
func (r *TenderRepo) AdminPatchTender(ctx context.Context, id string, p AdminTenderPatch) error {
	args := []any{}
	setClauses := ""
	add := func(col string, val any) {
		if setClauses != "" {
			setClauses += ", "
		}
		setClauses += fmt.Sprintf("%s = $%d", col, len(args)+1)
		args = append(args, val)
	}
	if p.Title != nil {
		add("title", *p.Title)
	}
	if p.Description != nil {
		add("description", *p.Description)
	}
	if p.ClientName != nil {
		add("client_name", *p.ClientName)
	}
	if p.TenderNumber != nil {
		add("tender_number", *p.TenderNumber)
	}
	if p.SubmissionDeadline != nil {
		add("submission_deadline", *p.SubmissionDeadline)
	}
	if p.Version != nil {
		add("version", *p.Version)
	}
	if p.AreaClient != nil {
		add("area_client", *p.AreaClient)
	}
	if p.AreaSP != nil {
		add("area_sp", *p.AreaSP)
	}
	if p.USDRate != nil {
		add("usd_rate", *p.USDRate)
	}
	if p.EURRate != nil {
		add("eur_rate", *p.EURRate)
	}
	if p.CNYRate != nil {
		add("cny_rate", *p.CNYRate)
	}
	if p.UploadFolder != nil {
		add("upload_folder", *p.UploadFolder)
	}
	if p.BSMLink != nil {
		add("bsm_link", *p.BSMLink)
	}
	if p.TZLink != nil {
		add("tz_link", *p.TZLink)
	}
	if p.QAFormLink != nil {
		add("qa_form_link", *p.QAFormLink)
	}
	if p.ProjectFolderLink != nil {
		add("project_folder_link", *p.ProjectFolderLink)
	}
	if p.HousingClass != nil {
		add("housing_class", *p.HousingClass)
	}
	if p.ConstructionScope != nil {
		add("construction_scope", *p.ConstructionScope)
	}
	if p.IsArchived != nil {
		add("is_archived", *p.IsArchived)
	}
	if p.MarkupTacticID != nil {
		add("markup_tactic_id", *p.MarkupTacticID)
	}
	if p.VolumeTitle != nil {
		add("volume_title", *p.VolumeTitle)
	}
	if setClauses == "" {
		return nil
	}
	setClauses += ", updated_at = NOW()"
	args = append(args, id)
	q := fmt.Sprintf(`UPDATE public.tenders SET %s WHERE id = $%d`, setClauses, len(args))

	// The admin modal re-submits EVERY form field on every save, so the mere
	// presence of usd_rate/eur_rate/cny_rate says nothing about whether they
	// moved. Financial relevance is therefore decided by VALUE, inside the tx
	// (see diffFinancialInputsTx) — a presence-based check made an ordinary
	// title edit run the full reprice pipeline and revoke the approval.
	if p.USDRate == nil && p.EURRate == nil && p.CNYRate == nil && p.MarkupTacticID == nil {
		if _, err := r.pool.Exec(ctx, q, args...); err != nil {
			return fmt.Errorf("tenderRepo.AdminPatchTender: %w", err)
		}
		return nil
	}

	// Financial fields submitted → one tx: diff under a row lock, and only on a
	// genuine change bump the revision first, then apply the patch.
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("tenderRepo.AdminPatchTender: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// markup_tactic_id changes the commercial config → financial input
	// (category A: stale + async recalc; the service enqueues after commit).
	diff, err := diffFinancialInputsTx(ctx, tx, id, p.USDRate, p.EURRate, p.CNYRate, p.MarkupTacticID)
	if err != nil {
		return fmt.Errorf("tenderRepo.AdminPatchTender: %w", err)
	}
	ratesChanged := diff.RatesChanged
	tacticChanged := diff.TacticChanged
	if !ratesChanged && !tacticChanged {
		// Submitted rates/tactic equal the stored ones → this is a plain field
		// update. It must not bump the revision, must not revoke the financial
		// approval and must not reprice: no calculation input moved.
		if _, err := tx.Exec(ctx, q, args...); err != nil {
			return fmt.Errorf("tenderRepo.AdminPatchTender: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("tenderRepo.AdminPatchTender: commit: %w", err)
		}
		return nil
	}

	revision, err := MarkTenderFinancialInputsChangedTx(ctx, tx, id, "admin_tender_patch")
	if err != nil {
		return fmt.Errorf("tenderRepo.AdminPatchTender: %w", err)
	}
	if _, err := tx.Exec(ctx, q, args...); err != nil {
		return fmt.Errorf("tenderRepo.AdminPatchTender: %w", err)
	}
	if ratesChanged {
		// Category B: the reprice pipeline is a FULL calculation (it reads the
		// new tactic too), so it finishes with the success CAS for `revision`.
		if err := repriceTenderAfterRateChangeTx(ctx, tx, id, revision); err != nil {
			return fmt.Errorf("tenderRepo.AdminPatchTender: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("tenderRepo.AdminPatchTender: commit: %w", err)
	}
	return nil
}

// GetUserRoleCode returns the role_code for a user from public.users.
// Used by ApproveFinancial to enforce the general_director-only rule.
func (r *TenderRepo) GetUserRoleCode(ctx context.Context, userID string) (string, error) {
	var code string
	if err := r.pool.QueryRow(ctx,
		`SELECT role_code FROM public.users WHERE id = $1`, userID,
	).Scan(&code); err != nil {
		return "", fmt.Errorf("tenderRepo.GetUserRoleCode: %w", err)
	}
	return code, nil
}

// ApproveFinancial marks the «Финансовые показатели» as approved for the given
// tender version (one public.tenders row). Irreversible by design: it only
// flips false→true, so the WHERE clause makes a repeat call a no-op. Returns
// pgx.ErrNoRows when the tender does not exist.
//
// Stage 0-F2 gates — approval is possible ONLY for a CURRENT calculation:
//  1. financial_calculation_status = 'calculated';
//  2. financial_calculation_revision = financial_input_revision;
//  3. no calculation error;
//  4. a configured redistribution snapshot must carry the current input
//     revision (stale snapshot → REDISTRIBUTION_STALE).
//
// A violated gate returns FinancialCalculationNotReadyError (RFC 7807 409).
func (r *TenderRepo) ApproveFinancial(ctx context.Context, tenderID, userID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("tenderRepo.ApproveFinancial: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Lock the tender row so the gate check and the flip are atomic against a
	// concurrent financial mutation (which would invalidate the approval).
	var (
		approved bool
		status   string
		inputRev int64
		calcRev  int64
		errCode  *string
		tacticID *string
		notReady *FinancialCalculationNotReadyError
	)
	err = tx.QueryRow(ctx, `
		SELECT financial_approved, financial_calculation_status,
		       financial_input_revision, financial_calculation_revision,
		       financial_calculation_error_code, markup_tactic_id::text
		FROM public.tenders WHERE id = $1 FOR UPDATE
	`, tenderID).Scan(&approved, &status, &inputRev, &calcRev, &errCode, &tacticID)
	if errors.Is(err, pgx.ErrNoRows) {
		return pgx.ErrNoRows
	}
	if err != nil {
		return fmt.Errorf("tenderRepo.ApproveFinancial: read: %w", err)
	}
	if approved {
		return nil // already approved → idempotent no-op success
	}

	switch {
	case status == "calculating":
		notReady = &FinancialCalculationNotReadyError{Reason: "CALCULATION_RUNNING"}
	case status == "failed" || errCode != nil:
		notReady = &FinancialCalculationNotReadyError{Reason: "CALCULATION_FAILED"}
	case status != "calculated":
		notReady = &FinancialCalculationNotReadyError{Reason: "CALCULATION_STALE"}
	case calcRev != inputRev:
		notReady = &FinancialCalculationNotReadyError{Reason: "REVISION_MISMATCH"}
	}
	if notReady == nil && tacticID != nil {
		// Redistribution configured? Its snapshot must match the CURRENT revision.
		var rawRules []byte
		err := tx.QueryRow(ctx, `
			SELECT redistribution_rules FROM public.cost_redistribution_results
			WHERE tender_id = $1::uuid AND markup_tactic_id = $2::uuid
			  AND redistribution_rules IS NOT NULL
			ORDER BY created_at ASC LIMIT 1
		`, tenderID, *tacticID).Scan(&rawRules)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("tenderRepo.ApproveFinancial: redistribution: %w", err)
		}
		if len(rawRules) > 0 {
			var meta rulesServerMetadata
			snapshotRev := int64(0)
			if json.Unmarshal(rawRules, &meta) == nil && meta.FinancialInputRevision != nil {
				snapshotRev = *meta.FinancialInputRevision
			}
			if snapshotRev != inputRev {
				notReady = &FinancialCalculationNotReadyError{Reason: "REDISTRIBUTION_STALE"}
			}
		}
	}
	if notReady != nil {
		notReady.TenderID = tenderID
		notReady.CalculationStatus = status
		notReady.InputRevision = inputRev
		notReady.CalculationRevision = calcRev
		return notReady
	}

	if _, err := tx.Exec(ctx, `
		UPDATE public.tenders
		   SET financial_approved = true,
		       financial_approved_by = $2,
		       financial_approved_at = NOW(),
		       updated_at = NOW()
		 WHERE id = $1
	`, tenderID, userID); err != nil {
		return fmt.Errorf("tenderRepo.ApproveFinancial: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("tenderRepo.ApproveFinancial: commit: %w", err)
	}
	return nil
}

// DeleteTender removes the tender (cascade is handled by FKs).
//
// Stage 0.1.2.4a: the per-row grand-total triggers are gone, so the FK cascade
// over thousands of boq_items no longer needs app.skip_grand_total. No final
// recompute is needed — the tender row itself is deleted.
func (r *TenderRepo) DeleteTender(ctx context.Context, id string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("tenderRepo.DeleteTender: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := setAuditUser(ctx, tx, ""); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `DELETE FROM public.tenders WHERE id = $1`, id); err != nil {
		return fmt.Errorf("tenderRepo.DeleteTender: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("tenderRepo.DeleteTender: commit: %w", err)
	}
	return nil
}
