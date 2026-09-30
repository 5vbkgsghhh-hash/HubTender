import { FC, useState } from 'react';
import {
  Alert, Button, Card, Popconfirm, Space, Table, Tag, Tooltip, Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { DeleteOutlined, PlusOutlined, StopOutlined } from '@ant-design/icons';
import type { ApiKey } from '../../../lib/api/apiAccess';
import { formatDateTime } from '../utils/format';

const { Text, Paragraph } = Typography;

interface IApiKeysSectionProps {
  keys: ApiKey[];
  loading: boolean;
  onIssue: () => void;
  onRevoke: (id: string) => void;
  onDelete: (id: string) => void;
}

const STATUS_TAG: Record<ApiKey['status'], { color: string; label: string }> = {
  active: { color: 'green', label: 'Действует' },
  revoked: { color: 'red', label: 'Отозван' },
  expired: { color: 'default', label: 'Истёк' },
};

const SCOPE_LABEL: Record<string, string> = {
  'archive:read': 'Чтение архива',
  'archive:write': 'Сборка смет',
  'tenders:read': 'Чтение тендеров и смет',
  'tenders:write': 'Запись строк тендера',
  'verification:read': 'Чтение проверки данных',
  'verification:write': 'Действия проверяющего',
  'registry:read': 'Чтение перечня тендеров',
  'registry:write': 'Хронология и напоминания перечня',
};

export const ApiKeysSection: FC<IApiKeysSectionProps> = ({
  keys, loading, onIssue, onRevoke, onDelete,
}) => {
  const [showHint, setShowHint] = useState(true);

  const columns: ColumnsType<ApiKey> = [
    {
      title: 'Название',
      dataIndex: 'name',
      key: 'name',
      render: (name: string, row) => (
        <Space direction="vertical" size={0}>
          <Text strong>{name}</Text>
          <Text type="secondary" style={{ fontSize: 12 }}>
            <code>{row.key_prefix}…</code>
          </Text>
        </Space>
      ),
    },
    {
      title: 'Права',
      dataIndex: 'scopes',
      key: 'scopes',
      render: (scopes: string[]) => (
        <Space size={4} wrap>
          {scopes.map((s) => (
            <Tag key={s} color={s.endsWith(':write') ? 'orange' : 'blue'}>
              {SCOPE_LABEL[s] ?? s}
            </Tag>
          ))}
        </Space>
      ),
    },
    {
      title: 'Тендеры',
      dataIndex: 'allowed_tender_ids',
      key: 'tenders',
      render: (ids: string[]) =>
        ids.length === 0 ? (
          <Text type="secondary">все</Text>
        ) : (
          <Tooltip title={ids.join('\n')}>
            <Tag>{ids.length}</Tag>
          </Tooltip>
        ),
    },
    {
      title: 'Статус',
      dataIndex: 'status',
      key: 'status',
      render: (status: ApiKey['status'], row) => (
        <Space direction="vertical" size={0}>
          <Tag color={STATUS_TAG[status].color}>{STATUS_TAG[status].label}</Tag>
          {row.expires_at && status === 'active' && (
            <Text type="secondary" style={{ fontSize: 12 }}>
              до {formatDateTime(row.expires_at)}
            </Text>
          )}
        </Space>
      ),
    },
    {
      title: 'Использование',
      key: 'usage',
      render: (_, row) => (
        <Space direction="vertical" size={0}>
          <Text>{row.calls_last_24h} за сутки</Text>
          <Text type="secondary" style={{ fontSize: 12 }}>
            {row.last_used_at ? formatDateTime(row.last_used_at) : 'ни разу'}
          </Text>
        </Space>
      ),
    },
    {
      title: 'Выпущен',
      key: 'created',
      render: (_, row) => (
        <Space direction="vertical" size={0}>
          <Text>{formatDateTime(row.created_at)}</Text>
          <Text type="secondary" style={{ fontSize: 12 }}>
            {row.created_by_name ?? '—'}
          </Text>
        </Space>
      ),
    },
    {
      title: '',
      key: 'actions',
      width: 96,
      render: (_, row) => (
        <Space size={4}>
          {row.status === 'active' && (
            <Popconfirm
              title="Отозвать ключ?"
              description="Все вызовы с этим ключом перестанут работать сразу."
              okText="Отозвать"
              cancelText="Отмена"
              onConfirm={() => onRevoke(row.id)}
            >
              <Button type="text" danger icon={<StopOutlined />} />
            </Popconfirm>
          )}
          <Popconfirm
            title="Удалить ключ?"
            description="Записи журнала сохранятся, но ключ исчезнет из списка."
            okText="Удалить"
            cancelText="Отмена"
            onConfirm={() => onDelete(row.id)}
          >
            <Button type="text" danger icon={<DeleteOutlined />} />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <Card
      title="Ключи доступа"
      extra={
        <Button type="primary" icon={<PlusOutlined />} onClick={onIssue}>
          Выпустить ключ
        </Button>
      }
    >
      {showHint && (
        <Alert
          type="info"
          showIcon
          closable
          onClose={() => setShowHint(false)}
          style={{ marginBottom: 16 }}
          message="Как пользоваться ключом"
          description={
            <Paragraph style={{ marginBottom: 0 }}>
              Ключ передаётся в заголовке <code>X-API-Key</code>. Он действует от имени
              выпустившего его пользователя, поэтому все правки в сметах подписываются им.
              Секрет показывается один раз при выпуске — восстановить его нельзя, только
              выпустить новый.
            </Paragraph>
          }
        />
      )}

      <Table
        rowKey="id"
        columns={columns}
        dataSource={keys}
        loading={loading}
        pagination={false}
        size="small"
        scroll={{ x: 900 }}
        locale={{ emptyText: 'Ключей пока нет' }}
      />
    </Card>
  );
};
