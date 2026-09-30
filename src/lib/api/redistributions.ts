import type { RedistributionRule } from '../types';
import { apiFetch } from './client';

// Строка снимка перераспределения. С этапа 0.1.2.3a — ВСЕГДА server-generated:
// клиент не передаёт финансовые значения, endpoint save принимает только rules.
export interface RedistributionRecord {
  boq_item_id: string;
  original_work_cost: number;
  deducted_amount: number;
  added_amount: number;
  final_work_cost: number;
}

// Статус сохранённого снимка (GET):
//  - calculated — server-authoritative; prepared-проекция доступна;
//  - requires_recalculation — legacy клиентский снимок ЛИБО server-снимок,
//    входы которого изменились: results/prepared нельзя применять в
//    Commerce/FI/экспортах, нужен новый серверный save;
//  - not_configured — перераспределение ещё не рассчитано.
export type RedistributionSnapshotStatus =
  | 'calculated'
  | 'requires_recalculation'
  | 'not_configured';

// Server-generated prepared-проекция (этап 0.1.2.3b): position adjustments,
// insurance, rounding, финальные строки и summary считает ТОЛЬКО
// backend/internal/calc. Клиент отображает эти значения и не пересчитывает их.
export interface PreparedServerRow {
  position_id: string;
  position_number: number;
  section_number: string | null;
  position_name: string;
  item_no: string | null;
  work_name: string;
  client_volume: number | null;
  manual_volume: number | null;
  unit_code: string;
  manual_note: string | null;
  is_additional: boolean;
  is_leaf: boolean;
  quantity: number;
  material_cost: number;
  work_cost_before: number;
  category_deducted: number;
  category_added: number;
  work_cost_after_category: number;
  position_deducted: number;
  position_added: number;
  work_cost_after_adjustments: number;
  rounded_material_unit_price: number;
  rounded_material_cost: number;
  rounded_work_unit_price: number;
  rounding_adjustment: number;
  work_cost_rounded: number;
  insurance_amount: number;
  final_work_cost: number;
  final_work_unit_price: number;
  final_position_total: number;
}

export interface PreparedServerSummary {
  total_material_cost: number;
  work_total_before_category: number;
  total_category_deducted: number;
  total_category_added: number;
  work_total_after_category: number;
  total_position_deducted: number;
  total_position_added: number;
  work_total_after_adjustments: number;
  rounding_adjustment_total: number;
  work_total_rounded_pre_insurance: number;
  insurance_total: number;
  insurance_allocated: number;
  final_work_total: number;
  final_total: number;
  is_category_balanced: boolean;
  is_insurance_fully_allocated: boolean;
}

export interface PreparedServerRedistribution {
  rows: PreparedServerRow[];
  summary: PreparedServerSummary;
  rounding_policy: string;
  prepared_schema_version: number;
  calculation_source: string;
}

export interface SaveRedistributionInput {
  tenderId: string;
  tacticId: string;
  rules: RedistributionRule;
}

export interface SavedRedistribution {
  saved_count: number;
  results: RedistributionRecord[];
  total_deducted: number;
  total_added: number;
  is_balanced: boolean;
  redistribution_rules: RedistributionRule | null;
  calculation_source: string;
  schema_version: number;
  position_deltas?: Record<string, number>;
  prepared?: PreparedServerRedistribution;
}

export interface LoadedRedistribution {
  results: RedistributionRecord[];
  redistribution_rules: RedistributionRule | null;
  status: RedistributionSnapshotStatus;
  // Стабильный reason-код для requires_recalculation (LEGACY_SNAPSHOT /
  // SNAPSHOT_SET_MISMATCH / PREPARED_INPUT_CHANGED /
  // INSURANCE_ALLOCATION_INVALID / PREPARED_CALCULATION_FAILED).
  // Frontend ветвится по коду, не по тексту.
  reason?: string;
  message?: string;
  prepared?: PreparedServerRedistribution;
}

/**
 * Загрузить сохранённый снимок перераспределения для (tenderId, tacticId).
 * Go: GET /api/v1/redistributions?tender_id=&markup_tactic_id= — строки
 * результата + rules JSONB + status. Возвращает null, если результатов нет.
 */
export async function loadRedistributionResults(
  tenderId: string,
  tacticId: string,
): Promise<LoadedRedistribution | null> {
  const res = await apiFetch<{
    data: {
      results: RedistributionRecord[];
      redistribution_rules: RedistributionRule | null;
      status: RedistributionSnapshotStatus;
      reason?: string;
      message?: string;
      prepared?: PreparedServerRedistribution;
    };
  }>(
    `/api/v1/redistributions?tender_id=${encodeURIComponent(tenderId)}&markup_tactic_id=${encodeURIComponent(tacticId)}`,
  );
  if (!res.data || res.data.results.length === 0) return null; // not_configured
  return {
    results: res.data.results,
    redistribution_rules: res.data.redistribution_rules ?? null,
    status: res.data.status ?? 'requires_recalculation',
    reason: res.data.reason,
    message: res.data.message,
    prepared: res.data.prepared,
  };
}

/**
 * Сохранить перераспределение. Этап 0.1.2.3a: клиент передаёт ТОЛЬКО правила
 * (deductions/targets/position_adjustments). Сервер в одной транзакции
 * материализует текущие commercial-стоимости, загружает весь BOQ тендера,
 * валидирует правила, считает результат через backend/internal/calc и
 * атомарно сохраняет ПОЛНЫЙ server-generated набор. Ответ содержит серверные
 * results — caller обязан заменить ими локальный preview.
 *
 * Никакие records / original_work_cost / deducted_amount / added_amount /
 * final_work_cost с клиента не отправляются и сервером не принимаются.
 */
export async function saveRedistributionResults(
  input: SaveRedistributionInput,
): Promise<SavedRedistribution> {
  const { tenderId, tacticId, rules } = input;

  const res = await apiFetch<{ data: SavedRedistribution }>(
    '/api/v1/redistributions/save',
    {
      method: 'POST',
      body: JSON.stringify({
        tender_id: tenderId,
        markup_tactic_id: tacticId,
        rules,
      }),
      timeoutMs: 0,
    },
  );
  return res.data;
}

/**
 * Удалить сохранённый снимок перераспределения (все правила удалены).
 * Go: DELETE /api/v1/redistributions?tender_id=&markup_tactic_id=. Save с
 * пустыми правилами сервер отклоняет (RULES_EMPTY) — очистка идёт отдельно.
 * Идемпотентно: отсутствующий снимок → deleted_count = 0.
 */
export async function clearRedistributionResults(
  tenderId: string,
  tacticId: string,
): Promise<number> {
  const res = await apiFetch<{ data: { deleted_count: number } }>(
    `/api/v1/redistributions?tender_id=${encodeURIComponent(tenderId)}&markup_tactic_id=${encodeURIComponent(tacticId)}`,
    { method: 'DELETE', timeoutMs: 0 },
  );
  return res.data.deleted_count;
}
