import { FC } from 'react';
import { Alert, Table, Typography } from 'antd';
import type { ParsedAdditionalRow } from '../../utils/boq/additionalImport';

const { Text } = Typography;

export interface IAdditionalPreviewRow extends ParsedAdditionalRow {
  /** «5 Кладка стен» — позиция, к которой создаётся ДОП (массовый импорт). */
  parentPositionLabel?: string;
}

interface IAdditionalRowsPreviewProps {
  rows: IAdditionalPreviewRow[];
  /** Колонка «К позиции» — для массового импорта. */
  showParent?: boolean;
  /** Пояснение под заголовком (одиночный импорт: к какой позиции создаются ДОП). */
  hint?: string;
}

/** Предпросмотр новых ДОП из строк «доп» Excel-импорта (обе модалки импорта). */
export const AdditionalRowsPreview: FC<IAdditionalRowsPreviewProps> = ({ rows, showParent = false, hint }) => {
  if (rows.length === 0) return null;

  return (
    <Alert
      type="info"
      showIcon
      style={{ marginBottom: 16 }}
      message={`Новые ДОП из строк «доп»: ${rows.length}`}
      description={
        <>
          <Text type="secondary" style={{ fontSize: 12, display: 'block', marginBottom: 8 }}>
            {hint ?? 'Номер ДОП присвоится при загрузке; работы и материалы под строкой «доп» попадут в эту ДОП.'}
          </Text>
          <Table<IAdditionalPreviewRow>
            size="small"
            rowKey="tempId"
            dataSource={rows}
            pagination={rows.length > 10 ? { pageSize: 10, size: 'small' } : false}
            scroll={{ x: true }}
            columns={[
              { title: 'Строка', dataIndex: 'rowIndex', width: 70 },
              ...(showParent
                ? [{
                    title: 'К позиции',
                    dataIndex: 'parentPositionLabel',
                    ellipsis: true,
                    render: (v?: string) => v || '—',
                  }]
                : []),
              {
                title: 'Наименование ДОП',
                dataIndex: 'workName',
                ellipsis: true,
                render: (v: string) => v || '—',
              },
              { title: 'Ед.', dataIndex: 'unitCode', width: 70, render: (v: string) => v || '—' },
              {
                title: 'Кол-во ГП',
                dataIndex: 'manualVolume',
                width: 100,
                align: 'right' as const,
                render: (v?: number) => (v !== undefined ? v.toLocaleString('ru-RU') : '—'),
              },
              { title: 'Элементов', dataIndex: 'itemsCount', width: 90, align: 'center' as const },
            ]}
          />
        </>
      }
    />
  );
};
