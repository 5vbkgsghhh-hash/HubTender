import {
  isWork,
  isMaterial,
  normalizeString,
  buildNomenclatureLookupKey,
} from '../../../utils/boq/importShared';
import { validateBoqRowBasics } from '../../../utils/boq/importRowValidation';
import {
  findDuplicateAdditionalRows,
  validateAdditionalRowBasics,
  type ParsedAdditionalRow,
} from '../../../utils/boq/additionalImport';
import type {
  ParsedBoqItem,
  ValidationError,
  MissingNomenclatureGroup,
  ValidationResult,
} from './boqImportTypes';

// Справочники для валидации (зеркалит ValidationMaps массового импорта).
export interface BoqValidationMaps {
  workNamesMap: Map<string, string>;
  materialNamesMap: Map<string, string>;
  costCategoriesMap: Map<string, string>;
  /** Коды единиц — для строк «доп» (единица ДОП ссылается на справочник). */
  unitCodes?: Set<string>;
}

/**
 * Строки «доп»: поля как в модалке «Добавить ДОП работу» и запрет повторов
 * наименования внутри файла (все ДОП создаются к одной позиции). Дубль с уже
 * существующей ДОП позиции отсекает сервер — атомарно, с номером строки.
 */
const validateAdditionalRows = (
  additionalRows: ParsedAdditionalRow[],
  unitCodes?: Set<string>,
): ValidationError[] => {
  const errors: ValidationError[] = [];
  const push = (rowIndex: number, message: string) => {
    errors.push({ rowIndex, type: 'additional_error', field: 'additional', message, severity: 'error' });
  };
  additionalRows.forEach((row) => {
    validateAdditionalRowBasics(row, unitCodes).forEach((message) => push(row.rowIndex, message));
  });
  findDuplicateAdditionalRows(additionalRows, () => 'position').forEach(({ row, firstRow }) => {
    push(row.rowIndex, `ДОП «${row.workName}» уже есть в файле (строка ${firstRow.rowIndex})`);
  });
  return errors;
};

// Парсинг затраты на строительство: "Категория / Детальная категория / Локация"
// ВАЖНО: Детальная категория может содержать слэши (например, "Плиты перекрытия / покрытия / разгрузочные")
// Формат: первая часть - категория, последняя часть - локация, всё между ними - детализация
export const parseCostCategory = (text: string): { category?: string; detail?: string; location?: string } => {
  if (!text) return {};

  // Разбиваем по разделителю " / " (пробел-слэш-пробел)
  const parts = text.split(' / ').map(p => p.trim());

  if (parts.length === 1) {
    // Только одна часть - возможно ошибка
    return { category: parts[0] };
  } else if (parts.length === 2) {
    // Две части: category / detail (без location)
    return {
      category: parts[0],
      detail: parts[1],
    };
  } else {
    // Три или более частей:
    // category = первая часть
    // location = последняя часть
    // detail = всё между ними, объединенное через " / "
    const category = parts[0];
    const location = parts[parts.length - 1];
    const detail = parts.slice(1, parts.length - 1).join(' / ');

    return {
      category: category || undefined,
      detail: detail || undefined,
      location: location || undefined,
    };
  }
};

/**
 * Чистая валидация распарсенных строк против справочников.
 * Мутирует элементы in-place (work_name_id / material_name_id /
 * detail_cost_category_id) — это контракт последующей вставки.
 * setValidationResult остаётся в хуке-обёртке.
 */
