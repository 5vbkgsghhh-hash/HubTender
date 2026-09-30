// UI preview only. Authoritative calculation is performed by backend/internal/calc
// (calc.CalculateBoqItemCost + ApplyPricingDistribution). The commercial
// materialisation write path was removed; the Go BFF recomputes on the server.
// See docs/CALCULATION_SOURCE_OF_TRUTH.md.
/**
 * Логика расчета наценок для элементов BOQ
 */

import type { BoqItem, MarkupStep } from '../../lib/types';
import {
  getTenderPricingDistribution,
  listSubcontractGrowthExclusionsForTender,
} from '../../lib/api/markup';
import {
  calculateMarkupResult,
  type CalculationContext
} from '../../utils/markupCalculator';

/**
 * Результат применения тактики
 */
export interface TacticApplicationResult {
  success: boolean;
  updatedCount?: number;
  errors?: string[];
  details?: {
    itemId: string;
    commercialCost: number;
    markupCoefficient: number;
    errors?: string[];
  }[];
}

/**
 * Настройки ценообразования для тендера
 */
export interface PricingDistribution {
  basic_material_base_target: 'material' | 'work';
  basic_material_markup_target: 'material' | 'work';
  auxiliary_material_base_target: 'material' | 'work';
  auxiliary_material_markup_target: 'material' | 'work';
  component_material_base_target?: 'material' | 'work';
  component_material_markup_target?: 'material' | 'work';
  subcontract_basic_material_base_target?: 'material' | 'work';
  subcontract_basic_material_markup_target?: 'material' | 'work';
  subcontract_auxiliary_material_base_target?: 'material' | 'work';
  subcontract_auxiliary_material_markup_target?: 'material' | 'work';
  work_base_target: 'material' | 'work';
  work_markup_target: 'material' | 'work';
  component_work_base_target?: 'material' | 'work';
  component_work_markup_target?: 'material' | 'work';
}

/**
 * Распределение по умолчанию — то, что Конструктор наценок (вкладка
 * «Ценообразование») показывает для тендера без сохранённой записи.
 * Держать 1:1 с calc.DefaultPricingDistribution на бэкенде.
 */
export const DEFAULT_PRICING_DISTRIBUTION: Readonly<Required<PricingDistribution>> = {
  basic_material_base_target: 'material',
  basic_material_markup_target: 'work',
  auxiliary_material_base_target: 'work',
  auxiliary_material_markup_target: 'work',
  component_material_base_target: 'work',
  component_material_markup_target: 'work',
  subcontract_basic_material_base_target: 'material',
  subcontract_basic_material_markup_target: 'work',
  subcontract_auxiliary_material_base_target: 'work',
  subcontract_auxiliary_material_markup_target: 'work',
  work_base_target: 'work',
  work_markup_target: 'work',
  component_work_base_target: 'work',
  component_work_markup_target: 'work',
};

/**
 * Загружает настройки ценообразования для тендера
 */
export async function loadPricingDistribution(tenderId: string): Promise<PricingDistribution | null> {
  try {
    const data = await getTenderPricingDistribution(tenderId);
    if (!data) {
      console.warn('⚠️ Настройки ценообразования не найдены, используются defaults');
      return null;
    }
    return data as unknown as PricingDistribution;
  } catch {
    console.warn('⚠️ Настройки ценообразования не найдены, используются defaults');
    return null;
  }
}

/**
 * Определяет тип материала на основе boq_item_type и material_type
 */
function getMaterialType(
  boqItemType: string,
  materialType?: string | null
): 'basic' | 'auxiliary' | 'component_material' | 'subcontract_basic' | 'subcontract_auxiliary' | 'work' | 'component_work' | null {
  // Для материалов проверяем material_type (основн./вспомогат.)
  if (boqItemType === 'мат') {
    return materialType === 'вспомогат.' ? 'auxiliary' : 'basic';
  }
  if (boqItemType === 'мат-комп.') {
    return materialType === 'вспомогат.' ? 'auxiliary' : 'component_material';
  }
  if (boqItemType === 'суб-мат') {
    return materialType === 'вспомогат.' ? 'subcontract_auxiliary' : 'subcontract_basic';
  }
  if (boqItemType === 'раб') return 'work';
  if (boqItemType === 'раб-комп.') return 'component_work';
  if (boqItemType === 'суб-раб') return 'work';
  return null;
}

