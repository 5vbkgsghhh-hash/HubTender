import {
  ParsedBoqItem,
  ParsedAdditionalPosition,
  PositionUpdateData,
  ValidationError,
  ValidationResult,
  ClientPosition,
  MissingNomenclatureGroup,
  NonLeafPositionGroup,
  isWork,
  isMaterial,
  buildNomenclatureLookupKey,
  findCostCategoryId,
} from './massBoqImportUtils';
import { validateBoqRowBasics } from '../../../utils/boq/importRowValidation';
import { validateAdditionalPositions, type AdditionalValidationMaps } from './massBoqImportAdditional';

// ===========================
// ВАЛИДАЦИЯ
// ===========================

interface ValidationMaps extends AdditionalValidationMaps {
  clientPositionsMap: Map<string, ClientPosition>;
  workNamesMap: Map<string, string>;
  materialNamesMap: Map<string, string>;
  costCategoriesMap: Map<string, string>;
  leafPositionIds: Set<string>;
}

export const validateBoqData = (
  data: ParsedBoqItem[],
  positionUpdates: Map<string, PositionUpdateData>,
  maps: ValidationMaps,
  additionalPositions: ParsedAdditionalPosition[] = [],
): ValidationResult => {
  const { clientPositionsMap, workNamesMap, materialNamesMap, costCategoriesMap, leafPositionIds } = maps;

  const errors: ValidationError[] = [];
  const warnings: ValidationError[] = [];
  const missingWorksMap = new Map<string, MissingNomenclatureGroup>();
  const missingMaterialsMap = new Map<string, MissingNomenclatureGroup>();
  const unknownCostsMap = new Map<string, number[]>();
  const unmatchedPositionsMap = new Map<string, number[]>();
  const nonLeafPositionsMap = new Map<string, NonLeafPositionGroup>();

  const validBoqTypes = ['раб', 'суб-раб', 'раб-комп.', 'мат', 'суб-мат', 'мат-комп.'];
  const validMaterialTypes = ['основн.', 'вспомогат.'];
  const validCurrencies = ['RUB', 'USD', 'EUR', 'CNY'];
  const validDeliveryTypes = ['в цене', 'не в цене', 'суммой'];

  data.forEach((item) => {
    const row = item.rowIndex;

    // 1. Проверка номера позиции и сопоставление. Элементы блока «доп» уходят в
    // новую ДОП (всегда лист) — её родителя проверяет validateAdditionalPositions.
    if (item.additionalTempId) {
      // позиция будет создана импортом
    } else if (!item.positionNumber) {
      errors.push({
        rowIndex: row,
        type: 'missing_field',
        field: 'positionNumber',
        message: 'Отсутствует номер позиции',
        severity: 'error',
      });
    } else {
      const position = clientPositionsMap.get(item.positionNumber);
      if (!position) {
        if (!unmatchedPositionsMap.has(item.positionNumber)) {
          unmatchedPositionsMap.set(item.positionNumber, []);
        }
        unmatchedPositionsMap.get(item.positionNumber)!.push(row);

        errors.push({
          rowIndex: row,
          type: 'position_not_found',
          field: 'positionNumber',
          message: `Позиция "${item.positionNumber}" не найдена в тендере`,
          severity: 'error',
        });
      } else {
        item.matchedPositionId = position.id;
        const posData = positionUpdates.get(item.positionNumber);
        if (posData) {
          posData.positionId = position.id;
        }

        if (!leafPositionIds.has(position.id)) {
          errors.push({
            rowIndex: row,
            type: 'non_leaf_position',
            field: 'positionNumber',
            message: `Позиция "${item.positionNumber}" — раздел/заголовок, в неё нельзя загружать работы/материалы`,
            severity: 'error',
          });

          const existingGroup = nonLeafPositionsMap.get(item.positionNumber);
          if (existingGroup) {
            existingGroup.rows.push(row);
          } else {
            nonLeafPositionsMap.set(item.positionNumber, {
              positionNumber: item.positionNumber,
              positionName: position.work_name || '',
              rows: [row],
            });
          }
        }
      }
    }

    // 2. Проверка обязательных полей
    if (!item.nameText) {
      errors.push({
        rowIndex: row,
        type: 'missing_field',
        field: 'nameText',
        message: 'Отсутствует наименование',
        severity: 'error',
      });
    }

    if (!item.unit_code) {
      errors.push({
        rowIndex: row,
        type: 'missing_field',
        field: 'unit_code',
        message: 'Отсутствует единица измерения',
        severity: 'error',
      });
    }

    if (!item.costCategoryText || item.costCategoryText.trim() === '') {
      errors.push({
        rowIndex: row,
        type: 'missing_field',
        field: 'costCategoryText',
        message: 'Отсутствует затрата на строительство',
        severity: 'error',
      });
    }

    // 3. Проверка типов
    if (!validBoqTypes.includes(item.boq_item_type)) {
      errors.push({
        rowIndex: row,
        type: 'invalid_type',
        field: 'boq_item_type',
        message: `Недопустимый тип элемента: "${item.boq_item_type}"`,
        severity: 'error',
      });
    }

    if (isMaterial(item.boq_item_type) && item.material_type && !validMaterialTypes.includes(item.material_type)) {
      errors.push({
        rowIndex: row,
        type: 'invalid_type',
        field: 'material_type',
        message: `Недопустимый тип материала: "${item.material_type}"`,
        severity: 'error',
      });
    }

    if (!validCurrencies.includes(item.currency_type)) {
      errors.push({
        rowIndex: row,
        type: 'invalid_type',
        field: 'currency_type',
        message: `Недопустимая валюта: "${item.currency_type}"`,
        severity: 'error',
      });
    }

    if (item.delivery_price_type && !validDeliveryTypes.includes(item.delivery_price_type)) {
      errors.push({
        rowIndex: row,
        type: 'invalid_type',
        field: 'delivery_price_type',
        message: `Недопустимый тип доставки: "${item.delivery_price_type}"`,
        severity: 'error',
      });
    }

    // 3.1 Количество и коэффициенты (общие правила, см. validateBoqRowBasics)
    validateBoqRowBasics(item).forEach((issue) => {
      (issue.severity === 'warning' ? warnings : errors).push({
        rowIndex: row,
        type: 'missing_field',
        field: issue.field,
        message: issue.message,
        severity: issue.severity,
      });
    });

    // 4. Проверка номенклатуры
    if (isWork(item.boq_item_type)) {
      const key = buildNomenclatureLookupKey(item.nameText, item.unit_code);
      const workId = workNamesMap.get(key);

      if (!workId) {
        errors.push({
          rowIndex: row,
          type: 'missing_nomenclature',
          field: 'work_name',
          message: `Работа "${item.nameText}" [${item.unit_code}] отсутствует в номенклатуре`,
          severity: 'error',
        });

        const groupKey = `${item.nameText}|${item.unit_code}`;
        if (!missingWorksMap.has(groupKey)) {
          missingWorksMap.set(groupKey, { name: item.nameText, unit: item.unit_code, rows: [] });
        }
        missingWorksMap.get(groupKey)!.rows.push(row);
      } else {
        item.work_name_id = workId;
      }
    }

    if (isMaterial(item.boq_item_type)) {
      const key = buildNomenclatureLookupKey(item.nameText, item.unit_code);
      const materialId = materialNamesMap.get(key);

      if (!materialId) {
        errors.push({
          rowIndex: row,
          type: 'missing_nomenclature',
          field: 'material_name',
          message: `Материал "${item.nameText}" [${item.unit_code}] отсутствует в номенклатуре`,
          severity: 'error',
        });

        const groupKey = `${item.nameText}|${item.unit_code}`;
        if (!missingMaterialsMap.has(groupKey)) {
          missingMaterialsMap.set(groupKey, { name: item.nameText, unit: item.unit_code, rows: [] });
        }
        missingMaterialsMap.get(groupKey)!.rows.push(row);
      } else {
        item.material_name_id = materialId;
      }
    }

    // 5. Проверка затраты на строительство
    if (item.costCategoryText) {
      const costId = findCostCategoryId(item.costCategoryText, costCategoriesMap);

      if (!costId) {
        errors.push({
          rowIndex: row,
          type: 'missing_cost',
          field: 'detail_cost_category_id',
          message: `Затрата "${item.costCategoryText}" не найдена в БД`,
          severity: 'error',
        });

        if (!unknownCostsMap.has(item.costCategoryText)) {
          unknownCostsMap.set(item.costCategoryText, []);
        }
        unknownCostsMap.get(item.costCategoryText)!.push(row);
      } else {
        item.detail_cost_category_id = costId;
      }
    }
  });

  // 5.1 Строки «доп» (новые ДОП)
  errors.push(...validateAdditionalPositions(additionalPositions, maps));

  // 6. Валидация position-only записей (только данные ГП без BOQ-элементов)
  positionUpdates.forEach((posData, posNum) => {
    const position = clientPositionsMap.get(posNum);
    if (position) {
      posData.positionId = position.id;
    }

    if (posData.itemsCount > 0) return;
    if (posData.manualVolume === undefined && posData.manualNote === undefined) return;

    if (!position) {
      if (!unmatchedPositionsMap.has(posNum)) {
        unmatchedPositionsMap.set(posNum, []);
      }
      unmatchedPositionsMap.get(posNum)!.push(0);

      errors.push({
        rowIndex: 0,
        type: 'position_not_found',
        field: 'positionNumber',
        message: `Позиция "${posNum}" (только данные ГП) не найдена в тендере`,
        severity: 'error',
      });
    }
  });

  const result: ValidationResult = {
    isValid: errors.length === 0,
    errors,
    warnings,
    missingNomenclature: {
      works: Array.from(missingWorksMap.values()),
      materials: Array.from(missingMaterialsMap.values()),
    },
    unknownCosts: Array.from(unknownCostsMap.entries()).map(([text, rows]) => ({ text, rows })),
    unmatchedPositions: Array.from(unmatchedPositionsMap.entries()).map(([positionNumber, rows]) => ({ positionNumber, rows })),
    nonLeafPositions: Array.from(nonLeafPositionsMap.values()),
  };

  console.log('[MassBoqImport] Результат валидации:', {
    isValid: result.isValid,
    errorsCount: errors.length,
    unmatchedPositions: result.unmatchedPositions.length,
  });

  return result;
};

