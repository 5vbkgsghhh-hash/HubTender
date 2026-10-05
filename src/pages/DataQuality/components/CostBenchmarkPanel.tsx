import React, { useMemo, useState } from 'react';
import { Alert, Button, Card, Empty, Select, Space, Spin, Switch, Table, Tag, Typography } from 'antd';
import { SettingOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { useAuth } from '../../../contexts/AuthContext';
import { BENCHMARK_PERIODS } from '../../../lib/api/costBenchmarks';
import {
  buildBenchmarkTree,
  canEditBenchmarkRanges,
  type CostBenchmarkTreeRow,
} from '../../../lib/quality/costBenchmarkPolicy';
import { useCostBenchmarks } from '../hooks/useCostBenchmarks';
import { BenchmarkCell } from './BenchmarkCell';
import { BenchmarkRangesModal } from './BenchmarkRangesModal';

const { Text } = Typography;

interface Props {
  tenderId: string;
  isPhone: boolean;
}

const money = (v: number): string => `${Math.round(v).toLocaleString('ru-RU')} ₽`;

/**
 * Сравнение с эталонами: ₽ на единицу объёма категории и ₽ на м² общей площади
 * по СП против диапазона из справочника или истории согласованных тендеров.
 */
export const CostBenchmarkPanel: React.FC<Props> = ({ tenderId, isPhone }) => {
  const { user } = useAuth();
  const { report, loading, error, period, setPeriod, reload } = useCostBenchmarks(tenderId);
  const [onlyDeviations, setOnlyDeviations] = useState(false);
  const [rangesOpen, setRangesOpen] = useState(false);
  const canEdit = canEditBenchmarkRanges(user?.role_code);

  const tree = useMemo(
    () =>
      report
        ? buildBenchmarkTree(report.rows, onlyDeviations, {
            areaSp: report.area_sp,
            calculationReady: report.calculation_ready,
          })
        : [],
    [report, onlyDeviations],
  );

  const columns: ColumnsType<CostBenchmarkTreeRow> = [
    {
      title: 'Категория затрат',
      key: 'name',
      render: (_, r) => (
        <Space direction="vertical" size={0}>
          <Text strong={r.level !== 'detail'}>
            {r.name}
            {r.location ? ` · ${r.location}` : ''}
          </Text>
          <Text type="secondary" style={{ fontSize: 12 }}>
            {money(r.commercial_total)}
            {r.volume !== null ? ` · объём ${r.volume.toLocaleString('ru-RU')} ${r.unit}` : ''}
          </Text>
        </Space>
      ),
    },
    {
      title: '₽ за единицу объёма',
      key: 'unit',
      width: isPhone ? 150 : 220,
      render: (_, r) => <BenchmarkCell a={r.per_volume_unit} />,
    },
    {
      title: '₽ за м² по СП',
      key: 'area',
      width: isPhone ? 150 : 220,
      render: (_, r) => <BenchmarkCell a={r.per_area_sp} />,
    },
  ];

  const s = report?.summary;
  const title = (
    <Space size={8} wrap>
      <span>Сравнение с эталонами</span>
      {s && report?.calculation_ready && (
        <>
          {s.above > 0 && <Tag color="red">выше: {s.above}</Tag>}
          {s.below > 0 && <Tag color="orange">ниже: {s.below}</Tag>}
          {s.conflicts > 0 && <Tag color="gold">справочник расходится с историей: {s.conflicts}</Tag>}
        </>
      )}
    </Space>
  );

  const extra = (
    <Space size={8} wrap>
      <Select
        size="small"
        value={period}
        onChange={setPeriod}
        options={BENCHMARK_PERIODS.map((p) => ({ value: p, label: `история ${p} мес.` }))}
        style={{ width: 140 }}
      />
      <Button size="small" icon={<SettingOutlined />} onClick={() => setRangesOpen(true)}>
        Диапазоны
      </Button>
    </Space>
  );

  let body: React.ReactNode;
  if (loading) {
    body = <Spin><div style={{ minHeight: 60 }} /></Spin>;
  } else if (error) {
    body = <Alert type="warning" showIcon message={error} />;
  } else if (!report || report.rows.length === 0) {
    body = <Empty description="В тендере нет строк с категориями затрат" />;
  } else {
    body = (
      <Space direction="vertical" size={8} style={{ width: '100%' }}>
        {!report.calculation_ready && (
          <Alert
            type="info"
            showIcon
            message="Расчёт тендера не актуален — коммерческие суммы пересчитываются. Сравнение появится после пересчёта."
          />
        )}
        <Space size={12} wrap>
          <Text type="secondary" style={{ fontSize: 12 }}>
            Класс: {report.housing_class ?? 'не задан'} · площадь по СП:{' '}
            {report.area_sp ? `${report.area_sp.toLocaleString('ru-RU')} м²` : 'не задана'} · в истории
            согласованных объектов: {report.history_tenders}
          </Text>
          <Space size={6}>
            <Switch size="small" checked={onlyDeviations} onChange={setOnlyDeviations} />
            <Text>Только отклонения</Text>
          </Space>
        </Space>
        <Table
          key={onlyDeviations ? 'deviations' : 'all'}
          rowKey="key"
          size="small"
          columns={columns}
          dataSource={tree}
          pagination={false}
          scroll={isPhone ? { x: 520 } : undefined}
          expandable={{ defaultExpandAllRows: onlyDeviations }}
        />
      </Space>
    );
  }

  return (
    <Card size="small" title={title} extra={extra}>
      {body}
      <BenchmarkRangesModal
        open={rangesOpen}
        canEdit={canEdit}
        onClose={() => setRangesOpen(false)}
        onChanged={reload}
      />
    </Card>
  );
};
