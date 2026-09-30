package calc

import (
	"math"
	"testing"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func numStep(mult float64) SequenceStep {
	return SequenceStep{BaseIndex: -1, Action1: "multiply", Operand1Type: "number", Operand1Key: mult}
}

// Material, no distribution → дефолт Конструктора: база → материалы, наценка → работы.
func TestCalculateBoqItemCost_MaterialNilDistribution(t *testing.T) {
	seqs := map[string][]SequenceStep{BoqMat: {numStep(1.25)}}
	cache := map[string]float64{}
	res, ok := CalculateBoqItemCost(
		BoqItemForCost{BoqItemType: BoqMat, TotalAmount: 100},
		seqs, nil, map[string]float64{}, nil, nil, cache)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if !approx(res.MaterialCost, 100) || !approx(res.WorkCost, 25) {
		t.Errorf("got mat=%v work=%v, want 100/25", res.MaterialCost, res.WorkCost)
	}
	if !approx(res.MarkupCoefficient, 1.25) {
		t.Errorf("coef got %v, want 1.25", res.MarkupCoefficient)
	}
}

// Material with distribution: base → material, markup → work.
func TestCalculateBoqItemCost_DistributionSplit(t *testing.T) {
	seqs := map[string][]SequenceStep{BoqMat: {numStep(1.25)}}
	dist := &PricingDistribution{BasicMaterialBaseTarget: TargetMaterial, BasicMaterialMarkupTarget: TargetWork}
	res, ok := CalculateBoqItemCost(
		BoqItemForCost{BoqItemType: BoqMat, MaterialType: "основн.", TotalAmount: 100},
		seqs, nil, map[string]float64{}, dist, nil, map[string]float64{})
	if !ok || !approx(res.MaterialCost, 100) || !approx(res.WorkCost, 25) {
		t.Errorf("got mat=%v work=%v, want 100/25", res.MaterialCost, res.WorkCost)
	}
}

// VAT removed via nds_22 param, then re-applied to each column.
func TestCalculateBoqItemCost_VatFromParam(t *testing.T) {
	seqs := map[string][]SequenceStep{BoqRab: {
		numStep(1.2),
		{BaseIndex: 0, Action1: "multiply", Operand1Type: "markup", Operand1Key: "nds_22", Operand1MultiplyFormat: "addOne"},
	}}
	res, ok := CalculateBoqItemCost(
		BoqItemForCost{BoqItemType: BoqRab, TotalAmount: 100},
		seqs, nil, map[string]float64{"nds_22": 22}, nil, nil, map[string]float64{})
	// coeff (без НДС) = 1.2 → 120; work column; ×1.22 = 146.4
	if !ok || !approx(res.MaterialCost, 0) || !approx(res.WorkCost, 120*1.22) {
		t.Errorf("got mat=%v work=%v, want 0/%v", res.MaterialCost, res.WorkCost, 120*1.22)
	}
}

// VAT recognised by step name + 1.xx numeric multiplier.
func TestCalculateBoqItemCost_VatFromName(t *testing.T) {
	seqs := map[string][]SequenceStep{BoqRab: {
		numStep(1.2),
		{Name: "НДС 20%", BaseIndex: 0, Action1: "multiply", Operand1Type: "number", Operand1Key: 1.2},
	}}
	res, ok := CalculateBoqItemCost(
		BoqItemForCost{BoqItemType: BoqRab, TotalAmount: 100},
		seqs, nil, map[string]float64{}, nil, nil, map[string]float64{})
	if !ok || !approx(res.WorkCost, 120*1.2) {
		t.Errorf("got work=%v, want %v", res.WorkCost, 120*1.2)
	}
}

// Excluded суб-раб drops the growth step and re-points the dependent baseIndex to base.
func TestCalculateBoqItemCost_SubcontractExclusion(t *testing.T) {
	seqs := map[string][]SequenceStep{BoqSubRab: {
		numStep(1.1), // step0
		{BaseIndex: 0, Action1: "multiply", Operand1Type: "markup", Operand1Key: "subcontract_works_cost_growth", Operand1MultiplyFormat: "addOne"}, // step1 (removed)
		{BaseIndex: 1, Action1: "multiply", Operand1Type: "number", Operand1Key: 1.05}, // step2 → after removal references base
	}}
	params := map[string]float64{"subcontract_works_cost_growth": 10}
	ex := &SubcontractExclusions{Works: map[string]bool{"cat1": true}, Materials: map[string]bool{}}

	excluded, ok := CalculateBoqItemCost(
		BoqItemForCost{BoqItemType: BoqSubRab, DetailCostCategoryID: "cat1", TotalAmount: 100},
		seqs, nil, params, nil, ex, map[string]float64{})
	// filtered: [×1.1, ×1.05] both off base(1) → last step = 1×1.05 = 1.05 → coeff 1.05 → 105
	if !ok || !approx(excluded.WorkCost, 105) {
		t.Errorf("excluded work got %v, want 105", excluded.WorkCost)
	}

	// Sanity: a non-excluded item keeps the growth step → different result.
	notExcluded, _ := CalculateBoqItemCost(
		BoqItemForCost{BoqItemType: BoqSubRab, DetailCostCategoryID: "other", TotalAmount: 100},
		seqs, nil, params, nil, ex, map[string]float64{})
	if approx(notExcluded.WorkCost, 105) {
		t.Errorf("non-excluded should differ from 105, got %v", notExcluded.WorkCost)
	}
}

// суб-мат основн. with nil distribution: base → material, markup → work.
func TestCalculateBoqItemCost_SubMaterialNilDistribution(t *testing.T) {
	seqs := map[string][]SequenceStep{BoqSubMat: {numStep(1.3)}}
	res, ok := CalculateBoqItemCost(
		BoqItemForCost{BoqItemType: BoqSubMat, MaterialType: "основн.", TotalAmount: 100},
		seqs, nil, map[string]float64{}, nil, nil, map[string]float64{})
	if !ok || !approx(res.MaterialCost, 100) || !approx(res.WorkCost, 30) {
		t.Errorf("got mat=%v work=%v, want 100/30", res.MaterialCost, res.WorkCost)
	}
}

// No sequence for the item type → ok=false (mirrors TS null).
func TestCalculateBoqItemCost_NoSequence(t *testing.T) {
	seqs := map[string][]SequenceStep{BoqRab: {numStep(1.2)}}
	_, ok := CalculateBoqItemCost(
		BoqItemForCost{BoqItemType: BoqMat, TotalAmount: 100},
		seqs, nil, map[string]float64{}, nil, nil, map[string]float64{})
	if ok {
		t.Error("expected ok=false for missing sequence")
	}
}

// Shared coeff cache: two items of the same type compute the coefficient once.
func TestCalculateBoqItemCost_CoeffCacheReuse(t *testing.T) {
	seqs := map[string][]SequenceStep{BoqMat: {numStep(1.25)}}
	cache := map[string]float64{}
	CalculateBoqItemCost(BoqItemForCost{BoqItemType: BoqMat, TotalAmount: 100}, seqs, nil, map[string]float64{}, nil, nil, cache)
	CalculateBoqItemCost(BoqItemForCost{BoqItemType: BoqMat, TotalAmount: 200}, seqs, nil, map[string]float64{}, nil, nil, cache)
	if len(cache) != 1 {
		t.Errorf("expected 1 cached coefficient, got %d", len(cache))
	}
}
