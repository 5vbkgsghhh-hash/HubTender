package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-playground/validator/v10"
	"github.com/su10/hubtender/backend/internal/middleware"
	"github.com/su10/hubtender/backend/internal/repository"
	"github.com/su10/hubtender/backend/pkg/apierr"
)

// importBoqServicer is the interface ImportBoqHandler depends on.
type importBoqServicer interface {
	BulkImport(ctx context.Context, in repository.ImportInput) (*repository.ImportResult, error)
}

// ImportBoqHandler handles POST /api/v1/imports/boq.
type ImportBoqHandler struct {
	svc      importBoqServicer
	validate *validator.Validate
}

// NewImportBoqHandler creates an ImportBoqHandler.
func NewImportBoqHandler(svc importBoqServicer) *ImportBoqHandler {
	return &ImportBoqHandler{svc: svc, validate: validator.New()}
}

// importBoqReq is the JSON body for POST /api/v1/imports/boq.
// Items and PositionUpdates may be empty slices — that is a valid no-op call.
type importBoqReq struct {
	TenderID        string                            `json:"tender_id" validate:"required,uuid"`
	FileName        string                            `json:"file_name" validate:"required"`
	Items           []repository.ImportBoqItem        `json:"items"`
	PositionUpdates []repository.ImportPositionUpdate `json:"position_updates"`
	// AdditionalPositions — строки «доп» (новые ДОП); элементы ссылаются на них
	// через client_position_temp_id.
	AdditionalPositions []repository.ImportAdditionalPosition `json:"additional_positions"`
}

// importBoqResp is the JSON body returned on success. TotalMismatches is the
// stage 0-F1 diagnostic report: rows whose legacy client total_amount diverged
// from the authoritative server calculation (warnings, not errors — the server
// total is what got persisted).
type importBoqResp struct {
	ImportSessionID       *string                          `json:"import_session_id"`
	InsertedItemsCount    int                              `json:"inserted_items_count"`
	UpdatedPositionsCount int                              `json:"updated_positions_count"`
	TotalMismatchCount    int                              `json:"total_mismatch_count"`
	TotalMismatches       []repository.ImportTotalMismatch `json:"total_mismatches"`
	// CreatedAdditionalPositions — ДОП, созданные строками «доп».
	CreatedAdditionalPositions []repository.CreatedAdditionalPosition `json:"created_additional_positions"`
}

// BulkImport handles POST /api/v1/imports/boq.
//
// The authenticated user's ID is taken from the JWT context (never from the
// body). Empty items/position_updates slices are accepted and produce a
// 200 with zeroed counts and a null import_session_id.
func (h *ImportBoqHandler) BulkImport(w http.ResponseWriter, r *http.Request) {
	authUser := middleware.UserFromContext(r.Context())
	if authUser == nil {
		apierr.Unauthorized("missing auth context").Render(w)
		return
	}

	var req importBoqReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierr.BadRequest("invalid JSON body").Render(w)
		return
	}
	if err := h.validate.Struct(req); err != nil {
		apierr.BadRequest("validation failed: " + err.Error()).Render(w)
		return
	}

	// Nil slices from JSON decode (omitted keys) are safe — repo handles them.
	in := repository.ImportInput{
		TenderID:            req.TenderID,
		FileName:            req.FileName,
		UserID:              authUser.ID,
		Items:               req.Items,
		PositionUpdates:     req.PositionUpdates,
		AdditionalPositions: req.AdditionalPositions,
	}

	result, err := h.svc.BulkImport(r.Context(), in)
	if err != nil {
		var bulkErr *repository.ErrBulkImport
		if errors.As(err, &bulkErr) {
			apierr.BadRequest(bulkErr.Message).Render(w)
			return
		}
		// Stage 0-F1: server-side recompute fails closed on a missing FX rate —
		// a clear 400, the whole import already rolled back.
		if renderMissingFXRate(w, err) {
			return
		}
		// Подстраховка: основные ошибки данных/ограничений уже превращаются в
		// ErrBulkImport (с номером строки) в repository.boqInsertError. Эти
		// override'ы срабатывают, только если ошибка пришла иным путём.
		if p := apierr.ProblemFromPgErr(err, map[string]string{
			"boq_items_quantity_positive":      "Количество должно быть больше нуля",
			"boq_items_base_quantity_positive": "Базовое количество должно быть больше нуля",
		}); p != nil {
			p.Render(w)
			return
		}
		apierr.InternalFromErr(w, r, err, "failed to import BOQ items")
		return
	}

	mismatches := result.TotalMismatches
	if mismatches == nil {
		mismatches = []repository.ImportTotalMismatch{}
	}
	createdDops := result.CreatedAdditionalPositions
	if createdDops == nil {
		createdDops = []repository.CreatedAdditionalPosition{}
	}
	renderJSON(w, r, http.StatusOK, importBoqResp{
		ImportSessionID:            result.ImportSessionID,
		InsertedItemsCount:         result.InsertedItemsCount,
		UpdatedPositionsCount:      result.UpdatedPositionsCount,
		TotalMismatchCount:         result.TotalMismatchCount,
		TotalMismatches:            mismatches,
		CreatedAdditionalPositions: createdDops,
	})
}
