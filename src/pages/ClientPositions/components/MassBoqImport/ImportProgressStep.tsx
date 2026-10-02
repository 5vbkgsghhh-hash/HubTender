import React from 'react';
import { Alert, Progress, Space, Table } from 'antd';
import type { ImportTotalMismatch } from '../../utils/massBoqImportPayload';
import {
  formatCreatedAdditionalNumbers,
  type CreatedAdditionalPosition,
} from '../../../../utils/boq/additionalImport';

const fmtMoney = (v: number) =>
  v.toLocaleString('ru-RU', { minimumFractionDigits: 2, maximumFractionDigits: 2 });

/** Компактная таблица расхождений: сумма из файла vs авторитетный расчёт
 *  сервера. Диагностика — в БД сохранены серверные значения. */
const MismatchTable: React.FC<{ mismatches: ImportTotalMismatch[] }> = ({ mismatches }) => (
  <Table<ImportTotalMismatch>
    size="small"
    rowKey={(m, i) => `${m.row_number}-${i}`}
    dataSource={mismatches}
    pagination={mismatches.length > 10 ? { pageSize: 10, size: 'small' } : false}
    scroll={{ x: true }}
    columns={[
      { title: 'Строка', dataIndex: 'row_number', width: 70,
        render: (v: number) => (v > 0 ? v : '—') },
      { title: 'Элемент', dataIndex: 'item_name', ellipsis: true },
      { title: 'Из файла', dataIndex: 'client_total_amount', align: 'right',
        render: fmtMoney },
      { title: 'Сервер (сохранено)', dataIndex: 'server_total_amount', align: 'right',
        render: fmtMoney },
      { title: 'Δ', dataIndex: 'absolute_difference', align: 'right',
        render: fmtMoney },
      { title: 'Δ %', dataIndex: 'relative_difference_percent', align: 'right', width: 80,
        render: (v: number) => `${v.toFixed(2)}%` },
    ]}
  />
);

/** Шаг 2 массового импорта BOQ: прогресс / успех / ошибка + отчёт расхождений. */
export const ImportProgressStep: React.FC<{
  importStatus: 'idle' | 'running' | 'success' | 'error';
  importError: string | null;
  importMismatches: ImportTotalMismatch[];
  insertedCount: number;
  uploadProgress: number;
  parsedDataLength: number;
  matchedCount: number;
  positionOnlyCount: number;
  /** Строк «доп» в файле и ДОП, созданных сервером. */
  additionalCount: number;
  createdAdditional: CreatedAdditionalPosition[];
}> = ({
  importStatus, importError, importMismatches, insertedCount,
  uploadProgress, parsedDataLength, matchedCount, positionOnlyCount,
  additionalCount, createdAdditional,
}) => (
  <Space direction="vertical" style={{ width: '100%' }} size="middle">
    {importStatus === 'running' && (
      <>
        <Alert
          type="info"
          message="Импорт данных"
          description={
            (parsedDataLength > 0
              ? `Импортируется ${parsedDataLength} элементов в ${matchedCount} позиций${positionOnlyCount > 0 ? ` + обновление ${positionOnlyCount} поз. ГП` : ''}`
              : `Обновляется ${positionOnlyCount} позиций (данные ГП)`) +
            (additionalCount > 0 ? `, создаётся ДОП: ${additionalCount}` : '')
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
          `Импортировано строк: ${insertedCount}. Ошибок: 0. ` +
          (createdAdditional.length > 0
            ? `Создано ДОП: ${createdAdditional.length} (${formatCreatedAdditionalNumbers(createdAdditional)}). `
            : '') +
          `Расхождений сумм: ${importMismatches.length}.`
        }
        showIcon
      />
    )}
    {importStatus === 'success' && importMismatches.length > 0 && (
      <>
        <Alert
          type="warning"
          message={`Суммы ${importMismatches.length} строк(и) пересчитаны сервером`}
          description="Итог каждой строки рассчитывается сервером по курсам тендера. Значения из файла ниже отличались и НЕ были сохранены."
          showIcon
        />
        <MismatchTable mismatches={importMismatches} />
      </>
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
);
