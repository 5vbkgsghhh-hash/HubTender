package changeimpact

import (
	"fmt"
	"sort"
	"strings"

	"github.com/su10/hubtender/backend/internal/calc"
)

// changedFields — сравнение входов matched-строки (§5). Финансовые totals
// сравниваются с денежной tolerance; source-метаданные помечаются
// evidence_only (доказательная информация, не财 причина).
func changedFields(cur, base *Item, curParent, baseParent map[string]string) []FieldChange {
	var out []FieldChange
	addF := func(field, label string, oldP, newP *float64) {
		o, n := derefFStr(oldP), derefFStr(newP)
		if o != n {
			out = append(out, FieldChange{Field: field, Label: label, OldValue: o, NewValue: n})
		}
	}
	addS := func(field, label, o, n string, evidence bool) {
		if o != n {
			out = append(out, FieldChange{Field: field, Label: label, OldValue: emptyDash(o), NewValue: emptyDash(n), EvidenceOnly: evidence})
		}
	}

	addF("quantity", "Количество", base.Quantity, cur.Quantity)
	addF("unit_rate", "Цена за единицу", base.UnitRate, cur.UnitRate)
	addS("currency_type", "Валюта", base.CurrencyType, cur.CurrencyType, false)
	addS("unit_code", "Единица измерения", derefS(base.UnitCode), derefS(cur.UnitCode), false)
	addF("consumption_coefficient", "Коэффициент расхода", base.ConsumptionCoef, cur.ConsumptionCoef)
	addF("conversion_coefficient", "Коэффициент перевода", base.ConversionCoef, cur.ConversionCoef)
	addF("base_quantity", "Базовое количество", base.BaseQuantity, cur.BaseQuantity)
	addS("delivery_price_type", "Тип доставки", derefS(base.DeliveryType), derefS(cur.DeliveryType), false)
	addF("delivery_amount", "Сумма доставки", base.DeliveryAmount, cur.DeliveryAmount)
	addS("material_type", "Тип материала", derefS(base.MaterialType), derefS(cur.MaterialType), false)
	addS("name_id", "Номенклатура", derefS(base.NameID), derefS(cur.NameID), false)
	addS("detail_cost_category_id", "Затрата на строительство", derefS(base.DetailCategoryID), derefS(cur.DetailCategoryID), false)
	addS("parent", "Родительская работа", baseParent[base.ID], curParent[cur.ID], false)
	addS("description", "Описание", derefS(base.Description), derefS(cur.Description), false)
	// Source-метаданные: изменение доказательной информации (§5).
	addS("quote_link", "Источник цены", derefS(base.QuoteLink), derefS(cur.QuoteLink), true)
	addS("quote_price_date", "Дата цены", derefS(base.QuotePriceDate), derefS(cur.QuotePriceDate), true)
	addS("quote_valid_until", "Срок действия цены", derefS(base.QuoteValidUntil), derefS(cur.QuoteValidUntil), true)

	// Authoritative money — через exact rat с tolerance.
	addMoney := func(field, label, oldT, newT string) {
		o, n := parseMoneyRat(oldT), parseMoneyRat(newT)
		if !ratWithinTolerance(ratSub(n, o)) {
			out = append(out, FieldChange{
				Field: field, Label: label,
				OldValue: fmt.Sprintf("%.2f", ratToFloat2(o)),
				NewValue: fmt.Sprintf("%.2f", ratToFloat2(n)),
			})
		}
	}
	addMoney("total_amount", "Прямая сумма", base.TotalAmountText, cur.TotalAmountText)
	addMoney("total_commercial_material_cost", "Коммерческие материалы", base.CommercialMaterialText, cur.CommercialMaterialText)
	addMoney("total_commercial_work_cost", "Коммерческие работы", base.CommercialWorkText, cur.CommercialWorkText)
	return out
}

