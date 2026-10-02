package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/su10/hubtender/backend/internal/middleware"
	"github.com/su10/hubtender/backend/internal/repository"
)

// Контракт строк «доп» на POST /api/v1/imports/boq: additional_positions и
// client_position_temp_id доезжают до сервиса, созданные ДОП — до ответа.

type stubImportBoqSvc struct {
	got repository.ImportInput
	res *repository.ImportResult
	err error
}

func (s *stubImportBoqSvc) BulkImport(_ context.Context, in repository.ImportInput) (*repository.ImportResult, error) {
	s.got = in
	if s.err != nil {
		return nil, s.err
	}
	return s.res, nil
}

func doImportBoq(t *testing.T, svc importBoqServicer, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/v1/imports/boq", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.CtxUser,
		&middleware.AuthUser{ID: "user-1"}))
	w := httptest.NewRecorder()
	NewImportBoqHandler(svc).BulkImport(w, req)
	return w
}

func TestImportBoqHandler_AdditionalPositionsContract(t *testing.T) {
	svc := &stubImportBoqSvc{res: &repository.ImportResult{
		InsertedItemsCount: 1,
		CreatedAdditionalPositions: []repository.CreatedAdditionalPosition{
			{ID: "dop-id", ParentPositionID: "pos-id", PositionNumber: 5.1, WorkName: "Перемычки"},
		},
	}}
	body := `{
		"tender_id": "6f1c1c0e-6a8b-4a43-9a43-0d0e6f3c0b11",
		"file_name": "dop.xlsx",
		"items": [{"row_index": 4, "client_position_temp_id": "dop_3", "boq_item_type": "раб", "quantity": 2}],
		"additional_positions": [{"temp_id": "dop_3", "row_index": 3, "parent_position_id": "pos-id",
			"work_name": "Перемычки", "unit_code": "шт", "manual_volume": 12, "manual_note": "прим"}]
	}`
	w := doImportBoq(t, svc, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	if len(svc.got.AdditionalPositions) != 1 {
		t.Fatalf("additional_positions not passed: %+v", svc.got.AdditionalPositions)
	}
	d := svc.got.AdditionalPositions[0]
	if d.TempID != "dop_3" || d.ParentPositionID != "pos-id" || d.WorkName != "Перемычки" ||
		d.UnitCode == nil || *d.UnitCode != "шт" || d.ManualVolume == nil || *d.ManualVolume != 12 ||
		d.RowIndex == nil || *d.RowIndex != 3 {
		t.Fatalf("additional position decoded wrong: %+v", d)
	}
	if len(svc.got.Items) != 1 || svc.got.Items[0].ClientPositionTempID == nil ||
		*svc.got.Items[0].ClientPositionTempID != "dop_3" || svc.got.Items[0].ClientPositionID != "" {
		t.Fatalf("item temp ref decoded wrong: %+v", svc.got.Items)
	}

	var resp struct {
		Created []struct {
			ID             string  `json:"id"`
			PositionNumber float64 `json:"position_number"`
		} `json:"created_additional_positions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Created) != 1 || resp.Created[0].ID != "dop-id" || resp.Created[0].PositionNumber != 5.1 {
		t.Fatalf("created_additional_positions = %+v", resp.Created)
	}
}

func TestImportBoqHandler_EmptyCreatedIsArray(t *testing.T) {
	svc := &stubImportBoqSvc{res: &repository.ImportResult{}}
	w := doImportBoq(t, svc, `{"tender_id": "6f1c1c0e-6a8b-4a43-9a43-0d0e6f3c0b11", "file_name": "x.xlsx"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"created_additional_positions":[]`) {
		t.Fatalf("want empty array, got %s", w.Body.String())
	}
}

func TestImportBoqHandler_DuplicateDopIs400(t *testing.T) {
	svc := &stubImportBoqSvc{err: &repository.ErrBulkImport{
		Message: "Строка 3: у позиции 5 уже есть ДОП 5.1 «Перемычки» — повторно создать её нельзя"}}
	w := doImportBoq(t, svc, `{"tender_id": "6f1c1c0e-6a8b-4a43-9a43-0d0e6f3c0b11", "file_name": "x.xlsx"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "уже есть ДОП 5.1") {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
}
