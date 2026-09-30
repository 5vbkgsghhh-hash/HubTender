package calc

import "testing"

// nil distribution — дефолт Конструктора наценок (DefaultPricingDistribution).
func TestApplyPricingDistribution_NilFallback(t *testing.T) {
	cases := []struct {
		name, boqType, matType string
		wantM, wantW           float64
	}{
		{"мат основн.: база → мат, наценка → раб", BoqMat, "основн.", 100, 50},
		{"мат вспомогат. → раб", BoqMat, "вспомогат.", 0, 150},
		{"мат-комп. → раб", BoqMatKomp, "основн.", 0, 150},
		{"суб-мат основн.: база → мат, наценка → раб", BoqSubMat, "основн.", 100, 50},
		{"суб-мат вспомогат. → раб", BoqSubMat, "вспомогат.", 0, 150},
		{"раб → раб", BoqRab, "", 0, 150},
		{"суб-раб → раб", BoqSubRab, "", 0, 150},
		{"раб-комп. → раб", BoqRabKomp, "", 0, 150},
	}
	for _, c := range cases {
		m, w := ApplyPricingDistribution(100, 150, c.boqType, c.matType, nil)
		if m != c.wantM || w != c.wantW {
			t.Errorf("%s: got m=%v w=%v, want m=%v w=%v", c.name, m, w, c.wantM, c.wantW)
		}
	}
}

// basic material: base → material, markup → material (both to material column).
func TestApplyPricingDistribution_BasicMaterial_AllMaterial(t *testing.T) {
	d := &PricingDistribution{
		BasicMaterialBaseTarget:   TargetMaterial,
		BasicMaterialMarkupTarget: TargetMaterial,
	}
	m, w := ApplyPricingDistribution(100, 150, BoqMat, "основн.", d)
	if m != 150 || w != 0 {
		t.Errorf("got m=%v w=%v, want m=150 w=0", m, w)
	}
}

// basic material: base → material, markup → work.
func TestApplyPricingDistribution_BasicMaterial_Split(t *testing.T) {
	d := &PricingDistribution{
		BasicMaterialBaseTarget:   TargetMaterial,
		BasicMaterialMarkupTarget: TargetWork,
	}
	m, w := ApplyPricingDistribution(100, 150, BoqMat, "основн.", d)
	if m != 100 || w != 50 {
		t.Errorf("got m=%v w=%v, want m=100 w=50", m, w)
	}
}

// auxiliary material (вспомогат.) uses auxiliary_material_* targets.
func TestApplyPricingDistribution_AuxiliaryMaterial(t *testing.T) {
	d := &PricingDistribution{
		BasicMaterialBaseTarget:         TargetMaterial, // shouldn't apply
		BasicMaterialMarkupTarget:       TargetMaterial,
		AuxiliaryMaterialBaseTarget:     TargetWork,
		AuxiliaryMaterialMarkupTarget:   TargetWork,
	}
	m, w := ApplyPricingDistribution(100, 150, BoqMat, "вспомогат.", d)
	if m != 0 || w != 150 {
		t.Errorf("aux mat: got m=%v w=%v, want m=0 w=150", m, w)
	}
}

// component_material: falls back to auxiliary when component_* empty.
func TestApplyPricingDistribution_ComponentMaterial_Fallback(t *testing.T) {
	d := &PricingDistribution{
		AuxiliaryMaterialBaseTarget:   TargetMaterial,
		AuxiliaryMaterialMarkupTarget: TargetMaterial,
		// ComponentMaterial* intentionally empty → fallback to auxiliary
	}
	m, w := ApplyPricingDistribution(100, 150, BoqMatKomp, "основн.", d)
	if m != 150 || w != 0 {
		t.Errorf("component fallback to aux: got m=%v w=%v, want m=150 w=0", m, w)
	}
}

// subcontract_basic_material without config: whole commercialCost → work.
func TestApplyPricingDistribution_SubcontractBasic_NoConfig(t *testing.T) {
	d := &PricingDistribution{} // no subcontract_* configured
	m, w := ApplyPricingDistribution(100, 150, BoqSubMat, "основн.", d)
	if m != 0 || w != 150 {
		t.Errorf("sub-mat fallback: got m=%v w=%v, want m=0 w=150", m, w)
	}
}

// work: base and markup split by configured targets.
func TestApplyPricingDistribution_Work(t *testing.T) {
	d := &PricingDistribution{
		WorkBaseTarget:   TargetWork,
		WorkMarkupTarget: TargetMaterial,
	}
	m, w := ApplyPricingDistribution(100, 150, BoqRab, "", d)
	if m != 50 || w != 100 {
		t.Errorf("work split: got m=%v w=%v, want m=50 w=100", m, w)
	}
}

// suф-раб uses the same work_* targets as раб.
func TestApplyPricingDistribution_SubWork_UsesWorkTargets(t *testing.T) {
	d := &PricingDistribution{
		WorkBaseTarget:   TargetMaterial,
		WorkMarkupTarget: TargetWork,
	}
	m, w := ApplyPricingDistribution(100, 150, BoqSubRab, "", d)
	if m != 100 || w != 50 {
		t.Errorf("sub-rab: got m=%v w=%v, want m=100 w=50", m, w)
	}
}

// component_work fallback to work_* when component_work_* empty.
func TestApplyPricingDistribution_ComponentWork_Fallback(t *testing.T) {
	d := &PricingDistribution{
		WorkBaseTarget:   TargetWork,
		WorkMarkupTarget: TargetWork,
	}
	m, w := ApplyPricingDistribution(100, 150, BoqRabKomp, "", d)
	if m != 0 || w != 150 {
		t.Errorf("component work fallback: got m=%v w=%v, want m=0 w=150", m, w)
	}
}

// Unknown type (empty string) → warn path, all to work.
func TestGetMaterialSubtype_Unknown(t *testing.T) {
	sub := GetMaterialSubtype("нечто", "")
	if sub != "" {
		t.Errorf("unknown type: got %v, want empty", sub)
	}
}
