/**
 * Хук сохранения перераспределения (этап 0.1.2.3a: server-authoritative).
 *
 * Клиент отправляет ТОЛЬКО правила; все per-BOQ результаты рассчитывает и
 * сохраняет backend (одна транзакция: материализация commercial → расчёт →
 * инварианты → атомарная запись полного набора). Ответ сервера — единственный
 * источник подтверждённых результатов: caller обязан заменить локальный
 * preview на response.results. Никаких records / fallbackBoqItem /
 * changedResults-фильтрации / createdBy с клиента больше нет.
 */

import { useState, useCallback } from 'react';
import { message } from 'antd';
import {
  saveRedistributionResults,
  loadRedistributionResults,
  clearRedistributionResults,
  type SavedRedistribution,
  type LoadedRedistribution,
} from '../../../lib/api/redistributions';
import type { SourceRule, TargetCost } from '../utils';
import type { RedistributionRule } from '../../../lib/types';
import type { PositionAdjustmentRule } from '../types/positionAdjustment';
import { markRealtimeMutation } from '../../../lib/realtime/useRealtimeRefetch';

// Тело RFC 7807 problem+json от Go BFF.
interface ProblemBody {
  detail?: string;
  title?: string;
  code?: string;
  issues?: Array<{ field?: string; code?: string; message?: string }>;
}

export function useSaveResults() {
  const [saving, setSaving] = useState(false);

  const saveResults = useCallback(
    async (
      tenderId: string,
      tacticId: string,
      sourceRules: SourceRule[],
      targetCosts: TargetCost[],
      positionAdjustments: PositionAdjustmentRule[] = [],
    ): Promise<SavedRedistribution | null> => {
      if (!tenderId || !tacticId) {
        message.error('Не выбран тендер или тактика наценок');
        return null;
      }

      setSaving(true);
      try {
        const rules: RedistributionRule = {
          deductions: sourceRules.map((rule) => ({
            level: rule.level,
            category_id: rule.category_id,
            detail_cost_category_id: rule.detail_cost_category_id,
            category_name: rule.category_name,
            percentage: rule.percentage,
            ...(rule.boq_item_types && rule.boq_item_types.length > 0
              ? { boq_item_types: rule.boq_item_types }
              : {}),
          })),
          targets: targetCosts.map((target) => ({
            level: target.level,
            category_id: target.category_id,
            detail_cost_category_id: target.detail_cost_category_id,
            category_name: target.category_name,
          })),
          ...(positionAdjustments.length > 0
            ? {
                position_adjustments: positionAdjustments.map((rule) => ({
                  mode: rule.mode,
                  amount: rule.amount,
                  sourceIds: rule.sourceIds,
                  targetIds: rule.targetIds,
                })),
              }
            : {}),
        };

        const saved = await saveRedistributionResults({ tenderId, tacticId, rules });

        // Подавляем self-echo: запись породит NOTIFY → WS-эхо в той же вкладке.
        markRealtimeMutation(`tender:${tenderId}`);

        message.success('Результаты перераспределения сохранены');
        return saved;
      } catch (error) {
        console.error('Ошибка сохранения результатов:', error);
        const body = (error as { body?: ProblemBody }).body;
        const firstIssue = body?.issues?.[0];
        const detail =
          firstIssue?.message ||
          body?.detail ||
          (error instanceof Error ? error.message : '');
        message.error(
          detail ? `Не удалось сохранить результаты: ${detail}` : 'Не удалось сохранить результаты',
        );
        // Backend отказал → локальный preview НЕ считается сохранённым; caller
        // оставляет предыдущий подтверждённый снимок / состояние «не сохранено».
        return null;
      } finally {
        setSaving(false);
      }
    },
    [],
  );

  // Все правила удалены → удалить снимок на сервере. true = сервер подтвердил.
  const clearSavedResults = useCallback(
    async (tenderId: string, tacticId: string): Promise<boolean> => {
      if (!tenderId || !tacticId) {
        return false;
      }

      setSaving(true);
      try {
        await clearRedistributionResults(tenderId, tacticId);
        markRealtimeMutation(`tender:${tenderId}`);
        message.success('Перераспределение очищено');
        return true;
      } catch (error) {
        console.error('Ошибка очистки перераспределения:', error);
        const body = (error as { body?: ProblemBody }).body;
        const detail = body?.detail || (error instanceof Error ? error.message : '');
        message.error(
          detail ? `Не удалось очистить перераспределение: ${detail}` : 'Не удалось очистить перераспределение',
        );
        return false;
      } finally {
        setSaving(false);
      }
    },
    [],
  );

  const loadSavedResults = useCallback(
    async (tenderId: string, tacticId: string): Promise<LoadedRedistribution | null> => {
      if (!tenderId || !tacticId) {
        return null;
      }

      try {
        return await loadRedistributionResults(tenderId, tacticId);
      } catch (error) {
        console.error('Ошибка загрузки сохраненных результатов:', error);
        return null;
      }
    },
    [],
  );

  return {
    saving,
    saveResults,
    clearSavedResults,
    loadSavedResults,
  };
}
