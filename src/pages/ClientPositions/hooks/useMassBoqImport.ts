import { useMemo, useState } from 'react';
import * as XLSX from 'xlsx';
import { message } from 'antd';
import { apiFetch } from '../../../lib/api/client';
import { createWorkName, createMaterialName } from '../../../lib/api/nomenclatures';
import {
  ParsedBoqItem,
  ParsedAdditionalPosition,
  PositionUpdateData,
  ValidationResult,
  parseExcelData,
  validateBoqData,
  processWorkBindings,
  normalizePositionNumber,
} from '../utils';
import {
  buildPositionUpdatesPayload,
  buildBoqItemsPayload,
  buildAdditionalPositionsPayload,
  analyzeImportMismatch,
  ImportTotalMismatch,
} from '../utils/massBoqImportPayload';
import { buildMissingNomenclatureInserts } from '../../../utils/boq/nomenclatureImport';
import {
  formatCreatedAdditionalNumbers,
  type CreatedAdditionalPosition,
} from '../../../utils/boq/additionalImport';
import { useMassBoqImportRefs } from './useMassBoqImportRefs';
import { getErrorMessage } from '../../../utils/errors';

// Справочники вынесены в useMassBoqImportRefs, payload-билдеры — в
// utils/massBoqImportPayload (лимит ≤600 строк на файл).

// ===========================
// ОСНОВНОЙ ХУК
// ===========================

