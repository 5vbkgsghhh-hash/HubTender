import type { ParsedAdditionalRow } from '../../../utils/boq/additionalImport';

// ===========================
// ТИПЫ И ИНТЕРФЕЙСЫ
// ===========================

export interface ParsedBoqItem {
  rowIndex: number;

  // Идентификация позиции
  positionNumber: string;
  matchedPositionId?: string;
  /** Элемент блока «доп»: уходит в новую ДОП (tempId строки «доп»), не в positionNumber. */
  additionalTempId?: string;

  // Основные поля
  boq_item_type: 'раб' | 'суб-раб' | 'раб-комп.' | 'мат' | 'суб-мат' | 'мат-комп.';
  material_type?: 'основн.' | 'вспомогат.';

  // Наименование (для поиска в номенклатуре)
  nameText: string;
  unit_code: string;

  // Найденные ID из номенклатуры
  work_name_id?: string;
  material_name_id?: string;

  // Привязка к работе
  bindToWork: boolean;
  parent_work_item_id?: string;
  tempId?: string;

  // Количество и коэффициенты
  base_quantity?: number;
  quantity?: number;
  conversion_coefficient?: number;
  consumption_coefficient?: number;

  // Финансовые поля
  currency_type: 'RUB' | 'USD' | 'EUR' | 'CNY';
  delivery_price_type?: 'в цене' | 'не в цене' | 'суммой';
  delivery_amount?: number;
  unit_rate?: number;

  // Затрата на строительство
  costCategoryText: string;
  detail_cost_category_id?: string;

  // Дополнительно
  quote_link?: string;
  description?: string;

  // Сортировка
  sort_number: number;
}

// Строка «доп» массового импорта: новая ДОП к позиции заказчика выше.
export interface ParsedAdditionalPosition extends ParsedAdditionalRow {
  /** Номер ближайшей строки с номером выше («доп» привязывается к ней). */
  parentPositionNumber: string;
  /** Колонка 2 (№ п/п) строки «доп» — должна быть пустой, номер даёт сервер. */
  columnB: string;
  /** Заполняет валидация. */
  parentPositionId?: string;
  parentPositionLabel?: string;
}

// Данные для обновления позиции заказчика
export interface PositionUpdateData {
  positionNumber: string;
  positionId?: string;
  manualVolume?: number;
  manualNote?: string;
  itemsCount: number;
}

export interface ValidationError {
  rowIndex: number;
  type: 'missing_nomenclature' | 'unit_mismatch' | 'missing_cost' | 'invalid_type' | 'missing_field' | 'binding_error' | 'position_not_found' | 'non_leaf_position' | 'additional_error';
  field: string;
  message: string;
  severity: 'error' | 'warning';
}

export interface MissingNomenclatureGroup {
  name: string;
  unit: string;
  rows: number[];
}

export interface NonLeafPositionGroup {
  positionNumber: string;
  positionName: string;
  rows: number[];
}

export interface ValidationResult {
  isValid: boolean;
  errors: ValidationError[];
  warnings: ValidationError[];
  missingNomenclature: {
    works: MissingNomenclatureGroup[];
    materials: MissingNomenclatureGroup[];
  };
  unknownCosts: Array<{ text: string; rows: number[] }>;
  unmatchedPositions: Array<{ positionNumber: string; rows: number[] }>;
  nonLeafPositions: NonLeafPositionGroup[];
}

export interface ClientPosition {
  id: string;
  position_number: number;
  work_name: string;
  hierarchy_level: number | null;
  is_additional: boolean | null;
  parent_position_id: string | null;
}

// ===========================
// ВСПОМОГАТЕЛЬНЫЕ ФУНКЦИИ
// ===========================

export const isWork = (type: string): boolean => {
  return ['раб', 'суб-раб', 'раб-комп.'].includes(type);
};

export const isMaterial = (type: string): boolean => {
  return ['мат', 'суб-мат', 'мат-комп.'].includes(type);
};

export const normalizeString = (str: string): string => {
  return str.trim().replace(/\s+/g, ' ');
};

// Нормализация для сравнения при сопоставлении: регистронезависимо + без двойных пробелов.
// Используется только для ключей поиска — исходные данные не изменяются.
export const normalizeForLookup = (str: string): string => {
  return normalizeString(str).toLowerCase();
};

export const buildNomenclatureLookupKey = (name: string, unit: string): string => {
  return `${normalizeForLookup(name)}|${normalizeForLookup(unit)}`;
};

