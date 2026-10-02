import React from 'react';
import { Alert, Collapse, List, Space, Typography } from 'antd';
import { WarningOutlined } from '@ant-design/icons';
import type { ValidationResult } from '../../utils';

const { Text } = Typography;
const { Panel } = Collapse;

/** Панели ошибок и предупреждений валидации массового импорта BOQ (шаг 1). */
export const ValidationIssuesPanels: React.FC<{
  validationResult: ValidationResult | null;
}> = ({ validationResult }) => (
  <>
    {/* Ошибки валидации */}
    {validationResult && !validationResult.isValid && (
      <Collapse defaultActiveKey={['errors']} style={{ marginBottom: 16 }}>
        <Panel
          header={
            <Space>
              <WarningOutlined style={{ color: '#ff4d4f' }} />
              <span>Ошибки валидации ({validationResult.errors.length})</span>
            </Space>
          }
          key="errors"
        >
          {/* Несопоставленные позиции */}
          {validationResult.unmatchedPositions.length > 0 && (
            <Alert
              message="Позиции не найдены в тендере"
              description={
                <List
                  size="small"
                  dataSource={validationResult.unmatchedPositions}
                  renderItem={item => (
                    <List.Item>
                      <Text type="danger">
                        Позиция "{item.positionNumber}" — строки: {item.rows.join(', ')}
                      </Text>
                    </List.Item>
                  )}
                />
              }
              type="error"
              style={{ marginBottom: 8 }}
            />
          )}

          {/* Загрузка в нелистовые позиции (разделы/заголовки) */}
          {validationResult.nonLeafPositions.length > 0 && (
            <Alert
              message="Нельзя загружать работы/материалы в разделы/заголовки"
              description={
                <List
                  size="small"
                  dataSource={validationResult.nonLeafPositions}
                  renderItem={item => (
                    <List.Item>
                      <Text type="danger">
                        Позиция "{item.positionNumber}"{item.positionName ? ` — ${item.positionName}` : ''} — строки: {item.rows.join(', ')}
                      </Text>
                    </List.Item>
                  )}
                />
              }
              type="error"
              style={{ marginBottom: 8 }}
            />
          )}

          {/* Строки «доп» (новые ДОП) */}
          {(() => {
            const additionalErrors = validationResult.errors.filter(e => e.type === 'additional_error');
            if (additionalErrors.length === 0) return null;
            return (
              <Alert
                message={`Строки «доп» — новые ДОП (${additionalErrors.length})`}
                description={
                  <List
                    size="small"
                    dataSource={additionalErrors}
                    renderItem={item => (
                      <List.Item>
                        <Text type="danger">Строка {item.rowIndex}: {item.message}</Text>
                      </List.Item>
                    )}
                  />
                }
                type="error"
                style={{ marginBottom: 8 }}
              />
            );
          })()}

          {/* Отсутствующая номенклатура — можно добавить кнопкой в футере */}
          {validationResult.missingNomenclature.works.length > 0 && (
            <Alert
              message="Работы отсутствуют в номенклатуре — нажмите «Добавить в номенклатуру»"
              description={
                <List
                  size="small"
                  dataSource={validationResult.missingNomenclature.works}
                  renderItem={item => (
                    <List.Item>
                      <Text>
                        {item.name} [{item.unit}] — строки: {item.rows.join(', ')}
                      </Text>
                    </List.Item>
                  )}
                />
              }
              type="warning"
              style={{ marginBottom: 8 }}
            />
          )}

          {validationResult.missingNomenclature.materials.length > 0 && (
            <Alert
              message="Материалы отсутствуют в номенклатуре — нажмите «Добавить в номенклатуру»"
              description={
                <List
                  size="small"
                  dataSource={validationResult.missingNomenclature.materials}
                  renderItem={item => (
                    <List.Item>
                      <Text>
                        {item.name} [{item.unit}] — строки: {item.rows.join(', ')}
                      </Text>
                    </List.Item>
                  )}
                />
              }
              type="warning"
              style={{ marginBottom: 8 }}
            />
          )}

          {/* Неизвестные затраты */}
          {validationResult.unknownCosts.length > 0 && (
            <Alert
              message="Затраты не найдены в БД"
              description={
                <List
                  size="small"
                  dataSource={validationResult.unknownCosts}
                  renderItem={item => (
                    <List.Item>
                      <Text type="danger">
                        {item.text} — строки: {item.rows.join(', ')}
                      </Text>
                    </List.Item>
                  )}
                />
              }
              type="error"
              style={{ marginBottom: 8 }}
            />
          )}

          {/* Прочие ошибки (отсутствующие поля, неверные типы, ошибки привязки) */}
          {(() => {
            const otherErrors = validationResult.errors.filter(
              e => !['position_not_found', 'missing_nomenclature', 'missing_cost', 'non_leaf_position', 'additional_error'].includes(e.type)
            );
            if (otherErrors.length === 0) return null;
            return (
              <Alert
                message={`Прочие ошибки (${otherErrors.length})`}
                description={
                  <List
                    size="small"
                    dataSource={otherErrors.slice(0, 50)}
                    renderItem={item => (
                      <List.Item>
                        <Text type="danger">
                          Строка {item.rowIndex}: {item.message}
                        </Text>
                      </List.Item>
                    )}
                    footer={otherErrors.length > 50 ? <Text type="secondary">...и ещё {otherErrors.length - 50} ошибок</Text> : undefined}
                  />
                }
                type="error"
              />
            );
          })()}
        </Panel>
      </Collapse>
    )}

    {/* Предупреждения (не блокируют импорт): незаполненные коэффициенты и т.п. */}
    {validationResult && validationResult.warnings.length > 0 && (
      <Collapse style={{ marginBottom: 16 }}>
        <Panel
          header={
            <Space>
              <WarningOutlined style={{ color: '#faad14' }} />
              <span>Предупреждения ({validationResult.warnings.length}) — не блокируют импорт</span>
            </Space>
          }
          key="warnings"
        >
          <List
            size="small"
            dataSource={validationResult.warnings.slice(0, 50)}
            renderItem={item => (
              <List.Item>
                <Text type="warning">
                  Строка {item.rowIndex}: {item.message}
                </Text>
              </List.Item>
            )}
            footer={validationResult.warnings.length > 50 ? <Text type="secondary">...и ещё {validationResult.warnings.length - 50} предупреждений</Text> : undefined}
          />
        </Panel>
      </Collapse>
    )}
  </>
);
