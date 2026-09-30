// Port of applyPricingDistribution + getMaterialType from
// src/services/markupTactic/calculation.ts. Splits a BOQ item's commercialCost
// between the material_cost and work_cost output columns based on per-tender
// tender_pricing_distribution config. Stay 1:1 with TS; any drift = cutover blocker.
package calc

// MaterialSubtype narrows boq_item_type + material_type into the 7 cases the
// distribution table uses as keys.
type MaterialSubtype string

const (
	MatBasic                MaterialSubtype = "basic"                // 'мат'            + !вспомогат.
	MatAuxiliary            MaterialSubtype = "auxiliary"            // 'мат' + вспомогат., and 'мат-комп.' + вспомогат.
	MatComponentMaterial    MaterialSubtype = "component_material"   // 'мат-комп.'      + !вспомогат.
	MatSubcontractBasic     MaterialSubtype = "subcontract_basic"    // 'суб-мат'        + !вспомогат.
	MatSubcontractAuxiliary MaterialSubtype = "subcontract_auxiliary" // 'суб-мат'        + вспомогат.
	MatWork                 MaterialSubtype = "work"                 // 'раб', 'суб-раб'
	MatComponentWork        MaterialSubtype = "component_work"       // 'раб-комп.'
)

const materialTypeAuxiliary = "вспомогат."

// GetMaterialSubtype narrows a BOQ item into one of the 7 distribution cases.
// Returns empty string when the item type is unknown (caller should fall back
// to all-work treatment — matches TS `console.warn` path).
func GetMaterialSubtype(boqItemType string, materialType string) MaterialSubtype {
	isAuxiliary := materialType == materialTypeAuxiliary
	switch boqItemType {
	case BoqMat:
		if isAuxiliary {
			return MatAuxiliary
		}
		return MatBasic
	case BoqMatKomp:
		if isAuxiliary {
			return MatAuxiliary
		}
		return MatComponentMaterial
	case BoqSubMat:
		if isAuxiliary {
			return MatSubcontractAuxiliary
		}
		return MatSubcontractBasic
	case BoqRab, BoqSubRab:
		return MatWork
	case BoqRabKomp:
		return MatComponentWork
	}
	return ""
}

// DistTarget selects which output column a cost flows to.
type DistTarget string

const (
	TargetMaterial DistTarget = "material"
	TargetWork     DistTarget = "work"
)

// PricingDistribution mirrors one row of public.tender_pricing_distribution.
// Every *base_target / *markup_target is either "material" or "work". Empty
// string is treated as "not configured" → fallback per TS logic.
type PricingDistribution struct {
	BasicMaterialBaseTarget   DistTarget
	BasicMaterialMarkupTarget DistTarget
	AuxiliaryMaterialBaseTarget   DistTarget
	AuxiliaryMaterialMarkupTarget DistTarget
	ComponentMaterialBaseTarget   DistTarget
	ComponentMaterialMarkupTarget DistTarget
	SubcontractBasicMaterialBaseTarget   DistTarget
	SubcontractBasicMaterialMarkupTarget DistTarget
	SubcontractAuxiliaryMaterialBaseTarget   DistTarget
	SubcontractAuxiliaryMaterialMarkupTarget DistTarget
	WorkBaseTarget           DistTarget
	WorkMarkupTarget         DistTarget
	ComponentWorkBaseTarget   DistTarget
	ComponentWorkMarkupTarget DistTarget
}

