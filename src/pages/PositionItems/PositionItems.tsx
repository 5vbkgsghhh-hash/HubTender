import { useState, useMemo, memo, useEffect } from 'react';
import { Card, Tabs, Alert, Skeleton } from 'antd';
import { missingFXMessage } from '../../utils/boq/currencyGuard';
import WorkEditForm from './WorkEditForm';
import MaterialEditForm from './MaterialEditForm';
import { useBoqItems } from './hooks/useBoqItems';
import { useItemActions } from './hooks/useItemActions';
import { useItemBulkActions } from './hooks/useItemBulkActions';
import { usePositionTabTitle } from './hooks/usePositionTabRegistration';
import ItemsTable, { ITEMS_PLAIN_FIT_WIDTH } from './components/ItemsTable';
import ItemsMobileCards from './components/ItemsMobileCards';
import ItemsToolbar from './components/ItemsToolbar';
import PositionHeader from './components/PositionHeader';
import PositionLandscapeInfo from './components/PositionLandscapeInfo';
import AddItemForm from './components/AddItemForm';
import TemplateSelectModal from './components/TemplateSelectModal';
import AuditHistoryTab from './components/AuditHistoryTab';
import { BoqItemsImportModal } from './components/BoqItemsImportModal';
import BoqItemSheet from './components/mobile/BoqItemSheet';
import { useGpAutosave } from './hooks/useGpAutosave';
import { useDeadlineCheck } from '../../hooks/useDeadlineCheck';
import { useIsMobile } from '../../hooks/useIsMobile';
import { useAuth } from '../../contexts/AuthContext';
import { useTheme } from '../../contexts/ThemeContext';
import { LandscapeTableOverlay } from '../../components/responsive/LandscapeTableOverlay';

/** Стабильная пустышка вместо рефетча позиции (телефонный автосейв ГП). */
const NOOP = () => {};

interface PositionItemsProps {
  /** Передаётся из WorkspaceKeepAlive (несколько экземпляров смонтированы сразу —
   *  нельзя полагаться на useParams). Для прямого роутинга есть PositionItemsRoute. */
  positionId: string;
  /** ?itemId=… из URL. Приходит пропом, а НЕ из useSearchParams: см. memo ниже.
   *  WorkspaceKeepAlive отдаёт его только активной вкладке. */
  deepLinkItemId?: string | null;
}