export const validateBoqData = (
  data: ParsedBoqItem[],
  { workNamesMap, materialNamesMap, costCategoriesMap, unitCodes }: BoqValidationMaps,
  additionalRows: ParsedAdditionalRow[] = [],
): ValidationResult => {
  console.log('[BoqImport] Валидация данных:', {
    rows: data.length,
    workNamesMapSize: workNamesMap.size,
    materialNamesMapSize: materialNamesMap.size,
  });

  const errors: ValidationError[] = [];
  const warnings: ValidationError[] = [];
  const missingWorksMap = new Map<string, MissingNomenclatureGroup>();
  const missingMaterialsMap = new Map<string, MissingNomenclatureGroup>();
  const unknownCostsMap = new Map<string, number[]>();

  const validBoqTypes = ['раб', 'суб-раб', 'раб-комп.', 'мат', 'суб-мат', 'мат-комп.'];
  const validMaterialTypes = ['основн.', 'вспомогат.'];
  const validCurrencies = ['RUB', 'USD', 'EUR', 'CNY'];
  const validDeliveryTypes = ['в цене', 'не в цене', 'суммой'];

  data.forEach((item) => {
    const row = item.rowIndex;

    // 1. Проверка обязательных полей
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

    // КРИТИЧЕСКОЕ: Проверка обязательной затраты на строительство
    if (!item.costCategoryText || item.costCategoryText.trim() === '') {
      errors.push({
        rowIndex: row,
        type: 'missing_field',
        field: 'costCategoryText',
        message: 'Отсутствует затрата на строительство (колонка 3 обязательна!)',
        severity: 'error',
      });
    }

    // 2. Проверка типа элемента
    if (!validBoqTypes.includes(item.boq_item_type)) {
      errors.push({
        rowIndex: row,
        type: 'invalid_type',
        field: 'boq_item_type',
        message: `Недопустимый тип элемента: "${item.boq_item_type}". Допустимые: ${validBoqTypes.join(', ')}`,
        severity: 'error',
      });
    }

    // 3. Проверка типа материала (только для материалов)
    if (isMaterial(item.boq_item_type) && item.material_type && !validMaterialTypes.includes(item.material_type)) {
      errors.push({
        rowIndex: row,
        type: 'invalid_type',
        field: 'material_type',
        message: `Недопустимый тип материала: "${item.material_type}". Допустимые: ${validMaterialTypes.join(', ')}`,
        severity: 'error',
      });
    }

    // 4. Проверка валюты
    if (!validCurrencies.includes(item.currency_type)) {
      errors.push({
        rowIndex: row,
        type: 'invalid_type',
        field: 'currency_type',
        message: `Недопустимая валюта: "${item.currency_type}". Допустимые: ${validCurrencies.join(', ')}`,
        severity: 'error',
      });
    }

    // 5. Проверка типа доставки
    if (item.delivery_price_type && !validDeliveryTypes.includes(item.delivery_price_type)) {
      errors.push({
        rowIndex: row,
        type: 'invalid_type',
        field: 'delivery_price_type',
        message: `Недопустимый тип доставки: "${item.delivery_price_type}". Допустимые: ${validDeliveryTypes.join(', ')}`,
        severity: 'error',
      });
    }

    // 5.1 Количество и коэффициенты (общие правила, см. validateBoqRowBasics)
    validateBoqRowBasics(item).forEach((issue) => {
      (issue.severity === 'warning' ? warnings : errors).push({
        rowIndex: row,
        type: 'missing_field',
        field: issue.field,
        message: issue.message,
        severity: issue.severity,
      });
    });

    // 6. КРИТИЧНО: Проверка наличия в номенклатуре
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

        // Группировка для отчета
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

        // Группировка для отчета
        const groupKey = `${item.nameText}|${item.unit_code}`;
        if (!missingMaterialsMap.has(groupKey)) {
          missingMaterialsMap.set(groupKey, { name: item.nameText, unit: item.unit_code, rows: [] });
        }
        missingMaterialsMap.get(groupKey)!.rows.push(row);
      } else {
        item.material_name_id = materialId;
      }
    }

    // 7. Проверка затраты на строительство (ОБЯЗАТЕЛЬНАЯ)
    if (item.costCategoryText) {
      // Сначала пробуем найти по полной строке (более надёжный способ)
      const fullPath = normalizeString(item.costCategoryText);
      let costId = costCategoriesMap.get(fullPath);

      // Если не нашли по полной строке, пробуем парсить на части
      let key = fullPath;
      if (!costId) {
        const parsed = parseCostCategory(item.costCategoryText);
        if (parsed.category && parsed.detail && parsed.location) {
          key = `${normalizeString(parsed.category)}|${normalizeString(parsed.detail)}|${normalizeString(parsed.location)}`;
          costId = costCategoriesMap.get(key);
        }
      }

      console.log(`[CostCategory] Строка ${row}:`, {
        original: item.costCategoryText,
        fullPath,
        key,
        found: !!costId,
      });

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

  // 8. Строки «доп» (новые ДОП)
  errors.push(...validateAdditionalRows(additionalRows, unitCodes));

  const result: ValidationResult = {
    isValid: errors.length === 0,
    errors,
    warnings,
    missingNomenclature: {
      works: Array.from(missingWorksMap.values()),
      materials: Array.from(missingMaterialsMap.values()),
    },
    unknownCosts: Array.from(unknownCostsMap.entries()).map(([text, rows]) => ({ text, rows })),
  };

  console.log('[BoqImport] Результат валидации:', {
    isValid: result.isValid,
    errorsCount: errors.length,
    warningsCount: warnings.length,
    missingWorks: result.missingNomenclature.works.length,
    missingMaterials: result.missingNomenclature.materials.length,
  });

  return result;
};
