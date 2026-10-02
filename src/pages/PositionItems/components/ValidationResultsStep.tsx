import React from 'react';
import { Alert, Card, List, Tag, Typography, Space, Collapse } from 'antd';
import {
  CheckCircleOutlined,
  ExclamationCircleOutlined,
  WarningOutlined,
  CloseCircleOutlined,
} from '@ant-design/icons';
import { AdditionalRowsPreview } from '../../../components/BoqImport/AdditionalRowsPreview';
import type { ParsedAdditionalRow } from '../../../utils/boq/additionalImport';

const { Text } = Typography;
const { Panel } = Collapse;

interface ValidationError {
  rowIndex: number;
  type: string;
  field: string;
  message: string;
  severity: 'error' | 'warning';
}

interface MissingNomenclatureGroup {
  name: string;
  unit: string;
  rows: number[];
}

interface ValidationResult {
  isValid: boolean;
  errors: ValidationError[];
  warnings: ValidationError[];
  missingNomenclature: {
    works: MissingNomenclatureGroup[];
    materials: MissingNomenclatureGroup[];
  };
  unknownCosts: Array<{ text: string; rows: number[] }>;
}

interface ValidationResultsStepProps {
  validationResult: ValidationResult;
  totalRows: number;
  /** Строки «доп» — новые ДОП к позиции. */
  additionalRows?: ParsedAdditionalRow[];
}

const ADDITIONAL_HINT =
  'ДОП создаются к позиции заказчика этой страницы, номер присвоится при загрузке; ' +
  'работы и материалы под строкой «доп» попадут в эту ДОП.';

