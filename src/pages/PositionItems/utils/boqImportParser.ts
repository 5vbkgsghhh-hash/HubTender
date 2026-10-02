import {
  isWork,
  normalizeString,
  parseNumber,
  parseBoolean,
} from '../../../utils/boq/importShared';
import {
  isAdditionalRowMarker,
  parseAdditionalRow,
  type ParsedAdditionalRow,
} from '../../../utils/boq/additionalImport';
import type { ParsedBoqItem, ValidationError } from './boqImportTypes';

// Нормализация типа материала (поддержка разных вариантов написания)
// ВНИМАНИЕ: у массового импорта своя версия с другими ветками — известный
// дрейф двойной реализации, НЕ унифицировать молча.
export const normalizeMaterialType = (value: string | undefined): 'основн.' | 'вспомогат.' | undefined => {
  if (!value) return undefined;

  const original = String(value).trim();
  const normalized = original.toLowerCase()
    .replace(/\s+/g, '')  // Убираем пробелы
    .replace(/\.$/, '');   // Убираем точку в конце если есть

  let result: 'основн.' | 'вспомогат.' | undefined = undefined;

  // Основной материал
  if (normalized === 'основной' || normalized === 'основн' || normalized === 'основ' || normalized === 'осн') {
    result = 'основн.';
  }
  // Вспомогательный материал
  else if (normalized === 'вспомогательный' || normalized === 'вспомогат' || normalized === 'вспом') {
    result = 'вспомогат.';
  }
  // Если уже в нужном формате
  else if (original === 'основн.' || original === 'вспомогат.') {
    result = original as 'основн.' | 'вспомогат.';
  }

  if (original !== result) {
    console.log(`[MaterialType] Нормализация: "${original}" -> "${result}"`);
  }

  return result;
};

// Нормализация типа доставки (поддержка разных вариантов написания)
export const normalizeDeliveryPriceType = (value: string | undefined): 'в цене' | 'не в цене' | 'суммой' | undefined => {
  if (!value) return undefined;

  const original = String(value).trim();
  const normalized = original.toLowerCase()
    .replace(/\s+/g, ' ');  // Нормализуем пробелы

  let result: 'в цене' | 'не в цене' | 'суммой' | undefined = undefined;

  // "в цене"
  if (normalized === 'в цене' || normalized === 'вцене' || normalized === 'входит') {
    result = 'в цене';
  }
  // "не в цене"
  else if (normalized === 'не в цене' || normalized === 'невцене' || normalized === 'не входит' || normalized === 'невходит') {
    result = 'не в цене';
  }
  // "суммой"
  else if (normalized === 'суммой' || normalized === 'доп. стоимость' || normalized === 'доп стоимость' || normalized === 'дополнительно') {
    result = 'суммой';
  }
  // Если уже в нужном формате
  else if (original === 'в цене' || original === 'не в цене' || original === 'суммой') {
    result = original as 'в цене' | 'не в цене' | 'суммой';
  }

  if (original !== result) {
    console.log(`[DeliveryPriceType] Нормализация: "${original}" -> "${result}"`);
  }

  return result;
};

export interface ParsedBoqExcel {
  items: ParsedBoqItem[];
  /** Строки «доп»: новые ДОП к позиции; элементы ниже уходят в них. */
  additionalRows: ParsedAdditionalRow[];
}

/**
 * Чистый маппинг строк Excel (без заголовка) в ParsedBoqItem[] —
 * тело parseExcelFile, вынесенное из хука. Строки до первой «доп» идут в
 * текущую позицию, после «доп» — в эту новую ДОП до следующей «доп».
 */
