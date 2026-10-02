import React from 'react';
import { Alert, List, Typography, Upload } from 'antd';
import { FileExcelOutlined } from '@ant-design/icons';

const { Dragger } = Upload;
const { Text } = Typography;

/** Шаг 0 массового импорта BOQ: инструкция по формату файла + Dragger. */
export const UploadStep: React.FC<{
  uploading: boolean;
  nomenclatureLoaded: boolean;
  onFileUpload: (file: File) => Promise<boolean> | boolean;
}> = ({ uploading, nomenclatureLoaded, onFileUpload }) => (
  <div>
    <Alert
      message="Формат файла для массового импорта"
      description={
        <div style={{ marginTop: 8 }}>
          <Text strong>Обязательные колонки:</Text>
          <List size="small" style={{ marginTop: 4 }}>
            <List.Item>
              <Text>Колонка 2: <Text code>Номер позиции</Text> — для сопоставления с позициями тендера</Text>
            </List.Item>
            <List.Item>
              <Text>Колонка 3: <Text code>Затрата на строительство</Text></Text>
            </List.Item>
            <List.Item>
              <Text>Колонка 5: <Text code>Тип элемента</Text> (раб, суб-раб, мат, суб-мат...)</Text>
            </List.Item>
            <List.Item>
              <Text>Колонка 7: <Text code>Наименование</Text></Text>
            </List.Item>
            <List.Item>
              <Text>Колонка 8: <Text code>Ед. изм.</Text></Text>
            </List.Item>
            <List.Item>
              <Text type="secondary">
                Если в позиции импортируются только материалы без работы, такая строка будет импортирована как независимый материал без привязки к работе.
              </Text>
            </List.Item>
          </List>
          <Text strong style={{ marginTop: 8, display: 'block' }}>Данные ГП для позиций (можно импортировать без работ/материалов):</Text>
          <List size="small" style={{ marginTop: 4 }}>
            <List.Item>
              <Text>Колонка 12: <Text code>Количество ГП</Text> — обновит manual_volume в позиции</Text>
            </List.Item>
            <List.Item>
              <Text>Колонка 20: <Text code>Примечание ГП</Text> — обновит manual_note в позиции</Text>
            </List.Item>
            <List.Item>
              <Text type="secondary">Достаточно указать номер позиции + количество/примечание ГП. Существующие значения будут перезаписаны.</Text>
            </List.Item>
          </List>
          <Text strong style={{ marginTop: 8, display: 'block' }}>Новые ДОП-работы (строка «доп»):</Text>
          <List size="small" style={{ marginTop: 4 }}>
            <List.Item>
              <Text>
                Колонка 5: <Text code>доп</Text>, колонки 7/8/12/20 — наименование, ед. изм., количество ГП и примечание ГП ДОП.
                Колонка 2 пустая — номер ДОП (например 5.1) присваивается при загрузке.
              </Text>
            </List.Item>
            <List.Item>
              <Text type="secondary">
                ДОП создаётся к ближайшей позиции заказчика выше. Работы и материалы ниже строки «доп» с пустой колонкой 2 попадают в эту ДОП —
                до следующей строки с номером или следующей «доп». Повторно создать ДОП с тем же наименованием у позиции нельзя.
              </Text>
            </List.Item>
          </List>
        </div>
      }
      type="info"
      style={{ marginBottom: 16 }}
    />

    <Alert
      message="Сопоставление по номеру позиции"
      description="Номер позиции из Excel будет сопоставлен с полем position_number в базе данных. Убедитесь, что номера совпадают (5 = 5.0 = 5.00)."
      type="warning"
      style={{ marginBottom: 16 }}
    />

    <Dragger
      beforeUpload={(file) => {
        onFileUpload(file as File);
        return false;
      }}
      accept=".xlsx,.xls"
      maxCount={1}
      disabled={uploading || !nomenclatureLoaded}
      showUploadList={false}
    >
      <p className="ant-upload-drag-icon">
        <FileExcelOutlined style={{ color: '#10b981', fontSize: 48 }} />
      </p>
      <p className="ant-upload-text">
        {nomenclatureLoaded
          ? 'Нажмите или перетащите Excel файл'
          : 'Загрузка справочников...'
        }
      </p>
      <p className="ant-upload-hint">Поддерживаются форматы: .xlsx, .xls</p>
    </Dragger>
  </div>
);
