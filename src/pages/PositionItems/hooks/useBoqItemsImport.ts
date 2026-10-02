import { useState } from 'react';
import * as XLSX from 'xlsx';
import { message } from 'antd';
import { apiFetch } from '../../../lib/api/client';
import {
  listWorkNames,
  listMaterialNames,
  listActiveUnits,
  createWorkName,
  createMaterialName,
} from '../../../lib/api/nomenclatures';
import { listDetailCostCategoriesWithCategory } from '../../../lib/api/costs';
import { getErrorMessage } from '../../../utils/errors';
import {
  normalizeString,
  buildNomenclatureLookupKey,
} from '../../../utils/boq/importShared';
import { buildBoqItemsPayload } from '../../../utils/boq/importPayload';
import {
  buildAdditionalPositionPayload,
  formatCreatedAdditionalNumbers,
  type CreatedAdditionalPosition,
  type ParsedAdditionalRow,
} from '../../../utils/boq/additionalImport';
import { buildMissingNomenclatureInserts } from '../../../utils/boq/nomenclatureImport';
import type { ParsedBoqItem, ValidationResult, CostCategoryRecord } from '../utils/boqImportTypes';
import { parseBoqExcelRows, processWorkBindings as processWorkBindingsUtil } from '../utils/boqImportParser';
import { validateBoqData } from '../utils/boqImportValidation';

// Типы/парсер/валидация вынесены в ../utils/boqImport* (лимит ≤600 строк),
// общие с mass-импортом хелперы — в src/utils/boq/*.

// ===========================
// ОСНОВНОЙ ХУК
// ===========================

