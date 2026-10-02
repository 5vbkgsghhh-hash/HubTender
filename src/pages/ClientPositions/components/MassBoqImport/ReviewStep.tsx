import React from 'react';
import { Alert, Collapse, Select, Space, Table, Tag, Typography } from 'antd';
import { CheckCircleOutlined, CloseCircleOutlined } from '@ant-design/icons';
import { BoqPreviewTable } from '../BoqPreviewTable';
import { AdditionalRowsPreview } from '../../../../components/BoqImport/AdditionalRowsPreview';
import type { ParsedBoqItem, ParsedAdditionalPosition, PositionUpdateData, ClientPosition } from '../../utils';
import type { ExistingBoqPreviewItem } from '../../hooks/useMassBoqImportRefs';

const { Text } = Typography;
const { Panel } = Collapse;

export interface PositionStat {
  positionNumber: string;
  positionName: string;
  matched: boolean;
  itemsCount: number;
  manualVolume?: number;
  manualNote?: string;
}

/** Шаг 1 массового импорта BOQ: статистика сопоставления, таблица позиций,
 *  предпросмотр строк и маппинг единиц измерения. Панели ошибок валидации —
 *  в ValidationIssuesPanels. */
export const ReviewStep: React.FC<{
  positionStats: PositionStat[];
  matchedCount: number;
  unmatchedCount: number;
  parsedData: ParsedBoqItem[];
  positionUpdates: Map<string, PositionUpdateData>;
  clientPositionsMap: Map<string, ClientPosition>;
  existingItemsByPosition: Map<string, ExistingBoqPreviewItem[]>;
  /** Строки «доп» (новые ДОП) с подписью позиции-родителя. */
  additionalRows: ParsedAdditionalPosition[];
  unknownUnits: string[];
  unitMappings: Record<string, string>;
  setUnitMapping: (excelUnit: string, dbUnit: string) => void;
  availableUnits: { code: string; name: string }[];
}> = ({
  positionStats,
  matchedCount,
  unmatchedCount,
  parsedData,
  positionUpdates,
  clientPositionsMap,
  existingItemsByPosition,
  additionalRows,
  unknownUnits,
  unitMappings,
  setUnitMapping,
  availableUnits,
}) => (
  <>
    {/* Статистика сопоставления */}
    <Alert
      message={
        <Space>
          <span>Найдено позиций: {positionStats.length}</span>
          <Tag color="green">{matchedCount} сопоставлено</Tag>
          {unmatchedCount > 0 && <Tag color="red">{unmatchedCount} не найдено</Tag>}
        </Space>
      }
      type={unmatchedCount > 0 ? 'warning' : 'success'}
      style={{ marginBottom: 16 }}
    />

    {/* Таблица позиций */}
    <Table
      dataSource={positionStats}
      rowKey="positionNumber"
      size="small"
      pagination={{ defaultPageSize: 20, showSizeChanger: true, pageSizeOptions: ['10', '20', '50', '100'] }}
      style={{ marginBottom: 16 }}
      columns={[
        {
          title: '№ позиции',
          dataIndex: 'positionNumber',
          width: 100,
        },
        {
          title: 'Статус',
          dataIndex: 'matched',
          width: 120,
          render: (matched: boolean) => matched
            ? <Tag icon={<CheckCircleOutlined />} color="success">Найдена</Tag>
            : <Tag icon={<CloseCircleOutlined />} color="error">Не найдена</Tag>,
        },
        {
          title: 'Название позиции',
          dataIndex: 'positionName',
          ellipsis: true,
        },
        {
          title: 'Элементов',
          dataIndex: 'itemsCount',
          width: 100,
          align: 'center',
          render: (count: number, record: { manualVolume?: number; manualNote?: string }) => {
            if (count === 0 && (record.manualVolume !== undefined || record.manualNote !== undefined)) {
              return <Tag color="blue">только ГП</Tag>;
            }
            return count;
          },
        },
        {
          title: 'Кол-во ГП',
          dataIndex: 'manualVolume',
          width: 100,
          render: (v: number | undefined) => v !== undefined ? v.toLocaleString('ru-RU') : '—',
        },
        {
          title: 'Примечание ГП',
          dataIndex: 'manualNote',
          width: 150,
          ellipsis: true,
          render: (v: string | undefined) => v || '—',
        },
      ]}
    />

    {/* Новые ДОП из строк «доп» */}
    <AdditionalRowsPreview rows={additionalRows} showParent />

    {/* Предпросмотр: существующие и новые строки */}
    <Collapse defaultActiveKey={['preview']} style={{ marginBottom: 16 }}>
      <Panel header="Предпросмотр строк (существующие и новые)" key="preview">
        <BoqPreviewTable
          parsedData={parsedData}
          positionUpdates={positionUpdates}
          clientPositionsMap={clientPositionsMap}
          existingItemsByPosition={existingItemsByPosition}
          additionalRows={additionalRows}
        />
      </Panel>
    </Collapse>

    {/* Маппинг единиц измерения */}
    {unknownUnits.length > 0 && (
      <Alert
        type="warning"
        style={{ marginBottom: 16 }}
        message={`Единицы измерения не найдены в справочнике (${unknownUnits.length})`}
        description={
          <div style={{ marginTop: 8 }}>
            <Text type="secondary" style={{ fontSize: 12, display: 'block', marginBottom: 8 }}>
              Сопоставьте каждую единицу из файла с единицей в базе данных, затем нажмите «Применить маппинг единиц»
            </Text>
            <Table
              size="small"
              pagination={false}
              dataSource={unknownUnits.map(u => ({ key: u, excelUnit: u }))}
              columns={[
                {
                  title: 'В файле',
                  dataIndex: 'excelUnit',
                  width: 160,
                  render: (u: string) => <Tag color="orange">{u}</Tag>,
                },
                {
                  title: '→ В базе данных',
                  key: 'mapping',
                  render: (_: unknown, row: { excelUnit: string }) => (
                    <Select
                      showSearch
                      style={{ width: 220 }}
                      placeholder="Выберите единицу..."
                      value={unitMappings[row.excelUnit] || undefined}
                      onChange={(val: string) => setUnitMapping(row.excelUnit, val)}
                      optionFilterProp="children"
                      filterOption={(input, opt) =>
                        (opt?.label ?? '').toLowerCase().includes(input.toLowerCase())
                      }
                      options={availableUnits.map(u => ({
                        value: u.code,
                        label: u.code === u.name ? u.code : `${u.code} — ${u.name}`,
                      }))}
                    />
                  ),
                },
                {
                  title: 'Статус',
                  key: 'status',
                  width: 100,
                  render: (_: unknown, row: { excelUnit: string }) =>
                    unitMappings[row.excelUnit]
                      ? <Tag color="success">✓ Сопоставлено</Tag>
                      : <Tag color="warning">Не задано</Tag>,
                },
              ]}
            />
          </div>
        }
      />
    )}
  </>
);