// ===========================
// ОБРАБОТКА ПРИВЯЗОК
// ===========================

export const processWorkBindings = (data: ParsedBoqItem[]): ValidationError[] => {
  const errors: ValidationError[] = [];

  // Привязка «материал → работа выше» не пересекает границу ДОП: у элементов
  // блока «доп» своя группа.
  const byPosition = new Map<string, ParsedBoqItem[]>();
  data.forEach(item => {
    const posId = item.additionalTempId
      ? `dop:${item.additionalTempId}`
      : item.matchedPositionId || item.positionNumber;
    if (!byPosition.has(posId)) {
      byPosition.set(posId, []);
    }
    byPosition.get(posId)!.push(item);
  });

  byPosition.forEach((items) => {
    let lastWork: ParsedBoqItem | null = null;

    items.forEach((item) => {
      if (isWork(item.boq_item_type)) {
        lastWork = item;
        item.tempId = `work_${item.rowIndex}`;
      } else if (item.bindToWork) {
        if (!lastWork) {
          errors.push({
            rowIndex: item.rowIndex,
            type: 'binding_error',
            field: 'parent_work_item_id',
            message: 'Материал с привязкой, но работа не найдена выше в этой позиции',
            severity: 'error',
          });
        } else {
          item.parent_work_item_id = lastWork.tempId;

          const workQty = lastWork.quantity || 0;
          const convCoef = item.conversion_coefficient || 1;
          const consCoef = item.consumption_coefficient || 1;
          item.quantity = workQty * convCoef * consCoef;
        }
      } else {
        item.quantity = item.base_quantity || 0;
      }
    });
  });

  return errors;
};

