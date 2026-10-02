import React, { useMemo } from 'react';
import { Table, Tag, Typography } from 'antd';
import { useTheme } from '../../../contexts/ThemeContext';
import type {
  ParsedBoqItem,
  ParsedAdditionalPosition,
  PositionUpdateData,
  ClientPosition,
} from '../utils/massBoqImportUtils';

const { Text } = Typography;

interface ExistingBoqItem {
  id: string;
  work_names?: { name?: string } | null;
  material_names?: { name?: string } | null;
  boq_item_type?: string | null;
  quantity?: number | null;
  total_amount?: number | null;
  client_position_id?: string;
}

interface BoqPreviewTableProps {
  parsedData: ParsedBoqItem[];
  positionUpdates: Map<string, PositionUpdateData>;
  clientPositionsMap: Map<string, ClientPosition>;
  existingItemsByPosition: Map<string, ExistingBoqItem[]>;
  /** Строки «доп»: их элементы — отдельной группой под позицией-родителем. */
  additionalRows?: ParsedAdditionalPosition[];
}

interface PreviewRow {
  key: string;
  isGroupHeader: boolean;
  /** Заголовок группы новой ДОП. */
  isAdditional?: boolean;
  positionLabel?: string;
  name?: string;
  itemType?: string;
  quantity?: number | null;
  amount?: number | null;
  status?: 'existing' | 'new';
}

const TYPE_TAG_COLORS: Record<string, string> = {
  'раб': 'orange', 'суб-раб': 'purple', 'раб-комп.': 'volcano',
  'мат': 'blue', 'суб-мат': 'green', 'мат-комп.': 'cyan',
};

// Светлая тема — те же цвета что в Excel-экспорте
const ROW_COLORS_LIGHT: Record<string, string> = {
  'раб': '#FFE6CC',
  'суб-раб': '#E6D9F2',
  'раб-комп.': '#FFDDDD',
  'мат': '#D9EAFF',
  'суб-мат': '#E8F5E0',
  'мат-комп.': '#CCF2EF',
};

// Тёмная тема — насыщенные полупрозрачные оттенки
const ROW_COLORS_DARK: Record<string, string> = {
  'раб': 'rgba(255, 145, 40, 0.20)',
  'суб-раб': 'rgba(160, 90, 230, 0.20)',
  'раб-комп.': 'rgba(255, 75, 75, 0.20)',
  'мат': 'rgba(60, 130, 255, 0.20)',
  'суб-мат': 'rgba(50, 185, 80, 0.20)',
  'мат-комп.': 'rgba(0, 195, 185, 0.20)',
};