/**
 * Применяет распределение ценообразования к коммерческой стоимости
 * Разделяет commercialCost на базовую стоимость и наценку, затем распределяет их
 */
export function applyPricingDistribution(
  baseAmount: number,
  commercialCost: number,
  boqItemType: string,
  materialTypeField: string | null | undefined,
  distributionOrNull: PricingDistribution | null
): { materialCost: number; workCost: number } {
  // Настроек нет — применяем дефолт, который показывает Конструктор наценок.
  const distribution = distributionOrNull ?? DEFAULT_PRICING_DISTRIBUTION;

  // Вычисляем базовую стоимость и наценку
  const markup = commercialCost - baseAmount;

  // Определяем тип материала/работы с учетом material_type поля
  const materialType = getMaterialType(boqItemType, materialTypeField);
  if (!materialType) {
    console.warn(`⚠️ Неизвестный тип элемента: ${boqItemType}`);
    return { materialCost: 0, workCost: commercialCost };
  }

  let materialCost = 0;
  let workCost = 0;

  // Применяем распределение для каждого типа
  switch (materialType) {
    case 'basic':
      materialCost += distribution.basic_material_base_target === 'material' ? baseAmount : 0;
      workCost += distribution.basic_material_base_target === 'work' ? baseAmount : 0;
      materialCost += distribution.basic_material_markup_target === 'material' ? markup : 0;
      workCost += distribution.basic_material_markup_target === 'work' ? markup : 0;
      break;

    case 'auxiliary':
      materialCost += distribution.auxiliary_material_base_target === 'material' ? baseAmount : 0;
      workCost += distribution.auxiliary_material_base_target === 'work' ? baseAmount : 0;
      materialCost += distribution.auxiliary_material_markup_target === 'material' ? markup : 0;
      workCost += distribution.auxiliary_material_markup_target === 'work' ? markup : 0;
      break;

    case 'component_material':
      if (distribution.component_material_base_target && distribution.component_material_markup_target) {
        materialCost += distribution.component_material_base_target === 'material' ? baseAmount : 0;
        workCost += distribution.component_material_base_target === 'work' ? baseAmount : 0;
        materialCost += distribution.component_material_markup_target === 'material' ? markup : 0;
        workCost += distribution.component_material_markup_target === 'work' ? markup : 0;
      } else {
        // Fallback к auxiliary если нет настроек для component_material
        materialCost += distribution.auxiliary_material_base_target === 'material' ? baseAmount : 0;
        workCost += distribution.auxiliary_material_base_target === 'work' ? baseAmount : 0;
        materialCost += distribution.auxiliary_material_markup_target === 'material' ? markup : 0;
        workCost += distribution.auxiliary_material_markup_target === 'work' ? markup : 0;
      }
      break;

    case 'subcontract_basic':
      if (distribution.subcontract_basic_material_base_target && distribution.subcontract_basic_material_markup_target) {
        materialCost += distribution.subcontract_basic_material_base_target === 'material' ? baseAmount : 0;
        workCost += distribution.subcontract_basic_material_base_target === 'work' ? baseAmount : 0;
        materialCost += distribution.subcontract_basic_material_markup_target === 'material' ? markup : 0;
        workCost += distribution.subcontract_basic_material_markup_target === 'work' ? markup : 0;
      } else {
        // Fallback на старую логику для субматериалов
        workCost = commercialCost;
      }
      break;

    case 'subcontract_auxiliary':
      if (distribution.subcontract_auxiliary_material_base_target && distribution.subcontract_auxiliary_material_markup_target) {
        materialCost += distribution.subcontract_auxiliary_material_base_target === 'material' ? baseAmount : 0;
        workCost += distribution.subcontract_auxiliary_material_base_target === 'work' ? baseAmount : 0;
        materialCost += distribution.subcontract_auxiliary_material_markup_target === 'material' ? markup : 0;
        workCost += distribution.subcontract_auxiliary_material_markup_target === 'work' ? markup : 0;
      } else {
        // Fallback на старую логику
        workCost = commercialCost;
      }
      break;

    case 'work':
      materialCost += distribution.work_base_target === 'material' ? baseAmount : 0;
      workCost += distribution.work_base_target === 'work' ? baseAmount : 0;
      materialCost += distribution.work_markup_target === 'material' ? markup : 0;
      workCost += distribution.work_markup_target === 'work' ? markup : 0;
      break;

    case 'component_work':
      if (distribution.component_work_base_target && distribution.component_work_markup_target) {
        materialCost += distribution.component_work_base_target === 'material' ? baseAmount : 0;
        workCost += distribution.component_work_base_target === 'work' ? baseAmount : 0;
        materialCost += distribution.component_work_markup_target === 'material' ? markup : 0;
        workCost += distribution.component_work_markup_target === 'work' ? markup : 0;
      } else {
        // Fallback к work если нет настроек для component_work
        materialCost += distribution.work_base_target === 'material' ? baseAmount : 0;
        workCost += distribution.work_base_target === 'work' ? baseAmount : 0;
        materialCost += distribution.work_markup_target === 'material' ? markup : 0;
        workCost += distribution.work_markup_target === 'work' ? markup : 0;
      }
      break;
  }

  return { materialCost, workCost };
}