// ===========================
// ВСПОМОГАТЕЛЬНЫЕ ФУНКЦИИ ДЛЯ РАСЧЕТА
// ===========================

export const getCurrencyRate = (
  currency: string,
  rates: { usd: number; eur: number; cny: number },
): number => {
  switch (currency) {
    case 'USD': return rates.usd;
    case 'EUR': return rates.eur;
    case 'CNY': return rates.cny;
    case 'RUB':
    default: return 1;
  }
};

export const calculateTotalAmount = (
  item: ParsedBoqItem,
  rates: { usd: number; eur: number; cny: number },
): number => {
  const rate = getCurrencyRate(item.currency_type || 'RUB', rates);
  const unitRate = item.unit_rate || 0;
  const quantity = item.quantity || 0;

  if (isWork(item.boq_item_type)) {
    return quantity * unitRate * rate;
  } else {
    const unitPriceInRub = unitRate * rate;
    let deliveryPrice = 0;

    if (item.delivery_price_type === 'не в цене') {
      deliveryPrice = unitPriceInRub * 0.03;
    } else if (item.delivery_price_type === 'суммой') {
      deliveryPrice = item.delivery_amount || 0;
    }

    const consumptionCoeff = !item.parent_work_item_id ? (item.consumption_coefficient || 1) : 1;
    return quantity * consumptionCoeff * (unitPriceInRub + deliveryPrice);
  }
};