const PositionItems: React.FC<PositionItemsProps> = ({ positionId, deepLinkItemId = null }) => {
  const { user } = useAuth();
  const { isPhone, isLandscapePhone, isMobile, isPhoneDevice } = useIsMobile();
  const { theme } = useTheme();

  const [workSearchText, setWorkSearchText] = useState<string>('');
  const [materialSearchText, setMaterialSearchText] = useState<string>('');
  const [expandedRowKeys, setExpandedRowKeys] = useState<string[]>([]);
  const [templateModalVisible, setTemplateModalVisible] = useState<boolean>(false);
  const [importModalVisible, setImportModalVisible] = useState<boolean>(false);
  const [selectedCostCategoryId, setSelectedCostCategoryId] = useState<string | null>(null);
  const [costSearchText, setCostSearchText] = useState<string>('');
  const [activeTab, setActiveTab] = useState<string>('current');
  const [isDeleteMode, setIsDeleteMode] = useState<boolean>(false);
  const [selectedDeleteIds, setSelectedDeleteIds] = useState<Set<string>>(new Set());
  const [isBulkDeleting, setIsBulkDeleting] = useState<boolean>(false);

  // Deep-link подсветка: после загрузки строк прокручиваем к itemId из query
  // и временно подсвечиваем строку (Ant Table рендерит data-row-key на <tr>).
  useEffect(() => {
    if (!deepLinkItemId) return;
    const t = setTimeout(() => {
      const row = document.querySelector(`[data-row-key="${deepLinkItemId}"]`);
      if (row) {
        row.scrollIntoView({ behavior: 'smooth', block: 'center' });
        row.classList.add('boq-row-deeplink-highlight');
        setTimeout(() => row.classList.remove('boq-row-deeplink-highlight'), 4000);
      }
    }, 400);
    return () => clearTimeout(t);
  }, [deepLinkItemId]);

  const {
    position,
    items,
    works,
    materials,
    templates,
    loading,
    currencyRates,
    costCategories,
    workNames,
    materialNames,
    units,
    gpVolume,
    setGpVolume,
    gpNote,
    setGpNote,
    workName,
    setWorkName,
    unitCode,
    setUnitCode,
    getCurrencyRate,
    fetchPositionData,
    fetchItems,
    loadEditData,
    editDataState,
  } = useBoqItems(positionId, isPhoneDevice);

  // Телефонный лист редактирования (обе ориентации). Держим id, а не запись:
  // лист сам находит её в items — так он переживает рефетч и поворот.
  const [sheetItemId, setSheetItemId] = useState<string | null>(null);
  const openSheet = (id: string) => {
    setSheetItemId(id);
    // Справочники на телефоне пропущены skipEditData — догружаем по первому тапу.
    void loadEditData();
  };

  // Обновление заголовка вкладки приложения для этой позиции
  usePositionTabTitle(positionId, position);

  // Проверка дедлайна для блокировки редактирования
  const { canEdit: canEditByDeadline, loading: deadlineLoading } =
    useDeadlineCheck(position?.tender_id);
  const isReadOnlyByDeadline = !canEditByDeadline || deadlineLoading;

  // На телефоне (любая ориентация) страница — только для просмотра
  const readOnly = isReadOnlyByDeadline || isMobile || isLandscapePhone;

  // Единый Alert об отсутствующем курсе валюты (P0). Бэкенд — окончательный блокер.
  const fxWarning = useMemo(
    () =>
      loading
        ? null
        : missingFXMessage(items, {
            usd_rate: currencyRates.usd,
            eur_rate: currencyRates.eur,
            cny_rate: currencyRates.cny,
          }),
    [loading, items, currencyRates],
  );

  const {
    handleAddWork,
    handleAddMaterial,
    handleAddTemplate,
    handleFormSave,
    handleItemFieldSave,
    handleSaveGPData,
    handleSaveAdditionalWorkData,
    handleMoveItem,
  } = useItemActions({
    position,
    works,
    materials,
    items,
    getCurrencyRate,
    fetchItems,
    readOnly: isReadOnlyByDeadline,
  });

  const { handleBulkDelete, handleClearAllItems, handleApplyCostToAll, getCostCategoryOptions } =
    useItemBulkActions({
      positionId,
      items,
      userId: user?.id,
      fetchItems,
      costCategories,
      costSearchText,
      selectedCostCategoryId,
      setSelectedCostCategoryId,
      setCostSearchText,
      selectedDeleteIds,
      setIsDeleteMode,
      setSelectedDeleteIds,
      setIsBulkDeleting,
    });

  // Fail-closed: если хотя бы у одной строки нет курса (total_amount == null) —
  // итог позиции не рассчитан (null → «—»), а не частичная сумма.
  const totalSum = useMemo<number | null>(
    () =>
      items.some((item) => item.total_amount == null)
        ? null
        : items.reduce((sum, item) => sum + (item.total_amount || 0), 0),
    [items],
  );

  const handleEditClick = (record: { id: string }) => setExpandedRowKeys([record.id]);

  const onFormSave = async (data: Record<string, unknown>) => {
    await handleFormSave(data, expandedRowKeys, items, () => setExpandedRowKeys([]));
  };
  const onFormCancel = () => setExpandedRowKeys([]);

  // Значения приходят аргументами, а не из замыкания: телефонный автосейв шлёт свой
  // драфт, иначе WS-рефетч (fetchPositionData → setGpVolume) успел бы затереть его
  // серверным значением до срабатывания debounce.
  const onSaveGPData = async (volume: number, note: string, opts?: { refetch?: boolean }) => {
    if (!positionId) return;
    await handleSaveGPData(
      positionId,
      volume,
      note,
      opts?.refetch === false ? NOOP : fetchPositionData,
    );
  };
  const onSaveAdditionalWorkData = async () => {
    if (positionId && position?.is_additional) {
      await handleSaveAdditionalWorkData(positionId, workName, unitCode, fetchPositionData);
    }
  };

  // РОВНО один экземпляр на страницу: в ландшафте PositionHeader остаётся
  // смонтированным под оверлеем, и хук внутри GpInlineFields дал бы два таймера.
  const gp = useGpAutosave({ gpVolume, setGpVolume, gpNote, setGpNote, onSaveGPData });
  const gpEditable = isPhoneDevice && !isReadOnlyByDeadline;

  const handleStartDelete = (id: string) => {
    setExpandedRowKeys([]);
    setIsDeleteMode(true);
    setSelectedDeleteIds(new Set([id]));
  };
  const handleToggleDeleteSelection = (id: string) => {
    setSelectedDeleteIds((prev) => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  };
  const handleCancelDeleteMode = () => {
    setIsDeleteMode(false);
    setSelectedDeleteIds(new Set());
  };

  // Обычно сюда не попадаем: call-site'ы («Позиции», «Форма КП») сеют строку в
  // positionRowCache перед навигацией, и useBoqItems гидратирует position синхронно.
  // Остаётся на промах кэша (deep-link, F5, переход из места без сида) — скелетон, а не
  // белый экран, чтобы промах деградировал мягко.
  if (!position) {
    return (
      <div style={{ padding: '0 8px' }}>
        <Card style={{ marginBottom: 16 }}>
          <Skeleton active paragraph={{ rows: 2 }} />
        </Card>
        <Card>
          <Skeleton active paragraph={{ rows: 6 }} title={false} />
        </Card>
      </div>
    );
  }

  // Тело карточки «Элементы позиции» в зависимости от устройства/ориентации
  const itemsBody = (() => {
    if (isPhone && !isLandscapePhone) {
      return (
        <ItemsMobileCards
          items={items}
          totalSum={totalSum}
          loading={loading}
          positionId={positionId}
          onItemClick={(it) => openSheet(it.id)}
        />
      );
    }
    if (isLandscapePhone) {
      return (
        // fit="width" (transform:scale), а НЕ "zoom": CSS zoom смещает hit-тестинг в
        // мобильных WebView — тап по строке попадал бы в пустоту. Прецедент: «Форма КП»
        // и «Затраты» переехали на width ровно тогда, когда их строки стали кликабельными.
        // Цена — sticky-шапка (она реализована только для .lto-fit-zoom).
        <LandscapeTableOverlay theme={theme} fit="width" width={ITEMS_PLAIN_FIT_WIDTH}>
          <PositionLandscapeInfo
            position={position}
            gpVolume={gpVolume}
            gpNote={gpNote}
            workName={workName}
            unitCode={unitCode}
            gpEditable={gpEditable}
            gp={gp}
            disabled={isReadOnlyByDeadline}
          />
          <ItemsTable
            plain
            readOnly
            items={items}
            loading={loading}
            getCurrencyRate={getCurrencyRate}
            onRowClick={(r) => openSheet(r.id)}
          />
        </LandscapeTableOverlay>
      );
    }
    return (
      <ItemsTable
        items={items}
        loading={loading}
        expandedRowKeys={expandedRowKeys}
        onExpandedRowsChange={setExpandedRowKeys}
        onEditClick={handleEditClick}
        onStartDelete={handleStartDelete}
        onToggleDeleteSelection={handleToggleDeleteSelection}
        onMoveItem={handleMoveItem}
        getCurrencyRate={getCurrencyRate}
        isDeleteMode={isDeleteMode}
        selectedDeleteIds={selectedDeleteIds}
        readOnly={readOnly}
        expandedRowRender={(record) => {
          const isWork = ['раб', 'суб-раб', 'раб-комп.'].includes(record.boq_item_type);
          if (isWork) {
            return (
              <WorkEditForm
                record={record}
                workNames={workNames}
                costCategories={costCategories}
                currencyRates={currencyRates}
                onSave={onFormSave}
                onCancel={onFormCancel}
                readOnly={readOnly}
              />
            );
          }
          const workItems = items.filter((item) =>
            ['раб', 'суб-раб', 'раб-комп.'].includes(item.boq_item_type),
          );
          return (
            <MaterialEditForm
              record={record}
              materialNames={materialNames}
              workItems={workItems}
              costCategories={costCategories}
              currencyRates={currencyRates}
              gpVolume={gpVolume}
              onSave={onFormSave}
              onCancel={onFormCancel}
              readOnly={readOnly}
            />
          );
        }}
      />
    );
  })();

  return (
    <div style={{ padding: '0 8px' }}>
      {fxWarning && (
        <Alert
          type="error"
          showIcon
          message={fxWarning}
          style={{ marginBottom: 12 }}
        />
      )}
      <PositionHeader
        position={position}
        gpVolume={gpVolume}
        setGpVolume={setGpVolume}
        gpNote={gpNote}
        setGpNote={setGpNote}
        workName={workName}
        setWorkName={setWorkName}
        unitCode={unitCode}
        setUnitCode={setUnitCode}
        units={units}
        disabled={isReadOnlyByDeadline}
        onSaveGPData={onSaveGPData}
        onSaveAdditionalWorkData={onSaveAdditionalWorkData}
        isPhone={isPhoneDevice}
        // В ландшафте шапка закрыта оверлеем — ГП там правится в PositionLandscapeInfo.
        gpEditable={gpEditable && isPhone && !isLandscapePhone}
        gp={gp}
      />

      <Tabs
        activeKey={activeTab}
        onChange={setActiveTab}
        items={[
          {
            key: 'current',
            label: 'Текущие',
            children: (
              <>
                {!isPhoneDevice && (
                  <Card title="Добавление работ и материалов" style={{ marginBottom: 16 }}>
                    <AddItemForm
                      works={works}
                      materials={materials}
                      workSearchText={workSearchText}
                      materialSearchText={materialSearchText}
                      onWorkSearchChange={setWorkSearchText}
                      onMaterialSearchChange={setMaterialSearchText}
                      onAddWork={(workNameId) => {
                        handleAddWork(workNameId);
                        setWorkSearchText('');
                      }}
                      onAddMaterial={(materialNameId) => {
                        handleAddMaterial(materialNameId);
                        setMaterialSearchText('');
                      }}
                      onOpenTemplateModal={() => setTemplateModalVisible(true)}
                      disabled={isReadOnlyByDeadline}
                    />
                  </Card>
                )}

                <Card
                  title={
                    isPhoneDevice ? (
                      <span>Элементы позиции</span>
                    ) : (
                      <ItemsToolbar
                        isDeleteMode={isDeleteMode}
                        selectedDeleteCount={selectedDeleteIds.size}
                        isBulkDeleting={isBulkDeleting}
                        onBulkDelete={handleBulkDelete}
                        onCancelDeleteMode={handleCancelDeleteMode}
                        costSearchText={costSearchText}
                        setCostSearchText={setCostSearchText}
                        setSelectedCostCategoryId={setSelectedCostCategoryId}
                        selectedCostCategoryId={selectedCostCategoryId}
                        getCostCategoryOptions={getCostCategoryOptions}
                        onApplyCostToAll={handleApplyCostToAll}
                        onOpenImport={() => setImportModalVisible(true)}
                        onClearAll={handleClearAllItems}
                        itemsCount={items.length}
                        disabled={isReadOnlyByDeadline}
                        totalSum={totalSum}
                      />
                    )
                  }
                  styles={{ body: { padding: isPhoneDevice ? 8 : 0 } }}
                >
                  {itemsBody}
                </Card>
              </>
            ),
          },
          {
            key: 'history',
            label: 'История',
            children: <AuditHistoryTab positionId={positionId} />,
          },
        ]}
      />

      {/* Модалка выбора шаблона */}
      <TemplateSelectModal
        visible={templateModalVisible}
        templates={templates}
        onCancel={() => setTemplateModalVisible(false)}
        onSelect={(templateId) => {
          handleAddTemplate(templateId, () => {});
          setTemplateModalVisible(false);
        }}
      />

      {/* Модалка импорта из Excel */}
      {positionId && position?.tender_id && (
        <BoqItemsImportModal
          open={importModalVisible}
          positionId={positionId}
          tenderId={position.tender_id}
          // Строки «доп» создают ДОП к позиции заказчика; на странице ДОП — к её родителю.
          additionalParentId={(position.is_additional && position.parent_position_id) || positionId}
          onClose={() => {
            setImportModalVisible(false);
            // Импорт атомарный; обновляем после любого закрытия, чтобы показать результат.
            fetchItems();
          }}
        />
      )}

      {/* Телефонный лист редактирования — вне веток портрета/ландшафта, поэтому
          переживает поворот (запись ищется по id в живом items). */}
      {isPhoneDevice && (
        <BoqItemSheet
          itemId={sheetItemId}
          items={items}
          workNames={workNames}
          materialNames={materialNames}
          costCategories={costCategories}
          units={units}
          currencyRates={currencyRates}
          gpVolume={gpVolume}
          editDataState={editDataState}
          canEdit={!isReadOnlyByDeadline}
          onFieldSave={handleItemFieldSave}
          onClose={() => setSheetItemId(null)}
        />
      )}
    </div>
  );
};

// Стили для подсветки строк по типу
const styles = `
  .boq-row-rab {
    background-color: rgba(255, 152, 0, 0.15) !important;
  }
  .boq-row-rab > td.ant-table-cell-fix-left {
    background-color: rgba(255, 152, 0, 0.15) !important;
  }
  .boq-row-rab:hover > td {
    background-color: rgba(255, 152, 0, 0.25) !important;
  }
  .boq-row-sub-rab {
    background-color: rgba(156, 39, 176, 0.15) !important;
  }
  .boq-row-sub-rab > td.ant-table-cell-fix-left {
    background-color: rgba(156, 39, 176, 0.15) !important;
  }
  .boq-row-sub-rab:hover > td {
    background-color: rgba(156, 39, 176, 0.25) !important;
  }
  .boq-row-rab-comp {
    background-color: rgba(244, 67, 54, 0.15) !important;
  }
  .boq-row-rab-comp > td.ant-table-cell-fix-left {
    background-color: rgba(244, 67, 54, 0.15) !important;
  }
  .boq-row-rab-comp:hover > td {
    background-color: rgba(244, 67, 54, 0.25) !important;
  }
  .boq-row-mat {
    background-color: rgba(33, 150, 243, 0.15) !important;
  }
  .boq-row-mat > td.ant-table-cell-fix-left {
    background-color: rgba(33, 150, 243, 0.15) !important;
  }
  .boq-row-mat:hover > td {
    background-color: rgba(33, 150, 243, 0.25) !important;
  }
  .boq-row-sub-mat {
    background-color: rgba(156, 204, 101, 0.15) !important;
  }
  .boq-row-sub-mat > td.ant-table-cell-fix-left {
    background-color: rgba(156, 204, 101, 0.15) !important;
  }
  .boq-row-sub-mat:hover > td {
    background-color: rgba(156, 204, 101, 0.25) !important;
  }
  .boq-row-mat-comp {
    background-color: rgba(0, 137, 123, 0.15) !important;
  }
  .boq-row-mat-comp > td.ant-table-cell-fix-left {
    background-color: rgba(0, 137, 123, 0.15) !important;
  }
  .boq-row-mat-comp:hover > td {
    background-color: rgba(0, 137, 123, 0.25) !important;
  }
`;

if (typeof document !== 'undefined') {
  const styleElement = document.createElement('style');
  styleElement.textContent = styles;
  document.head.appendChild(styleElement);
}

/**
 * memo: в keep-alive смонтировано несколько экземпляров (по вкладке на позицию), и без него
 * КАЖДАЯ скрытая вкладка перестраивала бы весь свой список на каждую навигацию.
 *
 * Держится только пока компонент не подписан на роутер. Раньше memo не работал вовсе:
 * useParams()/useSearchParams() подписывают на RouteContext/LocationContext, а те меняются
 * на каждую навигацию — то есть мемоизация была фиктивной, несмотря на стабильный
 * positionId. Поэтому positionId и deepLinkItemId приходят ПРОПАМИ, а роутер читает тонкая
 * обёртка PositionItemsRoute (для прямого роутинга) и WorkspaceKeepAlive (для вкладок).
 *
 * НЕ добавлять сюда useParams/useSearchParams/useLocation/useNavigate — это молча вернёт
 * шторм перерисовок, который ничем себя не выдаёт, кроме подтормаживания на телефоне.
 */
export default memo(PositionItems);