/**
 * Интерфейс тактики наценок
 */
interface MarkupTactic {
  sequences: Record<string, MarkupStep[]>;
  base_costs?: Record<string, number>;
}

/**
 * Структура для хранения исключений роста субподряда
 */
export interface SubcontractGrowthExclusions {
  works: Set<string>;      // Категории исключенные для суб-раб
  materials: Set<string>;  // Категории исключенные для суб-мат
}

type CalculableBoqItem = Pick<
  BoqItem,
  'id' |
  'boq_item_type' |
  'material_type' |
  'detail_cost_category_id' |
  'total_amount'
>;

/**
 * Загружает исключения роста субподряда для тендера
 */
export async function loadSubcontractGrowthExclusions(tenderId: string): Promise<SubcontractGrowthExclusions> {
  const exclusions: SubcontractGrowthExclusions = {
    works: new Set(),
    materials: new Set()
  };

  try {
    const rows = await listSubcontractGrowthExclusionsForTender(tenderId);
    rows.forEach(e => {
      if (e.exclusion_type === 'works') {
        exclusions.works.add(e.detail_cost_category_id);
      } else if (e.exclusion_type === 'materials') {
        exclusions.materials.add(e.detail_cost_category_id);
      }
    });
  } catch {
    // нет настроек / ошибка — пустые исключения (как и было при ошибке)
  }

  return exclusions;
}

/**
 * Проверяет, исключен ли элемент из роста субподряда
 */
function isExcludedFromGrowth(
  item: Pick<BoqItem, 'boq_item_type' | 'detail_cost_category_id'>,
  exclusions: SubcontractGrowthExclusions
): boolean {
  // Если нет категории, не исключаем
  if (!item.detail_cost_category_id) {
    return false;
  }

  // Проверяем для суб-раб
  if (item.boq_item_type === 'суб-раб') {
    return exclusions.works.has(item.detail_cost_category_id);
  }

  // Проверяем для суб-мат
  if (item.boq_item_type === 'суб-мат') {
    return exclusions.materials.has(item.detail_cost_category_id);
  }

  return false;
}

/**
 * Фильтрует последовательность наценок, удаляя параметры роста субподряда для исключенных категорий
 */
function filterSequenceForExclusions(
  sequence: MarkupStep[],
  isExcluded: boolean,
  itemType: string
): MarkupStep[] {
  if (!isExcluded) {
    return sequence;
  }

  // Определяем какой ключ роста нужно убрать в зависимости от типа элемента
  const growthKeyToRemove = itemType === 'суб-раб'
    ? 'subcontract_works_cost_growth'
    : 'subcontract_materials_cost_growth';

  // Находим индексы шагов, которые нужно удалить
  const removedIndices: number[] = [];
  sequence.forEach((step, index) => {
    const operandKeys = [
      step.operand1Key,
      step.operand2Key,
      step.operand3Key,
      step.operand4Key,
      step.operand5Key
    ].filter(Boolean);

    if (operandKeys.includes(growthKeyToRemove)) {
      removedIndices.push(index);
    }
  });

  // Фильтруем последовательность
  const filtered = sequence.filter((_, index) => !removedIndices.includes(index));

  // ВАЖНО: Пересчитываем baseIndex для оставшихся шагов
  // Если шаг ссылался на удаленный шаг, он должен ссылаться на базовую стоимость (-1)
  // Если шаг ссылался на сохраненный шаг, нужно скорректировать индекс
  return filtered.map((step) => {
    let newBaseIndex = step.baseIndex;

    if (newBaseIndex >= 0) {
      // Проверяем, был ли удален шаг, на который ссылается baseIndex
      if (removedIndices.includes(newBaseIndex)) {
        // Если да, то теперь применяем к базовой стоимости
        newBaseIndex = -1;
      } else {
        // Если нет, пересчитываем индекс с учетом удаленных шагов
        // Считаем сколько шагов было удалено до текущего baseIndex
        const removedBefore = removedIndices.filter(i => i < newBaseIndex).length;
        newBaseIndex = newBaseIndex - removedBefore;
      }
    }

    return {
      ...step,
      baseIndex: newBaseIndex
    };
  });
}

