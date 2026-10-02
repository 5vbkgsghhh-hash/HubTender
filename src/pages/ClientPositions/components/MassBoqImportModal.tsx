import React, { useState, useEffect } from 'react';
import { Modal, Steps, Button, Space } from 'antd';
import { useMassBoqImport } from '../hooks/useMassBoqImport';
import { useAuth } from '../../../contexts/AuthContext';
import { UploadStep } from './MassBoqImport/UploadStep';
import { ReviewStep } from './MassBoqImport/ReviewStep';
import { ValidationIssuesPanels } from './MassBoqImport/ValidationIssuesPanels';
import { ImportProgressStep } from './MassBoqImport/ImportProgressStep';

// UI шагов вынесен в ./MassBoqImport/* (лимит ≤600 строк на файл);
// здесь остаётся оркестрация: wiring хука, эффекты, хендлеры и футер.

interface MassBoqImportModalProps {
  open: boolean;
  tenderId: string;
  tenderTitle: string;
  onClose: (success: boolean) => void;
}

const MassBoqImportModalInner: React.FC<MassBoqImportModalProps> = ({
  open,
  tenderId,
  tenderTitle,
  onClose,
}) => {
  const { user } = useAuth();
  const [currentStep, setCurrentStep] = useState(0);
  const [nomenclatureLoaded, setNomenclatureLoaded] = useState(false);
  const [addingToNomenclature, setAddingToNomenclature] = useState(false);

  const {
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
    availableUnits,
    unitMappings,
    getUnknownUnits,
    setUnitMapping,
    applyUnitMappings,
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
  } = useMassBoqImport();

  // Загрузка справочников при открытии
  useEffect(() => {
    if (open && tenderId && !nomenclatureLoaded) {
      loadNomenclature(tenderId).then((success) => {
        setNomenclatureLoaded(success);
        if (!success) {
          Modal.error({
            title: 'Ошибка загрузки',
            content: 'Не удалось загрузить справочники. Попробуйте закрыть и открыть модал заново.',
          });
        }
      });
    }
    // loadNomenclature is a stable hook-returned function; intentionally excluded to avoid loop
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, tenderId, nomenclatureLoaded]);

  // Сброс при закрытии
  useEffect(() => {
    if (!open) {
      setNomenclatureLoaded(false);
      setCurrentStep(0);
      reset();
    }
    // reset is a stable hook-returned function; intentionally excluded to avoid loop
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  // Загрузка существующих BOQ items при переходе на шаг 1
  useEffect(() => {
    if (currentStep === 1 && positionUpdates.size > 0) {
      const ids = Array.from(positionUpdates.keys())
        .map(posNum => clientPositionsMap.get(posNum)?.id)
        .filter(Boolean) as string[];
      loadExistingItems(ids);
    }
    // clientPositionsMap and loadExistingItems are stable; intentionally excluded to avoid loop
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentStep, positionUpdates]);

  // Обработка загрузки файла
  const handleFileUpload = async (file: File) => {
    const success = await parseExcelFile(file);
    if (success) {
      setCurrentStep(1);
    }
    return false;
  };

  // Валидация
  const handleValidate = () => {
    const validation = validateParsedData(parsedData);
    const bindingErrors = processWorkBindings(parsedData);

    if (bindingErrors.length > 0) {
      validation.errors.push(...bindingErrors);
      validation.isValid = false;
    }

    if (validation.isValid) {
      handleImport();
    }
  };

  // Импорт
  const handleImport = async () => {
    setCurrentStep(2);
    const success = await insertBoqItems(parsedData, tenderId, user?.id);
    if (success) {
      setTimeout(() => handleClose(true), 500);
    }
  };

  // Применить маппинг единиц (parsedData обновится, затем пользователь нажимает Импортировать/Валидировать)
  const handleApplyUnitMappings = () => {
    applyUnitMappings();
  };

  // Добавить отсутствующую номенклатуру и повторить валидацию
  const handleAddMissingToNomenclature = async () => {
    setAddingToNomenclature(true);
    await addMissingToNomenclature(tenderId);
    setAddingToNomenclature(false);
  };

  // Закрытие
  const handleClose = (success: boolean = false) => {
    reset();
    setCurrentStep(0);
    setNomenclatureLoaded(false);
    onClose(success);
  };

  // Статистика по позициям
  const positionStats = getPositionStats();
  const matchedCount = positionStats.filter(p => p.matched).length;
  const unmatchedCount = positionStats.filter(p => !p.matched).length;
  const positionOnlyCount = positionStats.filter(
    p => p.matched && p.itemsCount === 0 && (p.manualVolume !== undefined || p.manualNote !== undefined)
  ).length;
  const additionalCount = additionalPositions.length;
  const hasDataToImport = parsedData.length > 0 || positionOnlyCount > 0 || additionalCount > 0;
  const unknownUnits = getUnknownUnits();
  const importLabelParts = [
    ...(parsedData.length > 0 ? [`${parsedData.length} элементов`] : []),
    ...(positionOnlyCount > 0 ? [`${positionOnlyCount} поз. ГП`] : []),
    ...(additionalCount > 0 ? [`${additionalCount} ДОП`] : []),
  ];
  const allUnitsMapped = unknownUnits.every(u => !!unitMappings[u]);
  const hasUnmappedUnits = unknownUnits.length > 0 && !allUnitsMapped;

  // Кнопки футера
  const getFooterButtons = () => {
    if (currentStep === 0) {
      return [
        <Button key="cancel" onClick={() => handleClose(false)} disabled={uploading}>
          Отмена
        </Button>,
      ];
    }

    if (currentStep === 1) {
      const hasMissingNomenclature = validationResult && (
        validationResult.missingNomenclature.works.length > 0 ||
        validationResult.missingNomenclature.materials.length > 0
      );
      const missingCount = hasMissingNomenclature
        ? (validationResult!.missingNomenclature.works.length + validationResult!.missingNomenclature.materials.length)
        : 0;

      return [
        <Button key="back" onClick={() => setCurrentStep(0)} disabled={uploading || addingToNomenclature}>
          Назад
        </Button>,
        ...(unknownUnits.length > 0 ? [
          <Button
            key="applyMappings"
            type="default"
            onClick={handleApplyUnitMappings}
            disabled={hasUnmappedUnits || uploading}
          >
            Применить маппинг единиц {allUnitsMapped ? `(${unknownUnits.length})` : `(${unknownUnits.filter(u => unitMappings[u]).length}/${unknownUnits.length})`}
          </Button>
        ] : []),
        ...(hasMissingNomenclature ? [
          <Button
            key="addNomenclature"
            onClick={handleAddMissingToNomenclature}
            loading={addingToNomenclature}
            disabled={uploading || hasUnmappedUnits}
          >
            Добавить в номенклатуру ({missingCount})
          </Button>
        ] : []),
        <Button
          key="import"
          type="primary"
          onClick={handleValidate}
          disabled={!hasDataToImport || addingToNomenclature || hasUnmappedUnits}
          loading={uploading}
        >
          {hasUnmappedUnits
            ? 'Сопоставьте единицы измерения'
            : parsedData.length === 0 && additionalCount === 0
              ? `Загрузить ${positionOnlyCount} позиций (данные ГП)`
              : `Загрузить ${importLabelParts.join(' + ')}`
          }
        </Button>,
      ];
    }

    if (currentStep === 2) {
      return [
        <Button
          key="close"
          onClick={() => handleClose(importStatus === 'success')}
          disabled={uploading}
        >
          Закрыть
        </Button>,
      ];
    }

    return [];
  };

  return (
    <Modal
      title={`Массовый импорт BOQ — ${tenderTitle}`}
      open={open}
      onCancel={() => handleClose(false)}
      width={1000}
      footer={getFooterButtons()}
      maskClosable={!uploading}
      keyboard={!uploading}
    >
      <Space direction="vertical" style={{ width: '100%' }} size="large">
        {/* Шаги */}
        <Steps current={currentStep} size="small">
          <Steps.Step title="Загрузка файла" />
          <Steps.Step title="Проверка и сопоставление" />
          <Steps.Step title="Импорт" />
        </Steps>

        {/* Шаг 0: Загрузка файла */}
        {currentStep === 0 && (
          <UploadStep
            uploading={uploading}
            nomenclatureLoaded={nomenclatureLoaded}
            onFileUpload={handleFileUpload}
          />
        )}

        {/* Шаг 1: Проверка и сопоставление */}
        {currentStep === 1 && (
          <div>
            <ReviewStep
              positionStats={positionStats}
              matchedCount={matchedCount}
              unmatchedCount={unmatchedCount}
              parsedData={parsedData}
              positionUpdates={positionUpdates}
              clientPositionsMap={clientPositionsMap}
              existingItemsByPosition={existingItemsByPosition}
              additionalRows={additionalPreviewRows}
              unknownUnits={unknownUnits}
              unitMappings={unitMappings}
              setUnitMapping={setUnitMapping}
              availableUnits={availableUnits}
            />
            <ValidationIssuesPanels validationResult={validationResult} />
          </div>
        )}

        {/* Шаг 2: Импорт */}
        {currentStep === 2 && (
          <ImportProgressStep
            importStatus={importStatus}
            importError={importError}
            importMismatches={importMismatches}
            insertedCount={parsedData.filter(i => i.matchedPositionId || i.additionalTempId).length}
            uploadProgress={uploadProgress}
            parsedDataLength={parsedData.length}
            matchedCount={matchedCount}
            positionOnlyCount={positionOnlyCount}
            additionalCount={additionalCount}
            createdAdditional={createdAdditional}
          />
        )}
      </Space>
    </Modal>
  );
};

// memo: модалка всегда смонтирована на странице позиций и без границы перерендеривалась
// бы на каждый символ поиска; пропсы стабилизированы в ClientPositions (useCallback).
export const MassBoqImportModal = React.memo(MassBoqImportModalInner);