export const parseNumber = (value: unknown): number | undefined => {
  if (value === null || value === undefined || value === '') return undefined;
  const num = typeof value === 'string' ? parseFloat(value.replace(',', '.')) : Number(value);
  return isNaN(num) ? undefined : num;
};

export const parseBoolean = (value: unknown): boolean => {
  if (!value) return false;
  const str = String(value).toLowerCase().trim();
  return str === 'да' || str === 'yes' || str === 'true' || str === '1';
};

// Нормализация номера позиции для сравнения
export const normalizePositionNumber = (value: string | number | null | undefined): string => {
  if (value === null || value === undefined || value === '') return '';

  // Приводим к строке и убираем пробелы
  const str = String(value).trim();

  // Парсим как число и обратно в строку для нормализации (5.0 -> 5, 5.10 -> 5.1)
  const num = parseFloat(str);
  if (!isNaN(num)) {
    // Если это целое число, возвращаем без дробной части
    if (Number.isInteger(num)) {
      return String(Math.floor(num));
    }
    // Иначе убираем лишние нули в конце
    return String(num);
  }

  return str;
};

// Нормализация типа материала
export const normalizeMaterialType = (value: string | undefined): 'основн.' | 'вспомогат.' | undefined => {
  if (!value) return undefined;

  const original = String(value).trim();
  const normalized = original.toLowerCase().replace(/\s+/g, '').replace(/\.$/, '');

  if (normalized === 'основной' || normalized === 'основн' || normalized === 'осн') {
    return 'основн.';
  }
  if (normalized === 'вспомогательный' || normalized === 'вспомогат' || normalized === 'вспом') {
    return 'вспомогат.';
  }
  if (original === 'основн.' || original === 'вспомогат.') {
    return original as 'основн.' | 'вспомогат.';
  }

  return undefined;
};

// Нормализация типа доставки
export const normalizeDeliveryPriceType = (value: string | undefined): 'в цене' | 'не в цене' | 'суммой' | undefined => {
  if (!value) return undefined;

  const original = String(value).trim();
  const normalized = original.toLowerCase().replace(/\s+/g, ' ');

  if (normalized === 'в цене' || normalized === 'вцене' || normalized === 'входит') {
    return 'в цене';
  }
  if (normalized === 'не в цене' || normalized === 'невцене' || normalized === 'не входит' || normalized === 'невходит') {
    return 'не в цене';
  }
  if (normalized === 'суммой' || normalized === 'доп. стоимость' || normalized === 'доп стоимость' || normalized === 'дополнительно') {
    return 'суммой';
  }
  if (original === 'в цене' || original === 'не в цене' || original === 'суммой') {
    return original as 'в цене' | 'не в цене' | 'суммой';
  }

  return undefined;
};

// Поиск ID затраты с fallback для многоуровневых названий со слэшами
// В БД cost_categories.name и detail_cost_categories.name могут содержать слэши.
// Например: category="ВИС / Слаботочные системы", detail="Пожарная сигнализация", location="Здание"
// Пробуем все возможные комбинации разбиения строки на category/detail/location.
export const findCostCategoryId = (
  text: string,
  costCategoriesMap: Map<string, string>
): string | undefined => {
  if (!text) return undefined;

  const parts = text.split(' / ').map(p => p.trim()).filter(p => p);
  if (parts.length < 2) return undefined;

  // Пробуем все возможные комбинации разбиения на category, detail, location
  // categoryParts: сколько частей отдаём на category (минимум 1)
  // locationParts: сколько частей отдаём на location (0 = пустая, или 1+)
  // Остальное уходит в detail
  for (let categoryParts = 1; categoryParts < parts.length; categoryParts++) {
    const category = normalizeString(parts.slice(0, categoryParts).join(' / '));

    // Вариант 1: location = последняя часть (или несколько частей)
    for (let locationParts = 1; locationParts <= parts.length - categoryParts; locationParts++) {
      const location = normalizeString(parts.slice(parts.length - locationParts).join(' / '));
      const detail = normalizeString(parts.slice(categoryParts, parts.length - locationParts).join(' / '));

      if (!detail) continue;

      const key = `${category}|${detail}|${location}`;
      const costId = costCategoriesMap.get(key);
      if (costId) return costId;
    }

    // Вариант 2: пустая location (всё после category = detail)
    const fullDetail = normalizeString(parts.slice(categoryParts).join(' / '));
    if (fullDetail) {
      const keyEmptyLocation = `${category}|${fullDetail}|`;
      const costIdEmptyLocation = costCategoriesMap.get(keyEmptyLocation);
      if (costIdEmptyLocation) return costIdEmptyLocation;
    }
  }

  return undefined;
};
