package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/su10/hubtender/backend/internal/calc"
	"github.com/su10/hubtender/backend/internal/middleware"
	"github.com/su10/hubtender/backend/internal/repository"
)

// Stage 0.1.2.3a (§17): the save handler accepts ONLY rules — forged client
// records are dropped by the decoder and can never reach the service — and
// maps every typed domain error to RFC 7807 through the %w chain.

type stubRedistributionSvc struct {
	err          error
	out          *repository.RedistributionSaveOutput
	load         *repository.RedistributionLoad
	gotTender    string
	gotTactic    string
	gotRules     calc.RedistributionRulesInput
	gotCreatedBy string
	calls        int
}

func (s *stubRedistributionSvc) Save(
	_ context.Context, tenderID, tacticID string,
	rules calc.RedistributionRulesInput, createdBy string,
) (*repository.RedistributionSaveOutput, error) {
	s.calls++
	s.gotTender, s.gotTactic, s.gotRules, s.gotCreatedBy = tenderID, tacticID, rules, createdBy
	if s.err != nil {
		return nil, s.err
	}
	return s.out, nil
}

func (s *stubRedistributionSvc) Clear(_ context.Context, tenderID, tacticID string) (int, error) {
	s.calls++
	s.gotTender, s.gotTactic = tenderID, tacticID
	if s.err != nil {
		return 0, s.err
	}
	return 3, nil
}

func (s *stubRedistributionSvc) LoadResults(context.Context, string, string) (*repository.RedistributionLoad, error) {
	if s.load == nil {
		panic("stub load not configured")
	}
	return s.load, nil
}

const (
	rTender = "11111111-1111-1111-1111-111111111111"
	rTactic = "22222222-2222-2222-2222-222222222222"
)

func doRedistributionSave(t *testing.T, svc redistributionServicer, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewRedistributionHandler(svc)
	req := httptest.NewRequest("POST", "/api/v1/redistributions/save", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.CtxUser,
		&middleware.AuthUser{ID: "user-1"}))
	w := httptest.NewRecorder()
	h.Save(w, req)
	return w
}

func okStubOutput() *repository.RedistributionSaveOutput {
	return &repository.RedistributionSaveOutput{
		SavedCount: 1,
		Results: []repository.RedistributionRecord{{
			BoqItemID: "i1", OriginalWorkCost: 1000, DeductedAmount: 100, AddedAmount: 0, FinalWorkCost: 900,
		}},
		TotalDeducted:  100,
		TotalAdded:     100,
		IsBalanced:     true,
		CanonicalRules: json.RawMessage(`{"schema_version":2,"calculation_source":"server"}`),
		TenderID:       rTender,
	}
}

func rulesOnlyBody() string {
	return fmt.Sprintf(`{
		"tender_id": %q, "markup_tactic_id": %q,
		"rules": {
			"deductions": [{"level":"detail","detail_cost_category_id":"33333333-3333-3333-3333-333333333333","percentage":10}],
			"targets": [{"level":"detail","detail_cost_category_id":"44444444-4444-4444-4444-444444444444"}]
		}
	}`, rTender, rTactic)
}

