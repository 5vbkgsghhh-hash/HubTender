// Модель строк ТОЛЬКО для вкладки «Таблица» (и Excel-экспорта). Строится
// трансформацией готовых строк buildIndicatorRows — их же (18 строк, исходная
// нумерация) продолжают потреблять «Графики», поэтому графики не затрагиваются.
//
// Отличия от базовых строк:
//   • «Субподряд» → «Субподряд работы» + «Субподряд материалы»
//   • «Работы + Материалы СУ-10» → «Работы СУ-10» + «Материалы СУ-10»
//   • «Рост стоимости» → 4 строки (раб/мат СУ-10, раб/мат субподряд)
//   • строка «В том числе НДС» убрана
//   • после ИТОГО добавлены «Работы» и «Материалы»
//   • у материальных строк — тултип осн/вспом (как у строк наценок)
//   • каждой строке проставлен calc_key (роль) и coeff_pct — для Excel-формул
//   • строки с нулевым итогом убраны (кроме ИТОГО), нумерация сквозная
import type { DirectCostTotals, MarkupCoefficients, FinancialCalcResult, IndicatorRow } from '../types';

const fmt = (n: number) => n.toLocaleString('ru-RU', { maximumFractionDigits: 2 });
const pctStr = (v: number) => (v > 0 ? `${parseFloat(v.toFixed(5))}%` : '');

/**
 * Тултип состава материалов осн/вспом — как у строк наценок (многострочный).
 * Значения уже с учётом НДС-множителя, чтобы сумма осн+вспом сходилась со строкой.
 */
const matTooltip = (label: string, osn: number, vsp: number, total: number): string =>
  `${label}:\nОсновные: ${fmt(osn)}\nВспомогательные: ${fmt(vsp)}\n= ${fmt(total)} руб.`;