/**
 * Фильтрует последовательность, исключая шаги с НДС
 */
function filterVATFromSequence(
  sequence: MarkupStep[],
  markupParameters: Map<string, number>
): { filtered: MarkupStep[]; vatCoefficient: number } {
  // Ищем параметр НДС по ключу nds_22
  const vatKey = 'nds_22';
  const vatParamValue = markupParameters.get(vatKey) || 0;

  // Находим индексы шагов с НДС и определяем коэффициент
  const removedIndices: number[] = [];
  let vatCoefficient = 0;

  sequence.forEach((step, index) => {
    const operandKeys = [
      step.operand1Key,
      step.operand2Key,
      step.operand3Key,
      step.operand4Key,
      step.operand5Key
    ].filter(Boolean);

    // Вариант 1: шаг ссылается на параметр nds_22
    if (operandKeys.includes(vatKey) && vatParamValue > 0) {
      removedIndices.push(index);
      vatCoefficient = vatParamValue;
      return;
    }

    // Вариант 2: шаг с числовым множителем НДС (operand1Type="number", имя содержит "НДС")
    const stepName = (step.name || '').toLowerCase();
    if (stepName.includes('ндс') || stepName.includes('nds') || stepName.includes('vat')) {
      // Извлекаем коэффициент НДС из числового операнда
      const operandTypes = [
        { key: step.operand1Key, type: step.operand1Type },
        { key: step.operand2Key, type: step.operand2Type },
        { key: step.operand3Key, type: step.operand3Type },
        { key: step.operand4Key, type: step.operand4Type },
        { key: step.operand5Key, type: step.operand5Type },
      ];

      for (const op of operandTypes) {
        if (op.type === 'number' && op.key != null) {
          const numValue = typeof op.key === 'number' ? op.key : parseFloat(String(op.key));
          if (!isNaN(numValue) && numValue > 1 && numValue < 2) {
            // Числовой множитель вида 1.22 → коэффициент 22%
            vatCoefficient = Math.round((numValue - 1) * 100);
            removedIndices.push(index);
            break;
          }
        }
      }
    }
  });

  // Если шагов с НДС не найдено, не применяем НДС отдельно
  if (removedIndices.length === 0) {
    return { filtered: sequence, vatCoefficient: 0 };
  }

  // Фильтруем последовательность
  const filtered = sequence.filter((_, index) => !removedIndices.includes(index));

  // Пересчитываем baseIndex для оставшихся шагов
  const result = filtered.map((step) => {
    let newBaseIndex = step.baseIndex;

    if (newBaseIndex >= 0) {
      if (removedIndices.includes(newBaseIndex)) {
        newBaseIndex = -1;
      } else {
        const removedBefore = removedIndices.filter(i => i < newBaseIndex).length;
        newBaseIndex = newBaseIndex - removedBefore;
      }
    }

    return {
      ...step,
      baseIndex: newBaseIndex
    };
  });

  return { filtered: result, vatCoefficient };
}

/**
 * Рассчитывает коэффициент наценки для типа элемента из тактики
 * Применяя последовательность к базе = 1, получаем коэффициент
 */
export function calculateTypeCoefficient(
  sequence: MarkupStep[],
  markupParameters: Map<string, number>,
  /** @deprecated P0: preview base_cost конструктора; в production не передавать. */
  baseCost?: number
): number {
  if (!sequence || sequence.length === 0) {
    return 1;
  }

  // Создаем контекст с baseAmount = 1 для получения коэффициента
  const context: CalculationContext = {
    baseAmount: 1,
    itemType: 'мат' as const, // Тип не используется в расчете, указываем любой допустимый
    markupSequence: sequence,
    markupParameters,
    baseCost: baseCost
  };

  const result = calculateMarkupResult(context);
  return result.commercialCost;
}

/**
 * Кэш коэффициентов по типам элементов для текущего пересчёта
 */
const typeCoefficientsCache = new Map<string, number>();

/**
 * Сбрасывает кэш коэффициентов (вызывать в начале пересчёта)
 */