// §17.1/12 — a rules-only request succeeds; the response carries the SERVER
// results and metadata.
func TestRedistributionSaveHandler_RulesOnlyRequest(t *testing.T) {
	svc := &stubRedistributionSvc{out: okStubOutput()}
	w := doRedistributionSave(t, svc, rulesOnlyBody())
	if w.Code != 200 {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			SavedCount        int                               `json:"saved_count"`
			Results           []repository.RedistributionRecord `json:"results"`
			CalculationSource string                            `json:"calculation_source"`
			SchemaVersion     int                               `json:"schema_version"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad response: %v", err)
	}
	if resp.Data.SavedCount != 1 || len(resp.Data.Results) != 1 || resp.Data.Results[0].FinalWorkCost != 900 {
		t.Fatalf("server results missing from response: %+v", resp.Data)
	}
	if resp.Data.CalculationSource != "server" || resp.Data.SchemaVersion != 2 {
		t.Fatalf("server metadata missing: %+v", resp.Data)
	}
	if len(svc.gotRules.Deductions) != 1 || svc.gotRules.Deductions[0].Percentage != 10 {
		t.Fatalf("rules not delivered to service: %+v", svc.gotRules)
	}
}

// §17.2-4 — a LEGACY request with forged records (including malformed financial
// values) is accepted for rolling compatibility, but records never reach the
// service: two different forged payloads produce the IDENTICAL service command.
func TestRedistributionSaveHandler_ForgedRecordsIgnored(t *testing.T) {
	forge := func(records string) string {
		return fmt.Sprintf(`{
			"tender_id": %q, "markup_tactic_id": %q,
			"records": %s,
			"created_by": "99999999-9999-9999-9999-999999999999",
			"rules": {
				"deductions": [{"level":"detail","detail_cost_category_id":"33333333-3333-3333-3333-333333333333","percentage":10}],
				"targets": [{"level":"detail","detail_cost_category_id":"44444444-4444-4444-4444-444444444444"}]
			}
		}`, rTender, rTactic, records)
	}
	forged1 := forge(`[{"boq_item_id":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa","original_work_cost":777,"deducted_amount":888888,"added_amount":999999,"final_work_cost":-123}]`)
	forged2 := forge(`[{"boq_item_id":"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb","original_work_cost":"мусор","final_work_cost":{"nested":true}}]`)

	svc1 := &stubRedistributionSvc{out: okStubOutput()}
	w1 := doRedistributionSave(t, svc1, forged1)
	svc2 := &stubRedistributionSvc{out: okStubOutput()}
	w2 := doRedistributionSave(t, svc2, forged2)

	if w1.Code != 200 || w2.Code != 200 {
		t.Fatalf("legacy payloads must stay accepted: %d / %d", w1.Code, w2.Code)
	}
	// Identical service command regardless of the forged records.
	r1, _ := json.Marshal(svc1.gotRules)
	r2, _ := json.Marshal(svc2.gotRules)
	if string(r1) != string(r2) {
		t.Fatalf("forged records leaked into the command: %s vs %s", r1, r2)
	}
	// §17.5 — actor from the auth context, not from created_by in the body.
	if svc1.gotCreatedBy != "user-1" || svc2.gotCreatedBy != "user-1" {
		t.Fatalf("actor must come from JWT, got %q/%q", svc1.gotCreatedBy, svc2.gotCreatedBy)
	}
	// Identical responses too.
	if w1.Body.String() != w2.Body.String() {
		t.Fatal("responses differ for identical rules with different forged records")
	}
}

// wrappedR mimics the repository → service %w chain.
func wrappedR(e error) error {
	return fmt.Errorf("redistributionService.Save: %w",
		fmt.Errorf("redistributionRepo.SaveAuthoritative: %w", e))
}

// §17.6 — tactic mismatch → 409 REDISTRIBUTION_TACTIC_MISMATCH.
func TestRedistributionSaveHandler_TacticMismatch409(t *testing.T) {
	svc := &stubRedistributionSvc{err: wrappedR(&calc.RedistributionTacticMismatchError{
		TenderID: rTender, RequestedTacticID: rTactic, ActiveTacticID: "other",
	})}
	w := doRedistributionSave(t, svc, rulesOnlyBody())
	if w.Code != 409 {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	m := decodeProblem(t, w)
	if m["code"] != "REDISTRIBUTION_TACTIC_MISMATCH" {
		t.Fatalf("code = %v", m["code"])
	}
}

// §17.7 — missing FX keeps its 400 MISSING_FX_RATE mapping.
func TestRedistributionSaveHandler_MissingFX400(t *testing.T) {
	svc := &stubRedistributionSvc{err: wrappedR(&calc.MissingFXRateError{Currency: "USD"})}
	w := doRedistributionSave(t, svc, rulesOnlyBody())
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if m := decodeProblem(t, w); m["code"] != "MISSING_FX_RATE" {
		t.Fatalf("code = %v", m["code"])
	}
}

// §17.8 — invalid rules → 400 with the issues array.
func TestRedistributionSaveHandler_InvalidRules400(t *testing.T) {
	svc := &stubRedistributionSvc{err: wrappedR(&calc.InvalidRedistributionRulesError{
		Issues: []calc.RuleIssue{{Field: "deductions[0].percentage", Code: "PERCENTAGE_OUT_OF_RANGE", Message: "…"}},
	})}
	w := doRedistributionSave(t, svc, rulesOnlyBody())
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	m := decodeProblem(t, w)
	if m["code"] != "INVALID_REDISTRIBUTION_RULES" {
		t.Fatalf("code = %v", m["code"])
	}
	issues, ok := m["issues"].([]any)
	if !ok || len(issues) != 1 {
		t.Fatalf("issues missing: %v", m["issues"])
	}
	first, _ := issues[0].(map[string]any)
	if first["field"] != "deductions[0].percentage" || first["code"] != "PERCENTAGE_OUT_OF_RANGE" {
		t.Fatalf("issue shape wrong: %v", first)
	}
}

// §17.9 — unbalanced output → 409, never persisted as success.
func TestRedistributionSaveHandler_Unbalanced409(t *testing.T) {
	svc := &stubRedistributionSvc{err: wrappedR(&calc.UnbalancedRedistributionError{
		TotalDeducted: 100, TotalAdded: 50,
	})}
	w := doRedistributionSave(t, svc, rulesOnlyBody())
	if w.Code != 409 {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	if m := decodeProblem(t, w); m["code"] != "REDISTRIBUTION_UNBALANCED" {
		t.Fatalf("code = %v", m["code"])
	}
}

// No BOQ items → 400 REDISTRIBUTION_NO_BOQ_ITEMS.
func TestRedistributionSaveHandler_NoBoqItems400(t *testing.T) {
	svc := &stubRedistributionSvc{err: wrappedR(&calc.RedistributionNoBoqItemsError{TenderID: rTender})}
	w := doRedistributionSave(t, svc, rulesOnlyBody())
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if m := decodeProblem(t, w); m["code"] != "REDISTRIBUTION_NO_BOQ_ITEMS" {
		t.Fatalf("code = %v", m["code"])
	}
}

// ─── Stage 0.1.2.3b (§21) ─────────────────────────────────────────────────────

func okStubOutputWithPrepared() *repository.RedistributionSaveOutput {
	out := okStubOutput()
	out.Prepared = &calc.PreparedRedistribution{
		Rows: []calc.PreparedPositionRow{{
			PositionID: "p1", Quantity: 1,
			WorkCostRounded: 900, FinalWorkCost: 950, InsuranceAmount: 50,
			FinalPositionTotal: 950,
		}},
		Summary:               calc.PreparedSummary{FinalWorkTotal: 950, FinalTotal: 950, InsuranceTotal: 50, InsuranceAllocated: 50},
		RoundingPolicy:        calc.RoundingPolicyUnitPrice2dp,
		PreparedSchemaVersion: calc.PreparedSchemaVersion,
		CalculationSource:     calc.RedistributionCalculationServer,
	}
	return out
}

// §21.1/19/20 — save returns the full server prepared result with markers.
func TestRedistributionSaveHandler_PreparedInResponse(t *testing.T) {
	svc := &stubRedistributionSvc{out: okStubOutputWithPrepared()}
	w := doRedistributionSave(t, svc, rulesOnlyBody())
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	var resp struct {
		Data struct {
			Prepared *calc.PreparedRedistribution `json:"prepared"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad response: %v", err)
	}
	p := resp.Data.Prepared
	if p == nil || len(p.Rows) != 1 || p.Rows[0].FinalWorkCost != 950 {
		t.Fatalf("prepared missing/wrong: %+v", p)
	}
	if p.CalculationSource != "server" || p.PreparedSchemaVersion != 1 || p.RoundingPolicy != "unit_price_2dp" {
		t.Fatalf("prepared markers wrong: %+v", p)
	}
}

// §21.3/4 — the request DTO does not accept prepared rows / forged
// position/insurance/rounding values: two requests with different forged
// prepared payloads produce the identical service command and response.
func TestRedistributionSaveHandler_ForgedPreparedIgnored(t *testing.T) {
	forge := func(extra string) string {
		return fmt.Sprintf(`{
			"tender_id": %q, "markup_tactic_id": %q,
			%s
			"rules": {
				"deductions": [{"level":"detail","detail_cost_category_id":"33333333-3333-3333-3333-333333333333","percentage":10}],
				"targets": [{"level":"detail","detail_cost_category_id":"44444444-4444-4444-4444-444444444444"}]
			}
		}`, rTender, rTactic, extra)
	}
	forged1 := forge(`"prepared_rows":[{"position_id":"x","final_work_cost":999999}],
		"position_results":[{"position_id":"x","deducted":777}],
		"insurance_total": 888888, "rounding_adjustments": {"x": -123},
		"summary": {"final_total": 1},`)
	forged2 := forge(`"prepared": {"rows":[],"summary":{"final_total":42}},`)

	svc1 := &stubRedistributionSvc{out: okStubOutputWithPrepared()}
	w1 := doRedistributionSave(t, svc1, forged1)
	svc2 := &stubRedistributionSvc{out: okStubOutputWithPrepared()}
	w2 := doRedistributionSave(t, svc2, forged2)

	if w1.Code != 200 || w2.Code != 200 {
		t.Fatalf("statuses: %d / %d", w1.Code, w2.Code)
	}
	r1, _ := json.Marshal(svc1.gotRules)
	r2, _ := json.Marshal(svc2.gotRules)
	if string(r1) != string(r2) {
		t.Fatal("forged prepared values leaked into the command")
	}
	if w1.Body.String() != w2.Body.String() {
		t.Fatal("responses differ for identical rules with different forged prepared payloads")
	}
}

// §21.11 — invalid insurance configuration → typed RFC 7807 400.
func TestRedistributionSaveHandler_InvalidInsurance400(t *testing.T) {
	svc := &stubRedistributionSvc{err: wrappedR(&calc.InvalidInsuranceConfigurationError{
		Field: "judicial_pct", Reason: "percentage out of range [0,100]",
	})}
	w := doRedistributionSave(t, svc, rulesOnlyBody())
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	m := decodeProblem(t, w)
	if m["code"] != "INVALID_INSURANCE_CONFIGURATION" || m["field"] != "judicial_pct" {
		t.Fatalf("code/field = %v/%v", m["code"], m["field"])
	}
}

// §21.8/9/17 — GET is a pure passthrough of the repo statuses (no mutation):
// legacy → requires_recalculation without prepared; missing → not_configured.
func TestRedistributionLoadHandler_Statuses(t *testing.T) {
	cases := []struct {
		name       string
		load       *repository.RedistributionLoad
		wantReason string
	}{
		{"legacy", &repository.RedistributionLoad{
			Results: []repository.RedistributionRecord{{BoqItemID: "b1"}},
			Status:  repository.RedistributionStatusRequiresRecalculation,
			Reason:  repository.RedistributionReasonLegacySnapshot,
			Message: "Сохранённый расчёт создан старой версией и требует пересчёта на сервере.",
		}, "LEGACY_SNAPSHOT"},
		{"set mismatch", &repository.RedistributionLoad{
			Results: []repository.RedistributionRecord{{BoqItemID: "b1"}},
			Status:  repository.RedistributionStatusRequiresRecalculation,
			Reason:  repository.RedistributionReasonSetMismatch,
		}, "SNAPSHOT_SET_MISMATCH"},
		{"insurance", &repository.RedistributionLoad{
			Results: []repository.RedistributionRecord{{BoqItemID: "b1"}},
			Status:  repository.RedistributionStatusRequiresRecalculation,
			Reason:  repository.RedistributionReasonInsuranceInvalid,
		}, "INSURANCE_ALLOCATION_INVALID"},
		{"missing", &repository.RedistributionLoad{
			Results: []repository.RedistributionRecord{},
			Status:  repository.RedistributionStatusNotConfigured,
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &stubRedistributionSvc{load: tc.load}
			h := NewRedistributionHandler(svc)
			req := httptest.NewRequest("GET",
				"/api/v1/redistributions?tender_id="+rTender+"&markup_tactic_id="+rTactic, nil)
			req = req.WithContext(context.WithValue(req.Context(), middleware.CtxUser,
				&middleware.AuthUser{ID: "user-1"}))
			w := httptest.NewRecorder()
			h.Load(w, req)
			if w.Code != 200 {
				t.Fatalf("status = %d", w.Code)
			}
			var resp struct {
				Data struct {
					Status   string                       `json:"status"`
					Reason   string                       `json:"reason"`
					Prepared *calc.PreparedRedistribution `json:"prepared"`
				} `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("bad response: %v", err)
			}
			if resp.Data.Status != tc.load.Status {
				t.Fatalf("status = %q, want %q", resp.Data.Status, tc.load.Status)
			}
			// §14.3-6 — stable reason code, никакого текст-парсинга на фронте.
			if resp.Data.Reason != tc.wantReason {
				t.Fatalf("reason = %q, want %q", resp.Data.Reason, tc.wantReason)
			}
			if resp.Data.Prepared != nil {
				t.Fatal("prepared must be absent for non-calculated statuses")
			}
		})
	}
}

// ─── Pipeline invariants must not degrade into a bare 500 ────────────────────
//
// Before this mapping every failure below produced HTTP 500 with the constant
// detail "failed to save redistribution results", so a stale snapshot, a broken
// ДОП hierarchy and a genuine internal bug were indistinguishable both for the
// user and for support. Each now carries a stable code the frontend can branch
// on.
func TestRedistributionSaveHandler_PipelineInvariants409(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"prepared input", &calc.InvalidPreparedRedistributionInputError{
			Field: "positions", EntityID: "p1", Reason: calc.AdditionalPositionParentMissingReason,
		}, "REDISTRIBUTION_PREPARED_INPUT_INVALID"},
		{"prepared invariant", &calc.InvalidPreparedRedistributionResultError{
			Field: "summary.final_total", Reason: "final_total != materials + works",
		}, "REDISTRIBUTION_PREPARED_INVARIANT_FAILED"},
		{"snapshot set", &calc.RedistributionSnapshotSetMismatchError{
			ExpectedCount: 2, ActualCount: 1, Reason: "set mismatch",
		}, "REDISTRIBUTION_SNAPSHOT_SET_MISMATCH"},
		{"insurance allocation", &calc.InvalidInsuranceAllocationError{
			ExpectedTotal: 50, AllocatedTotal: 0, Reason: calc.InsuranceZeroBaseReason,
		}, "INSURANCE_ALLOCATION_INVALID"},
		{"calculation result", &calc.InvalidRedistributionCalculationResultError{
			Field: "persist", Reason: "exact-set mismatch",
		}, "REDISTRIBUTION_CALCULATION_INVALID"},
		{"commercial result", &repository.InvalidCommercialCalculationResultError{
			ItemID: "b1", Field: "total_commercial_work_cost", Reason: "отрицательное значение",
		}, "COMMERCIAL_CALCULATION_INVALID"},
		{"superseded", &repository.StaleCalculationResultError{
			TenderID: rTender, CalculatedRevision: 7,
		}, "CALCULATION_SUPERSEDED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &stubRedistributionSvc{err: wrappedR(tc.err)}
			w := doRedistributionSave(t, svc, rulesOnlyBody())
			if w.Code != 409 {
				t.Fatalf("status = %d, want 409 (body %s)", w.Code, w.Body.String())
			}
			m := decodeProblem(t, w)
			if m["code"] != tc.want {
				t.Fatalf("code = %v, want %q", m["code"], tc.want)
			}
			// Internal diagnostics stay in the log: the response must not echo
			// the raw error chain.
			detail, _ := m["detail"].(string)
			if detail == "" || strings.Contains(detail, "SaveAuthoritative") {
				t.Fatalf("detail leaks internals or is empty: %q", detail)
			}
		})
	}
}

