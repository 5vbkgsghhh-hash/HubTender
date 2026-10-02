// Типы одиночного (по-позиционного) Excel-импорта BOQ.
// ВАЖНО: зеркалят типы массового импорта в ClientPositions/utils —
// параллельные реализации держат в синхроне (memory boq-import-dual-impl).

export interface ParsedBoqItem {
  rowIndex: number;

  /** Элемент блока «доп»: уходит в новую ДОП (tempId строки «доп»), а не в текущую позицию. */
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

export interface ValidationError {
  rowIndex: number;
  type: 'missing_nomenclature' | 'unit_mismatch' | 'missing_cost' | 'invalid_type' | 'missing_field' | 'binding_error' | 'additional_error';
  field: string;
  message: string;
  severity: 'error' | 'warning';
}

export interface MissingNomenclatureGroup {
  name: string;
  unit: string;
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
}

export interface CostCategoryRecord {
  id: string;
  name: string;
  location: string;
  cost_categories?: { name: string } | { name: string }[] | null;
}
