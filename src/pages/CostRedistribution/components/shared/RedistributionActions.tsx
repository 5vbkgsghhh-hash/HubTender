/**
 * Панель действий для перераспределения
 */

import React from 'react';
import { Button, Space, Card, Popconfirm } from 'antd';
import {
  ArrowRightOutlined,
  ClearOutlined,
} from '@ant-design/icons';

interface RedistributionActionsProps {
  canCalculate: boolean;
  isCalculated: boolean;
  saving?: boolean;
  onGoToResults: () => void;
  onClear: () => void;
}

export const RedistributionActions: React.FC<RedistributionActionsProps> = ({
  canCalculate,
  isCalculated,
  saving = false,
  onGoToResults,
  onClear,
}) => {
  return (
    <Card>
      <Space wrap>
        <Button
          type="primary"
          icon={<ArrowRightOutlined />}
          onClick={onGoToResults}
          disabled={!canCalculate}
          loading={saving}
          size="large"
        >
          Перейти к результатам
        </Button>

        <Popconfirm
          title="Очистить перераспределение?"
          description="Правила и сохранённый расчёт будут удалены"
          onConfirm={onClear}
          okText="Да"
          cancelText="Нет"
        >
          <Button
            icon={<ClearOutlined />}
            disabled={!isCalculated}
            danger
            size="large"
          >
            Очистить
          </Button>
        </Popconfirm>
      </Space>
    </Card>
  );
};