func doRedistributionClear(t *testing.T, svc redistributionServicer, query string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewRedistributionHandler(svc)
	req := httptest.NewRequest("DELETE", "/api/v1/redistributions?"+query, nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.CtxUser,
		&middleware.AuthUser{ID: "user-1"}))
	w := httptest.NewRecorder()
	h.Clear(w, req)
	return w
}

// Очистка снимка: пустые правила save отклоняет (RULES_EMPTY), поэтому удаление
// всех правил идёт отдельной командой DELETE.
func TestRedistributionClearHandler_DeletesSnapshot(t *testing.T) {
	svc := &stubRedistributionSvc{}
	w := doRedistributionClear(t, svc, fmt.Sprintf("tender_id=%s&markup_tactic_id=%s", rTender, rTactic))
	if w.Code != 200 {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			DeletedCount int `json:"deleted_count"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad response: %v", err)
	}
	if resp.Data.DeletedCount != 3 {
		t.Fatalf("deleted_count = %d, want 3", resp.Data.DeletedCount)
	}
	if svc.calls != 1 || svc.gotTender != rTender || svc.gotTactic != rTactic {
		t.Fatalf("service call wrong: calls=%d tender=%q tactic=%q", svc.calls, svc.gotTender, svc.gotTactic)
	}
}

func TestRedistributionClearHandler_RejectsBadIDs(t *testing.T) {
	for _, q := range []string{
		"",
		"tender_id=" + rTender,
		"tender_id=not-a-uuid&markup_tactic_id=" + rTactic,
	} {
		svc := &stubRedistributionSvc{}
		w := doRedistributionClear(t, svc, q)
		if w.Code != 400 {
			t.Fatalf("query %q: status = %d, want 400", q, w.Code)
		}
		if svc.calls != 0 {
			t.Fatalf("query %q: service must not be called", q)
		}
	}
}
