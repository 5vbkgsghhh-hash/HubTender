import React from 'react';
import { Upload, Alert, List, Typography } from 'antd';
import { FileExcelOutlined } from '@ant-design/icons';

const { Text } = Typography;
const { Dragger } = Upload;

interface FileUploadStepProps {
  onFileUpload: (file: File) => Promise<boolean>;
  uploading: boolean;
  nomenclatureLoaded: boolean;
}

export const FileUploadStep: React.FC<FileUploadStepProps> = ({ onFileUpload, uploading, nomenclatureLoaded }) => {
  return (
    <div style={{ width: '100%' }}>
      {/* Инструкция по формату файла */}
      <Alert
        message="Формат Excel файла"
        description={
          <List size="small" style={{ marginTop: 8 }}>
            <List.Item>
              <Text type="secondary">Колонка 1: Номер позиции (не импортируется)</Text>
            </List.Item>
            <List.Item>
              <Text type="secondary">Колонка 2: № п/п (не импортируется)</Text>
            </List.Item>
            <List.Item>
              <Text>Колонка 3: <Text code>Затрата на строительство</Text> <Text strong>(обязательно)</Text></Text>
            </List.Item>
            <List.Item>
              <Text>Колонка 4: <Text code>Привязка материала к работе</Text> ("да" или пусто)</Text>
            </List.Item>
            <List.Item>
              <Text>Колонка 5: <Text code>Тип элемента</Text> (раб, суб-раб, раб-комп., мат, суб-мат, мат-комп.)</Text>
            </List.Item>
            <List.Item>
              <Text>Колонка 6: <Text code>Тип материала</Text> (основной/основн./осн. → "основн.", вспомогательный/вспомогат./вспом. → "вспомогат.")</Text>
            </List.Item>
            <List.Item>
              <Text>Колонка 7: <Text code>Наименование</Text> (обязательное, должно быть в номенклатуре!)</Text>
            </List.Item>
            <List.Item>
              <Text>Колонка 8: <Text code>Ед. изм.</Text> (обязательное, должно совпадать с номенклатурой!)</Text>
            </List.Item>
            <List.Item>
              <Text type="secondary">Колонка 9: Количество заказчика (не импортируется)</Text>
            </List.Item>
            <List.Item>
              <Text>Колонка 10: <Text code>Коэфф. перевода</Text> (число)</Text>
            </List.Item>
            <List.Item>
              <Text>Колонка 11: <Text code>Коэфф. расхода</Text> (число)</Text>
            </List.Item>
            <List.Item>
              <Text>Колонка 12: <Text code>Количество</Text> (число)</Text>
            </List.Item>
            <List.Item>
              <Text>Колонка 13: <Text code>Валюта</Text> (RUB, USD, EUR, CNY)</Text>
            </List.Item>
            <List.Item>
              <Text>Колонка 14: <Text code>Тип доставки</Text> (в цене, не в цене, суммой)</Text>
            </List.Item>
            <List.Item>
              <Text>Колонка 15: <Text code>Стоимость доставки</Text> (число)</Text>
            </List.Item>
            <List.Item>
              <Text>Колонка 16: <Text code>Цена за единицу</Text> (число)</Text>
            </List.Item>
            <List.Item>
              <Text type="secondary">Колонка 17: Итоговая сумма (рассчитывается автоматически, не импортируется)</Text>
            </List.Item>
            <List.Item>
              <Text>Колонка 18: <Text code>Ссылка на КП</Text> (текст)</Text>
            </List.Item>
            <List.Item>
              <Text type="secondary">Колонка 19: Примечание заказчика (не импортируется)</Text>
            </List.Item>
            <List.Item>
              <Text>Колонка 20: <Text code>Примечание ГП</Text> (текст)</Text>
            </List.Item>
            <List.Item>
              <Text>
                Новая ДОП-работа: строка с <Text code>доп</Text> в колонке 5 (наименование, ед. изм., количество ГП и
                примечание ГП — в колонках 7/8/12/20). ДОП создаётся к этой позиции заказчика, номер присваивается при
                загрузке; строки ниже «доп» попадают в эту ДОП до следующей «доп», строки выше — в текущую позицию.
              </Text>
            </List.Item>
          </List>
        }
        type="info"
        style={{ marginBottom: 16 }}
      />

      <Alert
        message="ВАЖНО: Строгая валидация"
        description={
          <div style={{ marginTop: 8 }}>
            <Text strong>Все наименования работ и материалов ДОЛЖНЫ существовать в номенклатуре!</Text>
            <br />
            <Text>Связка "наименование + единица измерения" должна точно совпадать с данными в БД.</Text>
            <br />
            <Text type="danger">
              При несовпадении или отсутствии обязательных данных импорт будет остановлен со списком ошибок.
            </Text>
          </div>
        }
        type="warning"
        style={{ marginBottom: 16 }}
      />

      {/* Загрузчик файла */}
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
        <p className="ant-upload-text">Нажмите или перетащите Excel файл</p>
        <p className="ant-upload-hint">
          Поддерживаются форматы: .xlsx, .xls
        </p>
      </Dragger>
    </div>
  );
};