func derefFStr(p *float64) string {
	if p == nil {
		return "—"
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", *p), "0"), ".")
}

func emptyDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// ─── Configuration diff (§8): контекст, НЕ денежная причина ──────────────────

func diffConfiguration(cur, base *TenderState) []ConfigChange {
	var out []ConfigChange
	add := func(code, label, oldV, newV, nav string) {
		out = append(out, ConfigChange{
			Code: code, Label: label,
			OldValue: emptyDash(oldV), NewValue: emptyDash(newV),
			Changed: oldV != newV, Navigation: nav,
		})
	}
	rate := func(p *float64) string {
		if p == nil {
			return ""
		}
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.4f", *p), "0"), ".")
	}
	add("USD_RATE", "Курс USD", rate(base.USDRate), rate(cur.USDRate), "tender_currency")
	add("EUR_RATE", "Курс EUR", rate(base.EURRate), rate(cur.EURRate), "tender_currency")
	add("CNY_RATE", "Курс CNY", rate(base.CNYRate), rate(cur.CNYRate), "tender_currency")
	tacticVal := func(t *TenderState) string {
		if t.TacticLabel != "" {
			return t.TacticLabel
		}
		return derefS(t.TacticID)
	}
	add("MARKUP_TACTIC", "Тактика наценок", tacticVal(base), tacticVal(cur), "markup")
	add("APPLY_SUBCONTRACT_WORKS", "Рост суб-работ применяется", boolRu(base.ApplySubW), boolRu(cur.ApplySubW), "markup")
	add("APPLY_SUBCONTRACT_MATERIALS", "Рост суб-материалов применяется", boolRu(base.ApplySubM), boolRu(cur.ApplySubM), "markup")

	// Проценты наценок: сравнение по label (map детерминированно через sort).
	baseP := percentageMap(base.Percentages)
	curP := percentageMap(cur.Percentages)
	for _, label := range sortedStringKeys2(baseP, curP) {
		o, n := baseP[label], curP[label]
		if o != n {
			add("MARKUP_PERCENTAGE:"+label, "Процент: "+label, o, n, "markup")
		}
	}

	distStr := func(d *Distribution) string {
		if d == nil {
			// Нет записи → расчёт идёт по дефолту Конструктора наценок.
			def := calc.DefaultPricingDistribution
			d = &Distribution{
				BasicMaterialBase: string(def.BasicMaterialBaseTarget), BasicMaterialMarkup: string(def.BasicMaterialMarkupTarget),
				AuxiliaryMaterialBase: string(def.AuxiliaryMaterialBaseTarget), AuxiliaryMaterialMark: string(def.AuxiliaryMaterialMarkupTarget),
				WorkBase: string(def.WorkBaseTarget), WorkMarkup: string(def.WorkMarkupTarget),
			}
		}
		return strings.Join([]string{
			d.BasicMaterialBase, d.BasicMaterialMarkup,
			d.AuxiliaryMaterialBase, d.AuxiliaryMaterialMark,
			d.WorkBase, d.WorkMarkup,
		}, "/")
	}
	add("PRICING_DISTRIBUTION", "Распределение стоимости", distStr(base.Distribution), distStr(cur.Distribution), "distribution")
	add("SUBCONTRACT_EXCLUSIONS", "Исключения роста субподряда",
		strings.Join(base.Exclusions, "; "), strings.Join(cur.Exclusions, "; "), "exclusions")

	insStr := func(ins *Insurance) string {
		if !ins.Present {
			return ""
		}
		return fmt.Sprintf("судебный %s%% · итоговый %s%% · кв. %s×%s · паркинг %s×%s · кладовые %s×%s",
			ins.JudicialPct, ins.TotalPct, ins.AptPriceM2, ins.AptArea,
			ins.ParkingPriceM2, ins.ParkingA, ins.StoragePriceM2, ins.StorageA)
	}
	add("INSURANCE", "Страхование", insStr(&base.Insurance), insStr(&cur.Insurance), "insurance")

	// Только изменившиеся + детерминированный порядок объявления.
	changed := make([]ConfigChange, 0, len(out))
	for _, c := range out {
		if c.Changed {
			changed = append(changed, c)
		}
	}
	return changed
}

func boolRu(v bool) string {
	if v {
		return "да"
	}
	return "нет"
}

func percentageMap(ps []Percentage) map[string]string {
	out := make(map[string]string, len(ps))
	for _, p := range ps {
		out[p.Label] = p.Value
	}
	return out
}

func sortedStringKeys2(a, b map[string]string) []string {
	seen := map[string]bool{}
	var keys []string
	for k := range a {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for k := range b {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}