export const useMassBoqImport = () => {
  const [parsedData, setParsedData] = useState<ParsedBoqItem[]>([]);
  const [positionUpdates, setPositionUpdates] = useState<Map<string, PositionUpdateData>>(new Map());
  // Строки «доп» файла и ДОП, созданные сервером по итогам импорта.
  const [additionalPositions, setAdditionalPositions] = useState<ParsedAdditionalPosition[]>([]);
  const [createdAdditional, setCreatedAdditional] = useState<CreatedAdditionalPosition[]>([]);
  const [validationResult, setValidationResult] = useState<ValidationResult | null>(null);
  const [uploading, setUploading] = useState(false);
  const [uploadProgress, setUploadProgress] = useState(0);
  // Явный статус результата импорта — чтобы не выводить «успех» из uploadProgress.
  const [importStatus, setImportStatus] = useState<'idle' | 'running' | 'success' | 'error'>('idle');
  const [importError, setImportError] = useState<string | null>(null);
  // Диагностический отчёт сервера: расхождения legacy total_amount vs server calc.
  const [importMismatches, setImportMismatches] = useState<ImportTotalMismatch[]>([]);
  const [fileName, setFileName] = useState<string>('');
  const [unitMappings, setUnitMappings] = useState<Record<string, string>>({});

  const {
    workNamesMap,
    materialNamesMap,
    costCategoriesMap,
    clientPositionsMap,
    leafPositionIds,
    positionsById,
    existingAdditionalByParent,
    availableUnits,
    existingItemsByPosition,
    loadNomenclature: loadNomenclatureRefs,
    loadExistingItems,
    resetRefs,
  } = useMassBoqImportRefs();

  // ===========================
  // ЗАГРУЗКА СПРАВОЧНИКОВ
  // ===========================

  const loadNomenclature = async (tenderId: string) => {
    const ok = await loadNomenclatureRefs(tenderId);
    if (ok) {
      setUnitMappings({});
    }
    return ok;
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

          const rows = jsonData.slice(1);
          const { parsed, posUpdates, additionalPositions: dops } = parseExcelData(rows);

          setParsedData(parsed);
          setPositionUpdates(posUpdates);
          setAdditionalPositions(dops);

          const positionOnlyCount = Array.from(posUpdates.values()).filter(
            p => p.itemsCount === 0 && (p.manualVolume !== undefined || p.manualNote !== undefined)
          ).length;

          const parts: string[] = [];
          if (parsed.length > 0) {
            parts.push(`${parsed.length} элементов BOQ`);
          }
          if (positionOnlyCount > 0) {
            parts.push(`${positionOnlyCount} позиций с данными ГП`);
          }
          if (dops.length > 0) {
            parts.push(`${dops.length} новых ДОП`);
          }
          message.success(`Файл обработан: ${parts.join(', ')} в ${posUpdates.size} позициях`);
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
  // ВАЛИДАЦИЯ (обёртка)
  // ===========================

  const validateParsedData = (data: ParsedBoqItem[]): ValidationResult => {
    const result = validateBoqData(data, positionUpdates, {
      clientPositionsMap,
      workNamesMap,
      materialNamesMap,
      costCategoriesMap,
      leafPositionIds,
      positionsById,
      existingAdditionalByParent,
      unitCodes: new Set(availableUnits.map(u => u.code)),
    }, additionalPositions);
    setValidationResult(result);
    return result;
  };

  // ===========================
  // ВСТАВКА В БД
  // ===========================

  const insertBoqItems = async (
    data: ParsedBoqItem[],
    tenderId: string,
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    userId?: string,
  ): Promise<boolean> => {
    try {
      setUploading(true);
      setUploadProgress(5);
      setImportStatus('running');
      setImportError(null);
      setImportMismatches([]);
      setCreatedAdditional([]);

      const positionUpdatesPayload = buildPositionUpdatesPayload(positionUpdates);

      // Этап 0-F1: клиент больше не считает total_amount — курсы и расчёт
      // каждой строки выполняет сервер (calc) по фактическим курсам тендера.
      const itemsPayload = buildBoqItemsPayload(data);
      // Строки «доп»: сервер создаёт ДОП в той же транзакции до вставки элементов.
      const additionalPayload = buildAdditionalPositionsPayload(additionalPositions);

      setUploadProgress(15);

      let insertedItemsCount = 0;
      let updatedPositionsCount = 0;
      let importSessionId: string | null = null;
      let totalMismatches: ImportTotalMismatch[] = [];
      let created: CreatedAdditionalPosition[] = [];

      {
        // Go BFF: один pgx.Tx, audit в той же транзакции, user_id из JWT
        // (не из body). Bulk-импорт может быть объёмным → timeoutMs:0
        // (отключаем дефолтный 10s-таймаут apiFetch).
        const goResp = await apiFetch<{
          import_session_id: string | null;
          inserted_items_count: number;
          updated_positions_count: number;
          total_mismatch_count?: number;
          total_mismatches?: ImportTotalMismatch[];
          created_additional_positions?: CreatedAdditionalPosition[];
        }>('/api/v1/imports/boq', {
          method: 'POST',
          timeoutMs: 0,
          body: JSON.stringify({
            tender_id: tenderId,
            file_name: fileName || '',
            items: itemsPayload,
            position_updates: positionUpdatesPayload,
            additional_positions: additionalPayload,
          }),
        });
        insertedItemsCount = goResp.inserted_items_count;
        updatedPositionsCount = goResp.updated_positions_count;
        importSessionId = goResp.import_session_id;
        totalMismatches = goResp.total_mismatches ?? [];
        created = goResp.created_additional_positions ?? [];
      }
      setImportMismatches(totalMismatches);
      setCreatedAdditional(created);

      setUploadProgress(100);

      const analysis = analyzeImportMismatch(
        insertedItemsCount,
        updatedPositionsCount,
        itemsPayload.length,
        positionUpdatesPayload.length,
        data,
        created.length,
        additionalPayload.length,
      );

      console.log('[MassBoqImport] Импорт завершён:', {
        boqItems: insertedItemsCount,
        expectedItems: analysis.expectedItems,
        positionUpdates: updatedPositionsCount,
        expectedPositions: analysis.expectedPositions,
        droppedItems: analysis.droppedItems,
        sessionId: importSessionId,
      });

      if (analysis.mismatch) {
        console.error('[MassBoqImport] Расхождение количеств при импорте:', {
          insertedItemsCount, expectedItems: analysis.expectedItems,
          updatedPositionsCount, expectedPositions: analysis.expectedPositions,
          droppedItems: analysis.droppedItems, droppedRows: analysis.droppedRows,
        });
        message.error(analysis.mismatchMsg, 10);
        // Частичная загрузка трактуется как ошибка: модалка не закрывается,
        // показываем расхождение, пользователь проверяет позиции.
        setImportError(analysis.mismatchMsg);
        setImportStatus('error');
        return false;
      }

      const msgParts: string[] = [];
      if (insertedItemsCount > 0) {
        msgParts.push(`${insertedItemsCount} элементов`);
      }
      if (updatedPositionsCount > 0) {
        msgParts.push(`обновлено ${updatedPositionsCount} позиций`);
      }
      if (created.length > 0) {
        msgParts.push(`создано ДОП: ${created.length} (${formatCreatedAdditionalNumbers(created)})`);
      }
      message.success(`Импортировано: ${msgParts.join(', ')}`);
      if (totalMismatches.length > 0) {
        // Диагностика, не ошибка: в БД сохранены серверные суммы.
        message.warning(
          `Суммы ${totalMismatches.length} строк(и) пересчитаны сервером — ` +
          'значения из файла отличались (см. таблицу расхождений)',
          8,
        );
      }
      setImportStatus('success');
      return true;
    } catch (error) {
      // Расшифровываем структурно: apiFetch кладёт причину (RFC 7807 detail)
      // в error.message, а статус и тело — в error.status / error.body.
      const e = error as { status?: number; body?: { detail?: string; title?: string } };
      const detail = getErrorMessage(error);
      console.error('Ошибка импорта:', {
        status: e?.status,
        message: detail,
        body: e?.body,
      });
      message.error('Ошибка импорта: ' + detail, 8);
      setImportError(detail);
      setImportStatus('error');
      return false;
    } finally {
      setUploading(false);
      setUploadProgress(0);
    }
  };

  // ===========================
  // ДОБАВЛЕНИЕ В НОМЕНКЛАТУРУ
  // ===========================

  const addMissingToNomenclature = async (tenderId: string): Promise<boolean> => {
    if (!validationResult) return false;
    const { works, materials } = validationResult.missingNomenclature;
    if (works.length === 0 && materials.length === 0) return true;

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

      await loadNomenclature(tenderId);

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

  // ===========================
  // МАППИНГ ЕДИНИЦ ИЗМЕРЕНИЯ
  // ===========================

  // Единицы из Excel, отсутствующие в units таблице (элементы и строки «доп»)
  const getUnknownUnits = (): string[] => {
    if ((!parsedData.length && !additionalPositions.length) || !availableUnits.length) return [];
    const dbCodes = new Set(availableUnits.map(u => u.code));
    const unknown = new Set<string>();
    [...parsedData.map(item => item.unit_code), ...additionalPositions.map(dop => dop.unitCode)]
      .forEach(code => {
        if (code && !dbCodes.has(code)) {
          unknown.add(code);
        }
      });
    return Array.from(unknown).sort();
  };

  const setUnitMapping = (excelUnit: string, dbUnit: string) => {
    setUnitMappings(prev => ({ ...prev, [excelUnit]: dbUnit }));
  };

  // Применить маппинг — обновить unit_code во всех элементах parsedData
  const applyUnitMappings = (): boolean => {
    const unknowns = getUnknownUnits();
    if (unknowns.length === 0) return true;
    const unmapped = unknowns.filter(u => !unitMappings[u]);
    if (unmapped.length > 0) {
      message.warning(`Не все единицы сопоставлены: ${unmapped.join(', ')}`);
      return false;
    }
    setParsedData(prev => prev.map(item => ({
      ...item,
      unit_code: unitMappings[item.unit_code] || item.unit_code,
    })));
    setAdditionalPositions(prev => prev.map(dop => ({
      ...dop,
      unitCode: unitMappings[dop.unitCode] || dop.unitCode,
    })));
    return true;
  };

  // ===========================
  // ПУБЛИЧНЫЙ API
  // ===========================

  const reset = () => {
    setParsedData([]);
    setPositionUpdates(new Map());
    setAdditionalPositions([]);
    setCreatedAdditional([]);
    setValidationResult(null);
    setUploadProgress(0);
    setImportStatus('idle');
    setImportError(null);
    setImportMismatches([]);
    resetRefs();
    setFileName('');
    setUnitMappings({});
  };

  const getPositionStats = () => {
    const stats = new Map<string, {
      positionNumber: string;
      positionName: string;
      matched: boolean;
      itemsCount: number;
      manualVolume?: number;
      manualNote?: string;
    }>();

    positionUpdates.forEach((data, posNum) => {
      const position = clientPositionsMap.get(posNum);
      stats.set(posNum, {
        positionNumber: posNum,
        positionName: position?.work_name || 'Не найдена',
        matched: !!position,
        itemsCount: data.itemsCount,
        manualVolume: data.manualVolume,
        manualNote: data.manualNote,
      });
    });

    return Array.from(stats.values());
  };

  // Строки «доп» для предпросмотра: родитель — как его определит валидация
  // (существующая ДОП заменяется своей позицией заказчика).
  const additionalPreviewRows = useMemo(() => additionalPositions.map((dop) => {
    let parent = clientPositionsMap.get(dop.parentPositionNumber);
    if (parent?.is_additional) {
      parent = parent.parent_position_id ? positionsById.get(parent.parent_position_id) : undefined;
    }
    const fallback = dop.parentPositionNumber ? `${dop.parentPositionNumber} — не найдена` : '—';
    return {
      ...dop,
      parentPositionLabel: parent
        ? `${normalizePositionNumber(parent.position_number)} ${parent.work_name}`
        : fallback,
    };
  }), [additionalPositions, clientPositionsMap, positionsById]);

  return {
    // Данные
    parsedData,
    positionUpdates,
    additionalPositions,
    createdAdditional,
    validationResult,
    uploading,
    uploadProgress,
    importStatus,
    importError,
    importMismatches,
    clientPositionsMap,
    existingItemsByPosition,

    // Единицы измерения
    availableUnits,
    unitMappings,
    getUnknownUnits,
    setUnitMapping,
    applyUnitMappings,

    // Методы
    loadNomenclature,
    parseExcelFile,
    validateParsedData,
    processWorkBindings,
    insertBoqItems,
    addMissingToNomenclature,
    loadExistingItems,
    reset,
    getPositionStats,
    additionalPreviewRows,
  };
};