// DefaultPricingDistribution — распределение, которое Конструктор наценок
// (вкладка «Ценообразование») показывает для тендера без сохранённой записи.
// Применяется, когда tender_pricing_distribution для тендера отсутствует.
// Держать 1:1 с DEFAULT_PRICING_DISTRIBUTION в calculation.ts.
var DefaultPricingDistribution = PricingDistribution{
	BasicMaterialBaseTarget:                  TargetMaterial,
	BasicMaterialMarkupTarget:                TargetWork,
	AuxiliaryMaterialBaseTarget:              TargetWork,
	AuxiliaryMaterialMarkupTarget:            TargetWork,
	ComponentMaterialBaseTarget:              TargetWork,
	ComponentMaterialMarkupTarget:            TargetWork,
	SubcontractBasicMaterialBaseTarget:       TargetMaterial,
	SubcontractBasicMaterialMarkupTarget:     TargetWork,
	SubcontractAuxiliaryMaterialBaseTarget:   TargetWork,
	SubcontractAuxiliaryMaterialMarkupTarget: TargetWork,
	WorkBaseTarget:                           TargetWork,
	WorkMarkupTarget:                         TargetWork,
	ComponentWorkBaseTarget:                  TargetWork,
	ComponentWorkMarkupTarget:                TargetWork,
}

// ApplyPricingDistribution splits a commercialCost between material_cost and
// work_cost columns. baseAmount is the pre-markup cost; markup = commercial - base.
//
// If distribution == nil DefaultPricingDistribution is applied.
//
// Composite (комп.) and subcontract-special cases use optional fields with
// fallbacks per the TS switch.
func ApplyPricingDistribution(
	baseAmount, commercialCost float64,
	boqItemType, materialType string,
	distribution *PricingDistribution,
) (materialCost, workCost float64) {
	if distribution == nil {
		distribution = &DefaultPricingDistribution
	}

	markup := commercialCost - baseAmount
	subtype := GetMaterialSubtype(boqItemType, materialType)
	if subtype == "" {
		// TS console.warn path — treat as all-work.
		return 0, commercialCost
	}

	// Helper to emit (baseTarget, markupTarget) into the right columns.
	apply := func(baseTgt, markupTgt DistTarget) {
		if baseTgt == TargetMaterial {
			materialCost += baseAmount
		} else if baseTgt == TargetWork {
			workCost += baseAmount
		}
		if markupTgt == TargetMaterial {
			materialCost += markup
		} else if markupTgt == TargetWork {
			workCost += markup
		}
	}

	switch subtype {
	case MatBasic:
		apply(distribution.BasicMaterialBaseTarget, distribution.BasicMaterialMarkupTarget)
	case MatAuxiliary:
		apply(distribution.AuxiliaryMaterialBaseTarget, distribution.AuxiliaryMaterialMarkupTarget)
	case MatComponentMaterial:
		// Fall back to auxiliary if component fields empty.
		if distribution.ComponentMaterialBaseTarget != "" && distribution.ComponentMaterialMarkupTarget != "" {
			apply(distribution.ComponentMaterialBaseTarget, distribution.ComponentMaterialMarkupTarget)
		} else {
			apply(distribution.AuxiliaryMaterialBaseTarget, distribution.AuxiliaryMaterialMarkupTarget)
		}
	case MatSubcontractBasic:
		if distribution.SubcontractBasicMaterialBaseTarget != "" && distribution.SubcontractBasicMaterialMarkupTarget != "" {
			apply(distribution.SubcontractBasicMaterialBaseTarget, distribution.SubcontractBasicMaterialMarkupTarget)
		} else {
			// TS fallback: everything → work column.
			workCost = commercialCost
		}
	case MatSubcontractAuxiliary:
		if distribution.SubcontractAuxiliaryMaterialBaseTarget != "" && distribution.SubcontractAuxiliaryMaterialMarkupTarget != "" {
			apply(distribution.SubcontractAuxiliaryMaterialBaseTarget, distribution.SubcontractAuxiliaryMaterialMarkupTarget)
		} else {
			workCost = commercialCost
		}
	case MatWork:
		apply(distribution.WorkBaseTarget, distribution.WorkMarkupTarget)
	case MatComponentWork:
		if distribution.ComponentWorkBaseTarget != "" && distribution.ComponentWorkMarkupTarget != "" {
			apply(distribution.ComponentWorkBaseTarget, distribution.ComponentWorkMarkupTarget)
		} else {
			apply(distribution.WorkBaseTarget, distribution.WorkMarkupTarget)
		}
	}

	return materialCost, workCost
}