export const ValidationResultsStep: React.FC<ValidationResultsStepProps> = ({
  validationResult,
  totalRows,
  additionalRows = [],
}) => {
  const { isValid, errors, warnings, missingNomenclature, unknownCosts } = validationResult;

  // Группировка ошибок по типу
  const errorsByType = errors.reduce((acc, error) => {
    if (!acc[error.type]) {
      acc[error.type] = [];
    }
    acc[error.type].push(error);
    return acc;
  }, {} as Record<string, ValidationError[]>);

  const getTypeLabel = (type: string): string => {
    const labels: Record<string, string> = {
      missing_nomenclature: 'Отсутствует в номенклатуре',
      unit_mismatch: 'Несоответствие единиц измерения',
      missing_cost: 'Неизвестная затрата',
      invalid_type: 'Недопустимый тип',
      missing_field: 'Пустое обязательное поле',
      binding_error: 'Ошибка привязки материала',
      additional_error: 'Строки «доп» — новые ДОП',
    };
    return labels[type] || type;
  };

  if (isValid) {
    return (
      <Space direction="vertical" style={{ width: '100%' }} size="middle">
        <Alert
          type="success"
          icon={<CheckCircleOutlined />}
          message={
            <Text strong style={{ fontSize: 16 }}>
              Валидация пройдена успешно!
            </Text>
          }
          description={
            <Space direction="vertical">
              <Text>
                Все {totalRows} строк готовы к импорту
                {additionalRows.length > 0 ? `, будет создано ДОП: ${additionalRows.length}` : ''}.
              </Text>
              {warnings.length > 0 && (
                <Text type="warning">
                  Найдено {warnings.length} предупреждений (не блокируют импорт).
                </Text>
              )}
            </Space>
          }
          showIcon
        />

        <AdditionalRowsPreview rows={additionalRows} hint={ADDITIONAL_HINT} />

        {/* Предупреждения (не критичные) */}
        {warnings.length > 0 && (
          <Card size="small" title={<Text type="warning">Предупреждения</Text>}>
            <Collapse ghost>
              {unknownCosts.length > 0 && (
                <Panel
                  header={
                    <Space>
                      <WarningOutlined style={{ color: '#faad14' }} />
                      <Text>Неизвестные затраты на строительство ({unknownCosts.length})</Text>
                    </Space>
                  }
                  key="unknownCosts"
                >
                  <List
                    size="small"
                    dataSource={unknownCosts}
                    renderItem={(cost) => (
                      <List.Item>
                        <Space direction="vertical" style={{ width: '100%' }}>
                          <Text strong>{cost.text}</Text>
                          <Text type="secondary">
                            Строки: {cost.rows.join(', ')}
                          </Text>
                        </Space>
                      </List.Item>
                    )}
                  />
                </Panel>
              )}
            </Collapse>
          </Card>
        )}
      </Space>
    );
  }

  // Есть ошибки - показываем детали
  return (
    <Space direction="vertical" style={{ width: '100%' }} size="middle">
      <Alert
        type="error"
        icon={<CloseCircleOutlined />}
        message={
          <Text strong style={{ fontSize: 16 }}>
            Обнаружено {errors.length} ошибок
          </Text>
        }
        description={
          <Space direction="vertical">
            <Text>Импорт остановлен. Устраните все ошибки и повторите загрузку файла.</Text>
            <Text type="danger" strong>
              Все наименования работ и материалов должны существовать в номенклатуре с точным
              совпадением единиц измерения.
            </Text>
          </Space>
        }
        showIcon
      />

      {/* Группировка: Отсутствующие в номенклатуре */}
      {missingNomenclature.works.length > 0 && (
        <Card
          size="small"
          title={
            <Space>
              <ExclamationCircleOutlined style={{ color: '#ff4d4f' }} />
              <Text type="danger">
                Работы отсутствуют в номенклатуре ({missingNomenclature.works.length})
              </Text>
            </Space>
          }
        >
          <List
            size="small"
            dataSource={missingNomenclature.works}
            renderItem={(work) => (
              <List.Item>
                <Space direction="vertical" style={{ width: '100%' }}>
                  <Space>
                    <Tag color="red">{work.unit}</Tag>
                    <Text strong>{work.name}</Text>
                  </Space>
                  <Text type="secondary">Строки: {work.rows.join(', ')}</Text>
                  <Alert
                    type="error"
                    message="Необходимо добавить эту работу в номенклатуру работ"
                    showIcon
                    style={{ marginTop: 4 }}
                  />
                </Space>
              </List.Item>
            )}
          />
        </Card>
      )}

      {missingNomenclature.materials.length > 0 && (
        <Card
          size="small"
          title={
            <Space>
              <ExclamationCircleOutlined style={{ color: '#ff4d4f' }} />
              <Text type="danger">
                Материалы отсутствуют в номенклатуре ({missingNomenclature.materials.length})
              </Text>
            </Space>
          }
        >
          <List
            size="small"
            dataSource={missingNomenclature.materials}
            renderItem={(material) => (
              <List.Item>
                <Space direction="vertical" style={{ width: '100%' }}>
                  <Space>
                    <Tag color="blue">{material.unit}</Tag>
                    <Text strong>{material.name}</Text>
                  </Space>
                  <Text type="secondary">Строки: {material.rows.join(', ')}</Text>
                  <Alert
                    type="error"
                    message="Необходимо добавить этот материал в номенклатуру материалов"
                    showIcon
                    style={{ marginTop: 4 }}
                  />
                </Space>
              </List.Item>
            )}
          />
        </Card>
      )}

      {/* Отсутствующие обязательные поля */}
      {errorsByType['missing_field'] && errorsByType['missing_field'].length > 0 && (
        <Card
          size="small"
          title={
            <Space>
              <ExclamationCircleOutlined style={{ color: '#ff4d4f' }} />
              <Text type="danger">
                Отсутствуют обязательные данные ({errorsByType['missing_field'].length})
              </Text>
            </Space>
          }
        >
          <List
            size="small"
            dataSource={errorsByType['missing_field']}
            renderItem={(error) => (
              <List.Item>
                <Space direction="vertical" style={{ width: '100%' }}>
                  <Space>
                    <Tag color="red">Строка {error.rowIndex}</Tag>
                    <Text strong>{error.message}</Text>
                  </Space>
                  <Alert
                    type="error"
                    message="Заполните обязательное поле в Excel файле"
                    showIcon
                    style={{ marginTop: 4 }}
                  />
                </Space>
              </List.Item>
            )}
          />
        </Card>
      )}

      {/* Неизвестные затраты на строительство */}
      {errorsByType['missing_cost'] && errorsByType['missing_cost'].length > 0 && (
        <Card
          size="small"
          title={
            <Space>
              <ExclamationCircleOutlined style={{ color: '#ff4d4f' }} />
              <Text type="danger">
                Неизвестные затраты на строительство ({errorsByType['missing_cost'].length})
              </Text>
            </Space>
          }
        >
          <List
            size="small"
            dataSource={errorsByType['missing_cost']}
            renderItem={(error) => (
              <List.Item>
                <Space direction="vertical" style={{ width: '100%' }}>
                  <Space>
                    <Tag color="red">Строка {error.rowIndex}</Tag>
                    <Text strong>{error.message}</Text>
                  </Space>
                  <Alert
                    type="error"
                    message="Необходимо добавить эту затрату через раздел Администрирование → Строительные затраты"
                    showIcon
                    style={{ marginTop: 4 }}
                  />
                </Space>
              </List.Item>
            )}
          />
        </Card>
      )}

      {/* Остальные типы ошибок */}
      {Object.entries(errorsByType)
        .filter(([type]) => !['missing_nomenclature', 'missing_field', 'missing_cost'].includes(type))
        .map(([type, typeErrors]) => (
          <Card
            key={type}
            size="small"
            title={
              <Space>
                <ExclamationCircleOutlined style={{ color: '#ff4d4f' }} />
                <Text type="danger">
                  {getTypeLabel(type)} ({typeErrors.length})
                </Text>
              </Space>
            }
          >
            <List
              size="small"
              dataSource={typeErrors}
              renderItem={(error) => (
                <List.Item>
                  <Space>
                    <Tag color="red">Строка {error.rowIndex}</Tag>
                    <Text>{error.message}</Text>
                  </Space>
                </List.Item>
              )}
            />
          </Card>
        ))}
    </Space>
  );
};
