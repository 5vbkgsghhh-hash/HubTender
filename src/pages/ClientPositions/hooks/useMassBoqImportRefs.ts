import { useState } from 'react';
import {
  listWorkNames,
  listMaterialNames,
  listActiveUnits,
} from '../../../lib/api/nomenclatures';
import { listDetailCostCategoriesWithCategory } from '../../../lib/api/costs';
import { fetchPositionsWithCosts, listBoqPreviewByPositions } from '../../../lib/api/positions';
import { computeLeafPositionIds } from '../../../utils/positions/leafPositions';
import {
  ClientPosition,
  normalizeString,
  normalizeForLookup,
  buildNomenclatureLookupKey,
  normalizePositionNumber,
} from '../utils';
import type { ExistingAdditionalRef } from '../utils/massBoqImportAdditional';

export type ExistingBoqPreviewItem = {
  id: string;
  work_names?: { name?: string } | null;
  material_names?: { name?: string } | null;
  boq_item_type?: string | null;
  quantity?: number | null;
  total_amount?: number | null;
  client_position_id: string;
};

/**
 * Справочники массового BOQ-импорта: номенклатура, категории затрат,
 * позиции тендера, единицы измерения, курсы валют и предпросмотр
 * существующих BOQ items. Перенесено из useMassBoqImport без изменений логики.
 */
export const useMassBoqImportRefs = () => {
  // Справочники
  const [workNamesMap, setWorkNamesMap] = useState<Map<string, string>>(new Map());
  const [materialNamesMap, setMaterialNamesMap] = useState<Map<string, string>>(new Map());
  const [costCategoriesMap, setCostCategoriesMap] = useState<Map<string, string>>(new Map());
  const [clientPositionsMap, setClientPositionsMap] = useState<Map<string, ClientPosition>>(new Map());
  const [leafPositionIds, setLeafPositionIds] = useState<Set<string>>(new Set());
  // Для строк «доп»: родитель существующей ДОП и уже имеющиеся ДОП позиций (запрет дублей).
  const [positionsById, setPositionsById] = useState<Map<string, ClientPosition>>(new Map());
  const [existingAdditionalByParent, setExistingAdditionalByParent] =
    useState<Map<string, ExistingAdditionalRef[]>>(new Map());

  // Единицы измерения — для маппинга
  const [availableUnits, setAvailableUnits] = useState<{ code: string; name: string }[]>([]);

  // Существующие BOQ items по позициям (для предпросмотра)
  const [existingItemsByPosition, setExistingItemsByPosition] = useState<Map<string, ExistingBoqPreviewItem[]>>(new Map());

  const loadNomenclature = async (tenderId: string) => {
    try {
      const [
        worksData,
        materialsData,
        costsRows,
        positionsData,
        unitsRows,
      ] = await Promise.all([
        listWorkNames(),
        listMaterialNames(),
        listDetailCostCategoriesWithCategory(),
        fetchPositionsWithCosts(tenderId),
        listActiveUnits(),
      ]);

      // cost_categories!inner — оставляем только dcc с привязанной категорией.
      const costsData = costsRows.filter((c) => c.cost_categories != null);
      const unitsResult = {
        data: unitsRows
          .slice()
          .sort((a, b) => (a.code || '').localeCompare(b.code || '')),
      };

      const worksMap = new Map<string, string>();
      worksData.forEach((w) => {
        worksMap.set(buildNomenclatureLookupKey(w.name, w.unit), w.id);
      });

      const materialsMap = new Map<string, string>();
      materialsData.forEach((m) => {
        materialsMap.set(buildNomenclatureLookupKey(m.name, m.unit), m.id);
      });

      const costsMap = new Map<string, string>();
      costsData.forEach((c) => {
        const cc = Array.isArray(c.cost_categories) ? c.cost_categories[0] : c.cost_categories;
        const costCategoryName = cc?.name || '';
        costsMap.set(
          `${normalizeString(costCategoryName)}|${normalizeString(c.name)}|${normalizeString(c.location)}`,
          c.id
        );
      });

      console.log('[MassBoqImport] Затраты ВИС в БД:',
        Array.from(costsMap.keys()).filter(k => k.toLowerCase().startsWith('вис')).slice(0, 30)
      );

      const positionsMap = new Map<string, ClientPosition>();
      const byId = new Map<string, ClientPosition>();
      const additionalByParent = new Map<string, ExistingAdditionalRef[]>();
      positionsData.forEach((p) => {
        const normalizedNum = normalizePositionNumber(p.position_number);
        const position: ClientPosition = {
          id: p.id,
          position_number: Number(p.position_number),
          work_name: p.work_name ?? '',
          hierarchy_level: p.hierarchy_level,
          is_additional: p.is_additional,
          parent_position_id: p.parent_position_id,
        };
        positionsMap.set(normalizedNum, position);
        byId.set(p.id, position);
        if (p.is_additional && p.parent_position_id) {
          const refs = additionalByParent.get(p.parent_position_id) ?? [];
          refs.push({ key: normalizeForLookup(position.work_name), number: normalizedNum, workName: position.work_name });
          additionalByParent.set(p.parent_position_id, refs);
        }
      });

      const leafIds = computeLeafPositionIds(positionsData);

      console.log('[MassBoqImport] Первые 20 позиций в БД:',
        Array.from(positionsMap.entries()).slice(0, 20).map(([key, val]) =>
          `${key} (raw: ${val.position_number})`
        )
      );

      setWorkNamesMap(worksMap);
      setMaterialNamesMap(materialsMap);
      setCostCategoriesMap(costsMap);
      setClientPositionsMap(positionsMap);
      setPositionsById(byId);
      setExistingAdditionalByParent(additionalByParent);
      setLeafPositionIds(leafIds);
      setAvailableUnits((unitsResult.data || []) as { code: string; name: string }[]);

      console.log('[MassBoqImport] Загружено справочников:', {
        works: worksMap.size,
        materials: materialsMap.size,
        costs: costsMap.size,
        positions: positionsMap.size,
      });

      return true;
    } catch (error) {
      console.error('Ошибка загрузки справочников:', error);
      return false;
    }
  };

  const loadExistingItems = async (positionIds: string[]) => {
    if (positionIds.length === 0) return;
    const data = await listBoqPreviewByPositions(positionIds);

    const map = new Map<string, ExistingBoqPreviewItem[]>();
    data?.forEach((item) => {
      if (!map.has(item.client_position_id)) map.set(item.client_position_id, []);
      map.get(item.client_position_id)!.push(item as unknown as ExistingBoqPreviewItem);
    });
    setExistingItemsByPosition(map);
  };

  // reset() основного хука чистит только предпросмотр существующих items.
  const resetRefs = () => {
    setExistingItemsByPosition(new Map());
  };

  return {
    workNamesMap,
    materialNamesMap,
    costCategoriesMap,
    clientPositionsMap,
    leafPositionIds,
    positionsById,
    existingAdditionalByParent,
    availableUnits,
    existingItemsByPosition,
    loadNomenclature,
    loadExistingItems,
    resetRefs,
  };
};