export const buildTableRows = (
  baseRows: IndicatorRow[],
  calc: FinancialCalcResult,
  totals: DirectCostTotals,
  coeffs: MarkupCoefficients,
  areaSp: number,
  areaClient: number,
): IndicatorRow[] => {
  const byNum = new Map(baseRows.map((r) => [r.row_number, r]));
  const vatMul = coeffs.isVatInConstructor && coeffs.vatCoeff > 0 ? 1 + coeffs.vatCoeff / 100 : 1;

  const spPer = (v: number) => (areaSp > 0 ? v / areaSp : 0);
  const clPer = (v: number) => (areaClient > 0 ? v / areaClient : 0);

  // Денежная строка «с нуля» (для расщеплённых и итоговых строк).
  const mk = (o: {
    name: string;
    total: number;
    coefficient?: string;
    coeff_pct?: number;
    calc_key?: string;
    tooltip?: string;
    is_indented?: boolean;
    is_total?: boolean;
    is_yellow?: boolean;
  }): IndicatorRow => ({
    key: '',
    row_number: 0,
    indicator_name: o.name,
    coefficient: o.coefficient ?? '',
    sp_cost: spPer(o.total),
    customer_cost: clPer(o.total),
    total_cost: o.total,
    tooltip: o.tooltip,
    is_indented: o.is_indented,
    calc_key: o.calc_key,
    coeff_pct: o.coeff_pct,
    is_total: o.is_total,
    is_yellow: o.is_yellow,
  });

  // Сохранённая строка из base (значения уже с НДС-множителем и тултипами).
  const keep = (num: number, over: Partial<IndicatorRow>): IndicatorRow => ({
    ...(byNum.get(num) as IndicatorRow),
    ...over,
  });

  // Расщеплённые значения (× НДС-множитель — как в base для строк 1-16).
  const subWorks = totals.subcontractWorks * vatMul;
  const subMat = totals.subcontractMaterials * vatMul;
  const workSU10 = totals.works * vatMul;
  const matSU10 = totals.materials * vatMul;
  const gWork = calc.worksCostGrowthAmount * vatMul;
  const gMat = calc.materialCostGrowthAmount * vatMul;
  const gSubWork = calc.subcontractWorksCostGrowthAmount * vatMul;
  const gSubMat = calc.subcontractMaterialsCostGrowthAmount * vatMul;

  // Составы осн/вспом (× НДС-множитель).
  const subMatOsn = totals.subcontractMaterialsBasic * vatMul;
  const subMatVsp = totals.subcontractMaterialsAux * vatMul;
  const matOsn = totals.materialsBasic * vatMul;
  const matVsp = totals.materialsAux * vatMul;

  // Рост материалов осн/вспом = база(осн/вспом) × процент × НДС-множитель.
  const mg = coeffs.materialCostGrowth / 100;
  const gMatOsn = totals.materialsBasic * mg * vatMul;
  const gMatVsp = totals.materialsAux * mg * vatMul;
  const smg = coeffs.subcontractMaterialsCostGrowth / 100;
  const gSubMatBaseOsn = totals.subcontractMaterialsForGrowthBasic * vatMul;
  const gSubMatBaseVsp = totals.subcontractMaterialsForGrowthAux * vatMul;
  const gSubMatOsn = totals.subcontractMaterialsForGrowthBasic * smg * vatMul;
  const gSubMatVsp = totals.subcontractMaterialsForGrowthAux * smg * vatMul;

  // Базы роста (× НДС-множитель) для тултипов.
  const gWorkBase = calc.worksWithMarkup * vatMul;
  const gSubWorkBase = totals.subcontractWorksForGrowth * vatMul;

  // ИТОГО (grandTotal, уже НДС-инклюзив; НДС-множитель к нему не применялся).
  const grandTotal = byNum.get(17)?.total_cost ?? 0;
  // «Материалы» = базовая стоимость основных материалов (мат осн + суб-мат осн),
  // без наценок и без НДС — совпадает с колонкой «Итого материалы» на Коммерции.
  const materialsRow = totals.materialsBasic + totals.subcontractMaterialsBasic;
  const worksRow = grandTotal - materialsRow;

  const rows: IndicatorRow[] = [
    keep(1, { calc_key: 'direct_costs' }),

    mk({ name: 'Субподряд работы', total: subWorks, is_indented: true, calc_key: 'subcontract_work' }),
    mk({
      name: 'Субподряд материалы', total: subMat, is_indented: true, calc_key: 'subcontract_mat',
      tooltip: matTooltip('Субподряд материалы', subMatOsn, subMatVsp, subMat),
    }),
    mk({ name: 'Работы СУ-10', total: workSU10, is_indented: true, calc_key: 'work_su10' }),
    mk({
      name: 'Материалы СУ-10', total: matSU10, is_indented: true, calc_key: 'materials_su10',
      tooltip: matTooltip('Материалы СУ-10', matOsn, matVsp, matSU10),
    }),

    keep(4, { is_indented: true, calc_key: 'reserve' }),
    keep(5, { is_indented: true, calc_key: 'mechanization', coeff_pct: coeffs.mechanizationCoeff }),
    keep(6, { is_indented: true, calc_key: 'mvp', coeff_pct: coeffs.mvpGsmCoeff }),
    keep(7, { is_indented: true, calc_key: 'warranty', coeff_pct: coeffs.warrantyCoeff }),
    keep(8, { indicator_name: '0,6 к (Раб СУ-10+СМ)', calc_key: 'coef16', coeff_pct: coeffs.coefficient06 }),

    mk({
      name: 'Работы СУ-10 рост', total: gWork, calc_key: 'growth_work',
      coefficient: pctStr(coeffs.worksCostGrowth), coeff_pct: coeffs.worksCostGrowth,
      tooltip: `Формула: (Работы + 1,6к + МБП + СМ) × ${coeffs.worksCostGrowth}%\n` +
        `База: ${fmt(gWorkBase)} × ${coeffs.worksCostGrowth}% = ${fmt(gWork)} руб.`,
    }),
    mk({
      name: 'Материалы СУ-10 рост', total: gMat, calc_key: 'growth_mat',
      coefficient: pctStr(coeffs.materialCostGrowth), coeff_pct: coeffs.materialCostGrowth,
      tooltip: `Формула: Материалы СУ-10 × ${coeffs.materialCostGrowth}%\n` +
        `Основные: ${fmt(matOsn)} × ${coeffs.materialCostGrowth}% = ${fmt(gMatOsn)}\n` +
        `Вспомогательные: ${fmt(matVsp)} × ${coeffs.materialCostGrowth}% = ${fmt(gMatVsp)}\n` +
        `= ${fmt(gMat)} руб.`,
    }),
    mk({
      name: 'Работы субподряд рост', total: gSubWork, calc_key: 'growth_sub_work',
      coefficient: pctStr(coeffs.subcontractWorksCostGrowth), coeff_pct: coeffs.subcontractWorksCostGrowth,
      tooltip: `Формула: Субподряд работы (база роста) × ${coeffs.subcontractWorksCostGrowth}%\n` +
        `База: ${fmt(gSubWorkBase)} × ${coeffs.subcontractWorksCostGrowth}% = ${fmt(gSubWork)} руб.`,
    }),
    mk({
      name: 'Материалы субподряд рост', total: gSubMat, calc_key: 'growth_sub_mat',
      coefficient: pctStr(coeffs.subcontractMaterialsCostGrowth), coeff_pct: coeffs.subcontractMaterialsCostGrowth,
      tooltip: `Формула: Субподряд материалы (база роста) × ${coeffs.subcontractMaterialsCostGrowth}%\n` +
        `Основные: ${fmt(gSubMatBaseOsn)} × ${coeffs.subcontractMaterialsCostGrowth}% = ${fmt(gSubMatOsn)}\n` +
        `Вспомогательные: ${fmt(gSubMatBaseVsp)} × ${coeffs.subcontractMaterialsCostGrowth}% = ${fmt(gSubMatVsp)}\n` +
        `= ${fmt(gSubMat)} руб.`,
    }),

    keep(10, { calc_key: 'unforeseeable', coeff_pct: coeffs.unforeseeableCoeff }),
    keep(11, { calc_key: 'ooz', coeff_pct: coeffs.overheadOwnForcesCoeff }),
    keep(12, { calc_key: 'ooz_sub', coeff_pct: coeffs.overheadSubcontractCoeff }),
    keep(13, { indicator_name: 'ОФЗ (без субподряда)', calc_key: 'ofz', coeff_pct: coeffs.generalCostsCoeff }),
    keep(14, { calc_key: 'profit', coeff_pct: coeffs.profitOwnForcesCoeff }),
    keep(15, { calc_key: 'profit_sub', coeff_pct: coeffs.profitSubcontractCoeff }),
    keep(16, { indicator_name: 'Страхование от судимостей, 3% от продаваемых помещений', calc_key: 'insurance' }),
    keep(17, { indicator_name: 'ИТОГО стоимость по КП, в т.ч.', calc_key: 'total' }),

    mk({ name: 'Работы', total: worksRow, calc_key: 'row_works' }),
    mk({
      name: 'Материалы', total: materialsRow, calc_key: 'row_materials',
      tooltip: `Базовая стоимость основных материалов (прямые затраты):\n` +
        `Материалы осн.: ${fmt(totals.materialsBasic)}\n` +
        `+ Субматериалы осн.: ${fmt(totals.subcontractMaterialsBasic)}\n` +
        `= ${fmt(materialsRow)} руб.`,
    }),
  ];

  // Строки с нулевым итогом не показываем ни в таблице, ни в Excel; ИТОГО — всегда.
  // «Ноль» — как на экране: суммы округлены до рубля, поэтому |итог| < 0,5
  // (заодно отсекает хвосты float после снижения/обнуления). NaN не прячем.
  const visible = rows.filter((r) => r.is_total || !(Math.abs(r.total_cost ?? 0) < 0.5));

  // Последовательная нумерация «№ п/п» (графики её не используют).
  return visible.map((r, i) => ({ ...r, row_number: i + 1, key: String(i + 1) }));
};
