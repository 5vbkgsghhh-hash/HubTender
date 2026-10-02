import React, { useState, useEffect } from 'react';
import { Modal, Steps, Button, Space, Progress, Alert } from 'antd';
import { FileUploadStep } from './FileUploadStep';
import { ValidationResultsStep } from './ValidationResultsStep';
import { useBoqItemsImport } from '../hooks/useBoqItemsImport';
import { formatCreatedAdditionalNumbers } from '../../../utils/boq/additionalImport';

interface BoqItemsImportModalProps {
  open: boolean;
  positionId: string;
  tenderId: string;
  /** Позиция заказчика, к которой строки «доп» создают ДОП (для ДОП — её родитель). */
  additionalParentId: string;
  onClose: (success: boolean) => void;
}

export const BoqItemsImportModal: React.FC<BoqItemsImportModalProps> = ({
  open,
  positionId,
  tenderId,
  additionalParentId,
  onClose,
}) => {
  const [currentStep, setCurrentStep] = useState(0);
  const [nomenclatureLoaded, setNomenclatureLoaded] = useState(false);
  const [addingToNomenclature, setAddingToNomenclature] = useState(false);

  const {
    parsedData,
    additionalRows,
    createdAdditional,
    validationResult,
    uploading,
    uploadProgress,
    importStatus,
    importError,
    loadNomenclature,
    parseExcelFile,
    validateParsedData,
    processWorkBindings,
    insertBoqItems,
    addMissingToNomenclature,
    reset,
  } = useBoqItemsImport();

  // Загрузка справочников при открытии модального окна
  useEffect(() => {
    if (open) {
      loadNomenclature().then(setNomenclatureLoaded);
    }
    // loadNomenclature is a stable hook-returned function; intentionally excluded to avoid loop
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  useEffect(() => {
    if (!open) {
      setNomenclatureLoaded(false);
      setAddingToNomenclature(false);
    }
  }, [open]);

  // Обработка загрузки файла
  const handleFileUpload = async (file: File) => {
    const success = await parseExcelFile(file);
    if (success) {
      // Переходим к шагу валидации (валидация уже запущена в хуке)
      setCurrentStep(1);
    }
    return false; // Предотвращаем автоматическую загрузку
  };

  // Валидация и переход к следующему шагу
  const handleValidate = () => {
    // Обрабатываем привязки материалов к работам
    const validation = validateParsedData(parsedData);
    const bindingErrors = processWorkBindings(parsedData);

    if (bindingErrors.length > 0) {
      // Показываем ошибки привязок
      validation.errors.push(...bindingErrors);
      validation.isValid = false;
      return;
    }

    // Переходим к импорту
    handleImport();
  };

  // Импорт данных
  const handleAddMissingToNomenclature = async () => {
    setAddingToNomenclature(true);
    const success = await addMissingToNomenclature();
    if (success) {
      validateParsedData(parsedData);
    }
    setAddingToNomenclature(false);
  };

  const handleImport = async () => {
    setCurrentStep(2);

    const success = await insertBoqItems(parsedData, positionId, tenderId, additionalParentId);

    if (success) {
      setTimeout(() => {
        handleClose(true);
      }, 500);
    }
  };

  // Закрытие модального окна
  const handleClose = (success: boolean = false) => {
    reset();
    setCurrentStep(0);
    onClose(success);
  };

  // Определяем действия для кнопок в зависимости от шага
  const getFooterButtons = () => {
    if (currentStep === 0) {
      return [
        <Button key="cancel" onClick={() => handleClose(false)} disabled={uploading}>
          Отмена
        </Button>,
      ];
    }

    if (currentStep === 1) {
      const hasErrors = validationResult && !validationResult.isValid;
      const hasMissingNomenclature = validationResult && (
        validationResult.missingNomenclature.works.length > 0 ||
        validationResult.missingNomenclature.materials.length > 0
      );
      const missingCount = hasMissingNomenclature
        ? validationResult!.missingNomenclature.works.length + validationResult!.missingNomenclature.materials.length
        : 0;
      console.log('[BoqImportModal] Кнопки шага 1:', {
        hasErrors,
        validationResult,
        parsedDataLength: parsedData.length,
      });
      return [
        <Button key="back" onClick={() => setCurrentStep(0)} disabled={uploading || addingToNomenclature}>
          Назад
        </Button>,
        ...(hasMissingNomenclature ? [
          <Button
            key="addNomenclature"
            onClick={handleAddMissingToNomenclature}
            loading={addingToNomenclature}
            disabled={uploading}
          >
            Добавить в номенклатуру ({missingCount})
          </Button>,
        ] : []),
        <Button
          key="import"
          type="primary"
          onClick={handleValidate}
          disabled={hasErrors || (parsedData.length === 0 && additionalRows.length === 0) || addingToNomenclature}
          loading={uploading}
        >
          {hasErrors
            ? 'Устранить ошибки'
            : `Импортировать ${parsedData.length} элементов${additionalRows.length > 0 ? ` + ${additionalRows.length} ДОП` : ''}`}
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
      title="Импорт работ и материалов из Excel"
      open={open}
      onCancel={() => handleClose(false)}
      width={900}
      footer={getFooterButtons()}
      maskClosable={!uploading}
      keyboard={!uploading}
    >
      <Space direction="vertical" style={{ width: '100%' }} size="large">
        {/* Шаги */}
        <Steps current={currentStep} size="small">
          <Steps.Step title="Загрузка файла" />
          <Steps.Step title="Валидация" />
          <Steps.Step title="Импорт" />
        </Steps>

        {/* Шаг 0: Загрузка файла */}
        {currentStep === 0 && (
          <FileUploadStep
            onFileUpload={handleFileUpload}
            uploading={uploading}
            nomenclatureLoaded={nomenclatureLoaded}
          />
        )}

        {/* Шаг 1: Результаты валидации */}
        {currentStep === 1 && validationResult && (
          <ValidationResultsStep
            validationResult={validationResult}
            totalRows={parsedData.length}
            additionalRows={additionalRows}
          />
        )}

        {/* Шаг 2: Импорт */}
        {currentStep === 2 && (
          <Space direction="vertical" style={{ width: '100%' }} size="middle">
            {importStatus === 'running' && (
              <>
                <Alert
                  type="info"
                  message="Импорт данных"
                  description={
                    `Загрузка ${parsedData.length} элементов в базу данных` +
                    (additionalRows.length > 0 ? `, создание ДОП: ${additionalRows.length}` : '') + '…'
                  }
                  showIcon
                />
                <Progress
                  percent={uploadProgress}
                  status="active"
                  strokeColor={{ from: '#10b981', to: '#059669' }}
                />
              </>
            )}
            {importStatus === 'success' && (
              <Alert
                type="success"
                message="Импорт завершён успешно!"
                description={
                  `Импортировано элементов: ${parsedData.length}` +
                  (createdAdditional.length > 0
                    ? `. Создано ДОП: ${createdAdditional.length} (${formatCreatedAdditionalNumbers(createdAdditional)})`
                    : '')
                }
                showIcon
              />
            )}
            {importStatus === 'error' && (
              <Alert
                type="error"
                message="Импорт не выполнен — данные не загружены"
                description={importError || 'Произошла ошибка при импорте.'}
                showIcon
              />
            )}
          </Space>
        )}
      </Space>
    </Modal>
  );
};