export function resetTypeCoefficientsCache(): void {
  typeCoefficientsCache.clear();
}

/**
 * Выполняет расчет коммерческой стоимости для элемента BOQ
 * Упрощённая логика: коэффициент × база = коммерческая, затем распределение
 */
export function calculateBoqItemCost(
  item: CalculableBoqItem,
  tactic: MarkupTactic,
  markupParameters: Map<string, number>,
  pricingDistribution: PricingDistribution | null,
  exclusions?: SubcontractGrowthExclusions
): { materialCost: number; workCost: number; markupCoefficient: number } | null {
  try {
    // Получаем последовательность для типа элемента
    let sequence = tactic.sequences[item.boq_item_type];
    if (!sequence || sequence.length === 0) {
      return null;
    }

    const baseAmount = item.total_amount || 0;

    // Проверяем, исключен ли элемент из роста субподряда
    const isExcluded = exclusions
      ? isExcludedFromGrowth(item, exclusions)
      : false;

    // Если исключен, фильтруем последовательность
    if (isExcluded) {
      sequence = filterSequenceForExclusions(sequence, true, item.boq_item_type);
    }

    // Исключаем НДС из последовательности (НДС применим отдельно)
    const { filtered: sequenceWithoutVAT, vatCoefficient } = filterVATFromSequence(sequence, markupParameters);

    // Формируем ключ для кэша (тип + исключён + НДС)
    const cacheKey = `${item.boq_item_type}_${isExcluded ? 'excl' : 'norm'}_${vatCoefficient}`;

    // Получаем коэффициент из кэша или рассчитываем
    let coefficientWithoutVAT: number;
    if (typeCoefficientsCache.has(cacheKey)) {
      coefficientWithoutVAT = typeCoefficientsCache.get(cacheKey)!;
    } else {
      // P0: base_costs (preview-значение MarkupConstructor) НЕ должно влиять на
      // рабочий коэффициент — третий аргумент намеренно не передаём.
      coefficientWithoutVAT = calculateTypeCoefficient(
        sequenceWithoutVAT,
        markupParameters
      );
      typeCoefficientsCache.set(cacheKey, coefficientWithoutVAT);
    }

    // Коммерческая стоимость БЕЗ НДС = база × коэффициент
    const commercialCostWithoutVAT = baseAmount * coefficientWithoutVAT;

    // Применяем распределение ценообразования
    let { materialCost, workCost } = applyPricingDistribution(
      baseAmount,
      commercialCostWithoutVAT,
      item.boq_item_type,
      item.material_type,
      pricingDistribution
    );

    // Применяем НДС к обоим столбцам независимо
    // Каждый столбец получает свой НДС, не перетекая в другой
    if (vatCoefficient > 0) {
      const vatMultiplier = 1 + (vatCoefficient / 100);
      materialCost = materialCost * vatMultiplier;
      workCost = workCost * vatMultiplier;
    }

    // Итоговый коэффициент наценки с учетом НДС
    const totalCommercialCost = materialCost + workCost;
    const markupCoefficient = baseAmount > 0
      ? totalCommercialCost / baseAmount
      : 1;

    return {
      materialCost,
      workCost,
      markupCoefficient
    };

  } catch (error) {
    console.error(`Ошибка расчета элемента ${item.id}:`, error);
    return null;
  }
}

/**
 * Проверяет, нужен ли пересчет для элемента BOQ
 * @param item Элемент BOQ
 * @returns true, если нужен пересчет
 */
export function needsRecalculation(item: BoqItem): boolean {
  // Пересчет нужен, если:
  // 1. Есть базовая стоимость, но нет коммерческой
  // 2. Коэффициент наценки не соответствует отношению коммерческой к базовой стоимости

  if (!item.total_amount || item.total_amount === 0) {
    return false;
  }

  const isMaterial = ['мат', 'суб-мат', 'мат-комп.'].includes(item.boq_item_type);
  const commercialCost = isMaterial
    ? item.total_commercial_material_cost
    : item.total_commercial_work_cost;

  // Если коммерческая стоимость не задана
  if (!commercialCost) {
    return true;
  }

  // Проверяем соответствие коэффициента
  if (item.commercial_markup) {
    const expectedCost = item.total_amount * item.commercial_markup;
    const difference = Math.abs(expectedCost - commercialCost);

    // Если разница больше 0.01 (1 копейка), нужен пересчет
    return difference > 0.01;
  }

  return true;
}