export const parseBoqExcelRows = (rows: unknown[]): ParsedBoqExcel => {
  const parsed: ParsedBoqItem[] = [];
  const additionalRows: ParsedAdditionalRow[] = [];
  let currentAdditional: ParsedAdditionalRow | null = null;

  rows.forEach((row: unknown, index: number) => {
    if (!Array.isArray(row)) return;

    // Проверяем, что строка не пустая
    const hasData = row.some(cell => cell !== undefined && cell !== null && cell !== '');
    if (!hasData) return;

    const cells = row as unknown[];
    const rowNum = index + 2; // +2 потому что индекс с 0 и пропустили заголовок

    // Строка «доп» — новая ДОП, сама элементом не является.
    if (isAdditionalRowMarker(cells[4])) {
      currentAdditional = parseAdditionalRow(cells, rowNum);
      additionalRows.push(currentAdditional);
      return;
    }

    // Маппинг колонок согласно структуре из шаблона
    const item: ParsedBoqItem = {
      rowIndex: rowNum,

      ...(currentAdditional ? { additionalTempId: currentAdditional.tempId } : {}),

      // Колонка 4: Тип элемента
      boq_item_type: cells[4] ? String(cells[4]).trim() as ParsedBoqItem['boq_item_type'] : 'мат',

      // Колонка 5: Тип материала (с нормализацией)
      material_type: normalizeMaterialType(cells[5] != null ? String(cells[5]) : undefined),

      // Колонка 6: Наименование
      nameText: cells[6] ? normalizeString(String(cells[6])) : '',

      // Колонка 7: Ед. изм.
      unit_code: cells[7] ? String(cells[7]).trim() : '',

      // Колонка 3: Привязка материала к работе
      bindToWork: parseBoolean(cells[3]),

      // Колонка 9: Коэфф. перевода
      conversion_coefficient: parseNumber(cells[9]),

      // Колонка 10: Коэфф. расхода
      consumption_coefficient: parseNumber(cells[10]),

      // Колонка 11: Количество (base_quantity для непривязанных материалов)
      base_quantity: parseNumber(cells[11]),
      quantity: parseNumber(cells[11]), // Будет пересчитано для привязанных материалов

      // Колонка 12: Валюта
      currency_type: cells[12] ? String(cells[12]).trim() as ParsedBoqItem['currency_type'] : 'RUB',

      // Колонка 13: Тип доставки (с нормализацией)
      delivery_price_type: normalizeDeliveryPriceType(cells[13] != null ? String(cells[13]) : undefined),

      // Колонка 14: Стоимость доставки
      delivery_amount: parseNumber(cells[14]),

      // Колонка 15: Цена за единицу
      unit_rate: parseNumber(cells[15]),

      // Колонка 2: Затрата на строительство
      costCategoryText: cells[2] ? String(cells[2]).trim() : '',

      // Колонка 17: Ссылка на КП
      quote_link: cells[17] ? String(cells[17]).trim() : undefined,

      // Колонка 19: Примечание ГП
      description: cells[19] ? String(cells[19]).trim() : undefined,

      // Сортировка
      sort_number: index,
    };

    parsed.push(item);
    if (currentAdditional) {
      currentAdditional.itemsCount++;
    }
  });

  return { items: parsed, additionalRows };
};

/**
 * Привязка материалов к работам: проставляет tempId работам
 * (контракт `work_${rowIndex}` — его потребляет workIdMap в insertBoqItems),
 * parent_work_item_id и quantity материалам. Мутирует элементы in-place.
 */
export const processWorkBindings = (data: ParsedBoqItem[]): ValidationError[] => {
  const errors: ValidationError[] = [];
  let lastWork: ParsedBoqItem | null = null;
  // Привязка не пересекает границу ДОП: в новом блоке «доп» работа ищется заново.
  let currentBlock: string | undefined;

  data.forEach((item) => {
    if (item.additionalTempId !== currentBlock) {
      currentBlock = item.additionalTempId;
      lastWork = null;
    }
    if (isWork(item.boq_item_type)) {
      lastWork = item;
      item.tempId = `work_${item.rowIndex}`;
    } else if (item.bindToWork) {
      if (!lastWork) {
        errors.push({
          rowIndex: item.rowIndex,
          type: 'binding_error',
          field: 'parent_work_item_id',
          message: 'Материал с привязкой, но работа не найдена выше',
          severity: 'error',
        });
      } else {
        item.parent_work_item_id = lastWork.tempId;

        // Расчет quantity: работа.quantity * коэфф.перевода * коэфф.расхода
        const workQty = lastWork.quantity || 0;
        const convCoef = item.conversion_coefficient || 1;
        const consCoef = item.consumption_coefficient || 1;
        item.quantity = workQty * convCoef * consCoef;
      }
    } else {
      // Независимый материал: quantity = base_quantity (коэфф.расхода применяется при расчёте стоимости)
      item.quantity = item.base_quantity || 0;
    }
  });

  return errors;
};