export const useBoqItemsImport = () => {
  const [parsedData, setParsedData] = useState<ParsedBoqItem[]>([]);
  // Строки «доп» файла и ДОП, созданные сервером по итогам импорта.
  const [additionalRows, setAdditionalRows] = useState<ParsedAdditionalRow[]>([]);
  const [createdAdditional, setCreatedAdditional] = useState<CreatedAdditionalPosition[]>([]);
  const [fileName, setFileName] = useState<string>('');
  const [validationResult, setValidationResult] = useState<ValidationResult | null>(null);
  const [uploading, setUploading] = useState(false);
  const [uploadProgress, setUploadProgress] = useState(0);
  // Явный статус результата импорта — чтобы не выводить «успех» из uploadProgress.
  const [importStatus, setImportStatus] = useState<'idle' | 'running' | 'success' | 'error'>('idle');
  const [importError, setImportError] = useState<string | null>(null);

  // Справочники
  const [workNamesMap, setWorkNamesMap] = useState<Map<string, string>>(new Map());
  const [materialNamesMap, setMaterialNamesMap] = useState<Map<string, string>>(new Map());
  const [costCategoriesMap, setCostCategoriesMap] = useState<Map<string, string>>(new Map());
  const [unitCodes, setUnitCodes] = useState<Set<string>>(new Set());

  // ===========================
  // ЗАГРУЗКА СПРАВОЧНИКОВ
  // ===========================

  const loadNomenclature = async () => {
    try {
      {
      const [allWorks, allMaterials, allCostsRaw, units] = await Promise.all([
        listWorkNames(),
        listMaterialNames(),
        listDetailCostCategoriesWithCategory(),
        listActiveUnits(),
      ]);
      // cost_categories!inner — оставляем только dcc с привязанной категорией.
      const allCosts = (allCostsRaw as unknown as CostCategoryRecord[])
        .filter((c) => c.cost_categories != null);

      const nextWorksMap = new Map<string, string>();
      allWorks.forEach((work) => {
        nextWorksMap.set(buildNomenclatureLookupKey(work.name, work.unit), work.id);
      });

      const nextMaterialsMap = new Map<string, string>();
      allMaterials.forEach((material) => {
        nextMaterialsMap.set(buildNomenclatureLookupKey(material.name, material.unit), material.id);
      });

      const nextCostsMap = new Map<string, string>();
      let costLogCount = 0;
      allCosts.forEach((cost) => {
        const cc = Array.isArray(cost.cost_categories) ? cost.cost_categories[0] : cost.cost_categories;
        const costCategoryName = cc?.name || '';
        const key = `${normalizeString(costCategoryName)}|${normalizeString(cost.name)}|${normalizeString(cost.location)}`;
        nextCostsMap.set(key, cost.id);

        const fullPath = normalizeString(`${costCategoryName} / ${cost.name} / ${cost.location}`);
        nextCostsMap.set(fullPath, cost.id);

        if (costLogCount < 5 || cost.name.includes('/') || costCategoryName.includes('/')) {
          console.log('[CostCategory] Загружена затрата:', {
            category: costCategoryName,
            detail: cost.name,
            location: cost.location,
            key,
            fullPath,
          });
          costLogCount++;
        }
      });

      setWorkNamesMap(nextWorksMap);
      setMaterialNamesMap(nextMaterialsMap);
      setCostCategoriesMap(nextCostsMap);
      setUnitCodes(new Set(units.map((u) => u.code)));

      console.log('[BoqImport] Загружено справочников:', {
        works: nextWorksMap.size,
        materials: nextMaterialsMap.size,
        costs: nextCostsMap.size,
      });

      return true;
      }
    } catch (error) {
      console.error('Ошибка загрузки справочников:', error);
      return false;
    }
  };

  // ===========================
  // ПАРСИНГ EXCEL
  // ===========================

  const parseExcelFile = async (file: File): Promise<boolean> => {
    setFileName(file.name);
    return new Promise((resolve) => {
      const reader = new FileReader();

      reader.onload = async (e) => {
        try {
          const data = e.target?.result;
          const workbook = XLSX.read(data, { type: 'binary' });
          const firstSheet = workbook.Sheets[workbook.SheetNames[0]];
          const jsonData = XLSX.utils.sheet_to_json<unknown[]>(firstSheet, { header: 1 });

          // Пропускаем заголовок (первая строка)
          const rows = jsonData.slice(1);

          const { items: parsed, additionalRows: dops } = parseBoqExcelRows(rows);

          setParsedData(parsed);
          setAdditionalRows(dops);

          // ЛОГИРОВАНИЕ: Показываем порядок элементов после парсинга
          console.log('=== ПАРСИНГ EXCEL ЗАВЕРШЁН ===');
          console.log(`Всего строк: ${parsed.length}, строк «доп»: ${dops.length}`);
          console.log('Первые 10 элементов из файла (в порядке чтения):');
          parsed.slice(0, 10).forEach((item, idx) => {
            console.log(`  ${idx}: [Строка ${item.rowIndex}] ${item.nameText} (${item.boq_item_type})`);
          });

          // Сразу запускаем валидацию
          const validation = validateParsedData(parsed, dops);
          setValidationResult(validation);

          message.success(
            `Файл обработан: ${parsed.length} строк${dops.length > 0 ? `, новых ДОП: ${dops.length}` : ''}`,
          );
          resolve(true);
        } catch (error) {
          console.error('Ошибка парсинга Excel:', error);
          message.error('Ошибка при чтении файла Excel');
          resolve(false);
        }
      };

      reader.onerror = () => {
        message.error('Ошибка чтения файла');
        resolve(false);
      };

      reader.readAsBinaryString(file);
    });
  };

  // ===========================
  // ВАЛИДАЦИЯ (тонкая обёртка над чистой validateBoqData)
  // ===========================

  const validateParsedData = (
    data: ParsedBoqItem[],
    dops: ParsedAdditionalRow[] = additionalRows,
  ): ValidationResult => {
    const result = validateBoqData(data, { workNamesMap, materialNamesMap, costCategoriesMap, unitCodes }, dops);
    setValidationResult(result);
    return result;
  };

  // ===========================
  // ОБРАБОТКА ПРИВЯЗОК (вынесена в ../utils/boqImportParser)
  // ===========================

  const processWorkBindings = processWorkBindingsUtil;

  // ===========================
  // ВСТАВКА В БД
  // ===========================

  /**
   * Тот же атомарный POST /api/v1/imports/boq, что у массового импорта: одна
   * транзакция (ДОП из строк «доп» создаются вместе с элементами), суммы
   * считает сервер, сессия попадает в журнал импортов — её можно отменить.
   * additionalParentId — позиция, к которой создаются ДОП (у ДОП — её родитель).
   */
  const insertBoqItems = async (
    data: ParsedBoqItem[],
    positionId: string,
    tenderId: string,
    additionalParentId: string,
  ): Promise<boolean> => {
    try {
      setUploading(true);
      setUploadProgress(5);
      setImportStatus('running');
      setImportError(null);
      setCreatedAdditional([]);

      const itemsPayload = buildBoqItemsPayload(
        data.map((item) => ({ ...item, matchedPositionId: item.additionalTempId ? undefined : positionId })),
      );
      const additionalPayload = additionalRows.map((row) => buildAdditionalPositionPayload(row, additionalParentId));

      setUploadProgress(15);

      const resp = await apiFetch<{
        inserted_items_count: number;
        created_additional_positions?: CreatedAdditionalPosition[];
      }>('/api/v1/imports/boq', {
        method: 'POST',
        timeoutMs: 0,
        body: JSON.stringify({
          tender_id: tenderId,
          file_name: fileName || 'import.xlsx',
          items: itemsPayload,
          position_updates: [],
          additional_positions: additionalPayload,
        }),
      });
      const created = resp.created_additional_positions ?? [];
      setCreatedAdditional(created);
      setUploadProgress(100);

      // Сервер атомарен и вставляет ровно отправленное — расхождение = потеря данных.
      if (resp.inserted_items_count !== itemsPayload.length || created.length !== additionalPayload.length) {
        const mismatchMsg =
          `Импортировано ${resp.inserted_items_count} из ${itemsPayload.length} элементов, ` +
          `создано ${created.length} из ${additionalPayload.length} ДОП — проверьте позицию.`;
        message.error(mismatchMsg, 10);
        setImportError(mismatchMsg);
        setImportStatus('error');
        return false;
      }

      message.success(
        `Успешно импортировано ${resp.inserted_items_count} элементов` +
        (created.length > 0 ? `, создано ДОП: ${created.length} (${formatCreatedAdditionalNumbers(created)})` : ''),
      );
      setImportStatus('success');
      return true;
    } catch (error) {
      // Ошибки данных сервер возвращает с номером строки Excel («Строка N: …»).
      const detail = getErrorMessage(error);
      console.error('Ошибка импорта:', error);
      setImportError(detail);
      setImportStatus('error');
      message.error('Ошибка при импорте: ' + detail);
      return false;
    } finally {
      setUploading(false);
      setUploadProgress(0);
    }
  };

  // ===========================
  // ПУБЛИЧНЫЙ API
  // ===========================

  const addMissingToNomenclature = async (): Promise<boolean> => {
    if (!validationResult) return false;

    const { works, materials } = validationResult.missingNomenclature;
    if (works.length === 0 && materials.length === 0) {
      return true;
    }

    try {
      setUploading(true);

      const existingWorkKeys = new Set(workNamesMap.keys());
      const existingMaterialKeys = new Set(materialNamesMap.keys());

      const uniqueWorksToInsert = buildMissingNomenclatureInserts(works, existingWorkKeys);
      const uniqueMaterialsToInsert = buildMissingNomenclatureInserts(materials, existingMaterialKeys);

      if (uniqueWorksToInsert.length > 0) {
        await Promise.all(
          uniqueWorksToInsert.map((wkr) => createWorkName({ name: wkr.name, unit: wkr.unit })),
        );
      }

      if (uniqueMaterialsToInsert.length > 0) {
        await Promise.all(
          uniqueMaterialsToInsert.map((m) => createMaterialName({ name: m.name, unit: m.unit })),
        );
      }

      await loadNomenclature();

      const total = uniqueWorksToInsert.length + uniqueMaterialsToInsert.length;
      if (total > 0) {
        message.success(`Добавлено в номенклатуру: ${total} записей. Теперь нажмите «Загрузить».`);
      } else {
        message.info('Подходящие записи уже есть в номенклатуре. Теперь нажмите «Загрузить».');
      }

      return true;
    } catch (error) {
      message.error(getErrorMessage(error));
      return false;
    } finally {
      setUploading(false);
    }
  };

  const reset = () => {
    setParsedData([]);
    setAdditionalRows([]);
    setCreatedAdditional([]);
    setFileName('');
    setValidationResult(null);
    setUploadProgress(0);
    setImportStatus('idle');
    setImportError(null);
  };

  return {
    // Данные
    parsedData,
    additionalRows,
    createdAdditional,
    validationResult,
    uploading,
    uploadProgress,
    importStatus,
    importError,

    // Методы
    loadNomenclature,
    parseExcelFile,
    validateParsedData,
    processWorkBindings,
    insertBoqItems,
    addMissingToNomenclature,
    reset,
  };
};