export const BoqPreviewTable: React.FC<BoqPreviewTableProps> = ({
  parsedData,
  positionUpdates,
  clientPositionsMap,
  existingItemsByPosition,
  additionalRows = [],
}) => {
  const { theme } = useTheme();
  const isDark = theme === 'dark';
  const rowColors = isDark ? ROW_COLORS_DARK : ROW_COLORS_LIGHT;
  const groupHeaderBg = isDark ? 'rgba(255,255,255,0.07)' : '#f0f0f0';

  const rows = useMemo<PreviewRow[]>(() => {
    const result: PreviewRow[] = [];
    const renderedDops = new Set<string>();

    const pushIncoming = (items: ParsedBoqItem[], groupKey: string) => {
      items.forEach((item, idx) => {
        const qty = item.quantity ?? item.base_quantity ?? null;
        const amount = (item.unit_rate && qty) ? Math.round(item.unit_rate * qty * 100) / 100 : null;
        result.push({
          key: `new-${groupKey}-${idx}`,
          isGroupHeader: false,
          name: item.nameText,
          itemType: item.boq_item_type,
          quantity: qty,
          amount,
          status: 'new',
        });
      });
    };

    const pushDopGroup = (dop: ParsedAdditionalPosition) => {
      renderedDops.add(dop.tempId);
      result.push({
        key: `g-${dop.tempId}`,
        isGroupHeader: true,
        isAdditional: true,
        positionLabel: `${dop.workName || '—'} (строка ${dop.rowIndex})`,
      });
      pushIncoming(parsedData.filter(item => item.additionalTempId === dop.tempId), dop.tempId);
    };

    positionUpdates.forEach((_, posNum) => {
      const position = clientPositionsMap.get(posNum);
      if (!position) return;

      const existing = existingItemsByPosition.get(position.id) || [];
      // Матчим по positionNumber через clientPositionsMap (matchedPositionId не заполнен до валидации);
      // элементы блоков «доп» идут не в позицию, а в свои новые ДОП.
      const incoming = parsedData.filter(item =>
        !item.additionalTempId && clientPositionsMap.get(item.positionNumber)?.id === position.id
      );
      const dops = additionalRows.filter(dop => dop.parentPositionNumber === posNum);

      if (existing.length === 0 && incoming.length === 0 && dops.length === 0) return;

      result.push({
        key: `g-${posNum}`,
        isGroupHeader: true,
        positionLabel: `${posNum}  ${position.work_name}`,
      });

      existing.forEach(item => {
        const wn = Array.isArray(item.work_names) ? item.work_names[0] : item.work_names;
        const mn = Array.isArray(item.material_names) ? item.material_names[0] : item.material_names;
        const name = wn?.name || mn?.name || '—';
        result.push({
          key: `ex-${item.id}`,
          isGroupHeader: false,
          name,
          itemType: item.boq_item_type ?? undefined,
          quantity: item.quantity,
          amount: item.total_amount,
          status: 'existing',
        });
      });

      pushIncoming(incoming, posNum);
      dops.forEach(pushDopGroup);
    });

    // «доп» без найденной позиции (ошибку покажет валидация) — в конце.
    additionalRows.filter(dop => !renderedDops.has(dop.tempId)).forEach(pushDopGroup);

    return result;
  }, [parsedData, positionUpdates, clientPositionsMap, existingItemsByPosition, additionalRows]);

  const columns = [
    {
      title: 'Наименование',
      key: 'name',
      ellipsis: true,
      render: (_: unknown, row: PreviewRow) => {
        if (row.isGroupHeader) {
          return {
            children: (
              <>
                {row.isAdditional && <Tag color="green" style={{ fontSize: 11 }}>Новая ДОП</Tag>}
                <Text strong style={{ fontSize: 12 }}>{row.positionLabel}</Text>
              </>
            ),
            props: { colSpan: 5 },
          };
        }
        return { children: <Text style={{ fontSize: 12 }}>{row.name}</Text>, props: { colSpan: 1 } };
      },
    },
    {
      title: 'Тип',
      key: 'type',
      width: 90,
      render: (_: unknown, row: PreviewRow) => {
        if (row.isGroupHeader) return { children: null, props: { colSpan: 0 } };
        return <Tag color={TYPE_TAG_COLORS[row.itemType || ''] || 'default'} style={{ fontSize: 11 }}>{row.itemType}</Tag>;
      },
    },
    {
      title: 'Кол-во',
      key: 'qty',
      width: 80,
      align: 'right' as const,
      render: (_: unknown, row: PreviewRow) => {
        if (row.isGroupHeader) return { children: null, props: { colSpan: 0 } };
        return <Text style={{ fontSize: 12 }}>{row.quantity != null ? row.quantity.toLocaleString('ru-RU', { maximumFractionDigits: 4 }) : '—'}</Text>;
      },
    },
    {
      title: 'Сумма',
      key: 'amount',
      width: 110,
      align: 'right' as const,
      render: (_: unknown, row: PreviewRow) => {
        if (row.isGroupHeader) return { children: null, props: { colSpan: 0 } };
        if (row.amount == null) return <Text type="secondary" style={{ fontSize: 12 }}>—</Text>;
        const formatted = row.amount.toLocaleString('ru-RU', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
        return <Text style={{ fontSize: 12 }}>{formatted}{row.status === 'new' ? '*' : ''}</Text>;
      },
    },
    {
      title: 'Статус',
      key: 'status',
      width: 110,
      align: 'center' as const,
      render: (_: unknown, row: PreviewRow) => {
        if (row.isGroupHeader) return { children: null, props: { colSpan: 0 } };
        return row.status === 'existing'
          ? <Tag style={{ fontSize: 11 }}>Существующий</Tag>
          : <Tag color="green" style={{ fontSize: 11 }}>Новый</Tag>;
      },
    },
  ];

  if (rows.length === 0) return null;

  return (
    <div>
      <Text type="secondary" style={{ fontSize: 11, display: 'block', marginBottom: 4 }}>
        * Сумма для новых строк ориентировочная (без учёта курса валют)
      </Text>
      <Table
        dataSource={rows}
        columns={columns}
        size="small"
        pagination={{ defaultPageSize: 50, showSizeChanger: false, simple: true }}
        onRow={(row: PreviewRow) => ({
          style: row.isGroupHeader
            ? { background: groupHeaderBg, fontWeight: 600 }
            : { background: rowColors[row.itemType || ''] || undefined },
        })}
        scroll={{ x: 500 }}
        style={{ fontSize: 12 }}
      />
    </div>
  );
};
