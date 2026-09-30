# Документация страницы тендеров

## Обзор

Страница тендеров (`/admin/tenders`) предоставляет комплексное управление тендерами с загрузкой BOQ из Excel, контролем версий и мастером маппинга для импорта позиций заказчика.

**Маршрут**: `/admin/tenders`
**Компонент**: `src/pages/Admin/Tenders/Tenders.tsx`
**Уровень доступа**: Пользователи-администраторы

## Возможности

### 1. Таблица управления тендерами
- **Сложная таблица**: Вся информация о тендере в одном представлении
- **Выбор строк**: Множественный выбор для массовых операций
- **Пагинация**: 10 на странице с выбором размера
- **Поиск**: Фильтр по номеру тендера, названию, описанию
- **Меню действий**: Контекстное меню для каждой строки

### 2. Загрузка BOQ из Excel
- **Многошаговый мастер**: 3-шаговый процесс загрузки
  1. Загрузка файла Excel
  2. Маппинг колонок на поля базы данных
  3. Предпросмотр и подтверждение
- **Маппинг колонок**: Гибкое сопоставление полей
- **Валидация**: Проверка данных перед импортом
- **Предпросмотр**: Просмотр данных перед фиксацией

### 3. Контроль версий
- **Множественные версии**: Отслеживание ревизий тендера
- **Сравнение версий**: Сравнение версий бок о бок
- **Сопоставление версий**: Сопоставление позиций между версиями
- **Автоинкремент**: Автоматическое назначение номеров версий

### 4. Действия с тендерами
- **Создать**: Добавить новый тендер
- **Редактировать**: Изменить детали тендера
- **Удалить**: Удалить тендер (с подтверждением)
- **Копировать**: Дублировать тендер
- **Архивировать**: Переместить в архив
- **Экспорт**: Экспорт данных тендера
- **Загрузить BOQ**: Импорт позиций заказчика

### 5. Отображение информации о тендере
**Колонки**:
1. Номер тендера
2. Название тендера
3. Имя заказчика
4. Описание
5. Срок сдачи (дни до истечения)
6. Статус
7. Версия
8. Курсы валют (USD, EUR, CNY)
9. Действия

## UI-компоненты

### TendersToolbar
**Назначение**: Панель поиска и действий

**Элементы**:
- Поле поиска (300px)
- Кнопка "Экспортировать все"
- Кнопка "Создать новый тендер"

### TendersTable
**Назначение**: Основная таблица отображения тендеров

**Возможности**:
- Чекбоксы выбора строк
- Сортируемые колонки
- Раскрываемые строки (в будущем)
- Меню действий для каждой строки
- Индикаторы статуса

### TenderModal
**Назначение**: Форма создания/редактирования тендера

**Поля**:
- Номер тендера (обязательно)
- Название тендера (обязательно)
- Имя заказчика (обязательно)
- Описание (необязательно)
- Дата крайнего срока (обязательно)
- Статус (Select: Активный, На паузе, Завершен, Архив)
- Версия (автозаполнение)
- Курсы валют:
  - Курс USD
  - Курс EUR
  - Курс CNY

**Валидация**:
- Обязательные поля принудительно требуются
- Положительные числа для курсов
- Допустимый формат даты

### UploadBOQModal
**Назначение**: 3-шаговый мастер импорта BOQ

#### Шаг 1: Загрузка
**Возможности**:
- Загрузка файла (перетаскивание или клик)
- Валидация файла Excel (.xlsx, .xls)
- Проверка размера файла
- Индикатор прогресса парсинга

**Принимаемые форматы**:
- Excel 2007+ (.xlsx)
- Excel 97-2003 (.xls)

#### Шаг 2: Маппинг
**Возможности**:
- Предпросмотр колонок из Excel
- Выпадающий маппинг для каждого поля:
  - Номер позиции → Колонка
  - Название работы → Колонка
  - Код единицы измерения → Колонка
  - Объём → Колонка
  - Номер раздела → Колонка (необязательно)
- Автоопределение общих названий
- Опция пропуска колонок

**Обязательные маппинги**:
- Номер позиции (должен быть смапирован)
- Название работы (должно быть смапировано)
- Код единицы измерения (должен быть смапирован)
- Объём (должен быть смапирован)

**Необязательные маппинги**:
- Номер раздела
- Примечания
- Пользовательские поля

#### Шаг 3: Предпросмотр
**Возможности**:
- Предпросмотр таблицы распарсенных данных
- Отображаются примерные строки (первые 10)
- Ошибки валидации подсвечены
- Общее количество строк
- Кнопки подтверждения/отмены

**Проверки валидации**:
- Наличие обязательных полей
- Валидация формата чисел
- Код единицы измерения существует в базе данных
- Уникальность номера позиции

### TendersActionMenu
**Назначение**: Выпадающее меню действий для каждой строки

**Действия**:
1. **Редактировать**: Открыть модальное окно редактирования
2. **Загрузить BOQ**: Открыть мастер загрузки BOQ
3. **Копировать**: Дублировать тендер
4. **Архивировать**: Переместить в архив
5. **Экспорт**: Экспорт данных тендера
6. **Удалить**: Удалить тендер (красная, опасная)

**Структура меню**:
```tsx
<Dropdown.Button menu={actionMenu}>
  Действия
</Dropdown.Button>
```

### VersionMatchModal
**Назначение**: Сопоставление позиций между версиями тендера

**Возможности**:
- Сравнение позиций бок о бок
- Автоматическое сопоставление с сохранением порядка строк (как diff): якоря по точному названию, нечёткий поиск только между соседними якорями; номер раздела учитывается, только если заказчик не перенумеровал разделы
- Ручное переопределение сопоставлений
- Обработка дополнительных работ
- Статистика сопоставлений
- Массовое сопоставление/отмена сопоставления

**Случай использования**:
Когда заказчик предоставляет обновлённую BOQ с изменениями позиций, сопоставьте старые позиции с новыми для сохранения данных.

## Модель данных

### Таблица: `tenders`
```sql
CREATE TABLE tenders (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  tender_number TEXT NOT NULL,
  title TEXT NOT NULL,
  client_name TEXT NOT NULL,
  description TEXT,
  deadline_date DATE,
  status TEXT DEFAULT 'Активный',
  version INTEGER DEFAULT 1,
  usd_rate NUMERIC(10,4) DEFAULT 1.0,
  eur_rate NUMERIC(10,4) DEFAULT 1.0,
  cny_rate NUMERIC(10,4) DEFAULT 1.0,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
  user_id UUID REFERENCES auth.users(id)
);

CREATE INDEX idx_tenders_title_version ON tenders(title, version);
```

**Поля**:
- `tender_number`: Уникальный идентификатор (например, "ТЕН-2025-001")
- `title`: Название/описание тендера
- `client_name`: Название компании-заказчика
- `description`: Дополнительные детали
- `deadline_date`: Крайний срок подачи
- `status`: Текущий статус
- `version`: Номер версии (для отслеживания изменений)
- `usd_rate`, `eur_rate`, `cny_rate`: Курсы конвертации валют

### Связанные таблицы
- `client_positions`: Позиции BOQ для тендера
- `markup_tactics`: Стратегии наценок для тендера
- `tender_markup_percentage`: Процентные ставки наценок
- `construction_cost_volumes`: Объёмы работ для тендера

## API-эндпоинты (Supabase)

### Получение всех тендеров
```typescript
const { data, error } = await supabase
  .from('tenders')
  .select('*')
  .order('created_at', { ascending: false });
```

### Создание тендера
```typescript
const { data, error } = await supabase
  .from('tenders')
  .insert({
    tender_number: tenderNumber,
    title: title,
    client_name: clientName,
    description: description,
    deadline_date: deadlineDate,
    status: 'Активный',
    version: 1,
    usd_rate: usdRate || 1.0,
    eur_rate: eurRate || 1.0,
    cny_rate: cnyRate || 1.0,
    user_id: userId,
  })
  .select()
  .single();
```

### Обновление тендера
```typescript
const { error } = await supabase
  .from('tenders')
  .update({
    tender_number: newNumber,
    title: newTitle,
    client_name: newClient,
    description: newDescription,
    deadline_date: newDeadline,
    status: newStatus,
    usd_rate: newUsdRate,
    eur_rate: newEurRate,
    cny_rate: newCnyRate,
    updated_at: new Date().toISOString(),
  })
  .eq('id', tenderId);
```

### Удаление тендера
```typescript
// Каскадное удаление обрабатывает связанные данные
const { error } = await supabase
  .from('tenders')
  .delete()
  .eq('id', tenderId);
```

### Копирование тендера
```typescript
// 1. Копировать тендер
const { data: newTender } = await supabase
  .from('tenders')
  .insert({
    ...originalTender,
    id: undefined,
    version: originalTender.version + 1,
    created_at: new Date().toISOString(),
  })
  .select()
  .single();

// 2. Копировать позиции
const { data: positions } = await supabase
  .from('client_positions')
  .select('*')
  .eq('tender_id', originalTenderId);

await supabase
  .from('client_positions')
  .insert(
    positions.map(p => ({
      ...p,
      id: undefined,
      tender_id: newTender.id,
    }))
  );
```

## Рабочие процессы пользователя

### Создание тендера
1. Нажмите "Создать новый тендер"
2. Открывается модальное окно с пустой формой
3. Заполните обязательные поля:
   - Номер тендера
   - Название тендера
   - Имя заказчика
   - Дата крайнего срока
4. Опционально заполните:
   - Описание
   - Курсы валют
5. Нажмите "Создать"
6. Тендер добавлен в таблицу

### Редактирование тендера
1. Найдите тендер в таблице
2. Нажмите Действия → Редактировать
3. Открывается модальное окно с текущими значениями
4. Измените поля по необходимости
5. Нажмите "Сохранить"
6. Тендер обновлён в таблице

### Загрузка BOQ из Excel
1. Найдите тендер в таблице
2. Нажмите кнопку "Загрузить СМР"
3. **Шаг 1 - Загрузка**:
   - Перетащите файл или нажмите для выбора
   - Файл Excel загружен и распарсен
   - Нажмите "Далее"
4. **Шаг 2 - Маппинг**:
   - Сопоставьте колонки Excel с полями
   - Обязательно: Номер позиции, Название, Ед. изм., Объём
   - Необязательно: Номер раздела, Примечания
   - Автоопределение помогает с общими названиями
   - Нажмите "Далее"
5. **Шаг 3 - Предпросмотр**:
   - Просмотрите распарсенные данные (первые 10 строк)
   - Проверьте на ошибки (выделены красным)
   - Посмотрите общее количество строк
   - Нажмите "Загрузить" для подтверждения
   - Или "Назад" для изменения маппинга
6. Позиции импортированы в базу данных
7. Отображается сообщение об успехе

### Копирование тендера
1. Найдите тендер в таблице
2. Нажмите Действия → Копировать
3. Появляется диалог подтверждения
4. Нажмите "Копировать"
5. Создан новый тендер с версией + 1
6. Все позиции скопированы
7. Таблица обновляется с новым тендером

### Удаление тендера
1. Найдите тендер в таблице
2. Нажмите Действия → Удалить
3. Диалог подтверждения с предупреждением
4. Нажмите "Удалить" для подтверждения
5. Тендер и связанные данные удалены
6. Таблица обновляется

### Сопоставление версий
1. Имеются две версии одного тендера
2. Нажмите кнопку "Сопоставить версии"
3. Открывается VersionMatchModal
4. Просмотр позиций бок о бок
5. Сначала выполняется автоматическое сопоставление
6. Просмотрите автоматические сопоставления
7. Вручную сопоставьте несопоставленные позиции
8. Сохраните сопоставления
9. Используйте сопоставления для миграции данных

## Процесс загрузки Excel

### Парсинг файла
```typescript
import * as XLSX from 'xlsx';

function parseExcelFile(file: File): Promise<any[]> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();

    reader.onload = (e) => {
      const data = e.target?.result;
      const workbook = XLSX.read(data, { type: 'binary' });
      const firstSheet = workbook.Sheets[workbook.SheetNames[0]];
      const jsonData = XLSX.utils.sheet_to_json(firstSheet, { header: 1 });

      resolve(jsonData);
    };

    reader.onerror = reject;
    reader.readAsBinaryString(file);
  });
}
```

### Маппинг колонок
```typescript
interface ColumnMapping {
  positionNumber: number;  // Индекс колонки Excel
  workName: number;
  unitCode: number;
  volume: number;
  sectionNumber?: number;
  note?: number;
}

// Пользователь выбирает маппинг через выпадающие списки
const mapping: ColumnMapping = {
  positionNumber: 0,  // Колонка A
  workName: 1,        // Колонка B
  unitCode: 2,        // Колонка C
  volume: 3,          // Колонка D
};
```

### Трансформация данных
```typescript
function transformRows(
  rows: any[][],
  mapping: ColumnMapping,
  tenderId: string
): ClientPosition[] {
  return rows.slice(1).map((row, index) => ({  // Пропустить заголовок
    tender_id: tenderId,
    position_number: parseFloat(row[mapping.positionNumber]),
    work_name: row[mapping.workName],
    unit_code: row[mapping.unitCode],
    volume: parseFloat(row[mapping.volume]),
    section_number: mapping.sectionNumber
      ? row[mapping.sectionNumber]
      : null,
    hierarchy_level: calculateLevel(row[mapping.positionNumber]),
  }));
}
```

### Валидация
```typescript
function validateRow(row: ClientPosition, index: number): ValidationError[] {
  const errors: ValidationError[] = [];

  // Обязательные поля
  if (!row.position_number) {
    errors.push({ row: index, field: 'position_number', message: 'Required' });
  }
  if (!row.work_name) {
    errors.push({ row: index, field: 'work_name', message: 'Required' });
  }
  if (!row.unit_code) {
    errors.push({ row: index, field: 'unit_code', message: 'Required' });
  }
  if (!row.volume) {
    errors.push({ row: index, field: 'volume', message: 'Required' });
  }

  // Формат числа
  if (isNaN(row.position_number)) {
    errors.push({ row: index, field: 'position_number', message: 'Must be number' });
  }

  return errors;
}
```

### Массовая вставка
```typescript
async function insertPositions(positions: ClientPosition[]) {
  const BATCH_SIZE = 100;

  for (let i = 0; i < positions.length; i += BATCH_SIZE) {
    const batch = positions.slice(i, i + BATCH_SIZE);

    const { error } = await supabase
      .from('client_positions')
      .insert(batch);

    if (error) throw error;
  }
}
```

## Контроль версий

### Назначение номера версии
```typescript
async function getNextVersion(title: string): Promise<number> {
  const { data } = await supabase
    .from('tenders')
    .select('version')
    .eq('title', title)
    .order('version', { ascending: false })
    .limit(1);

  return data && data.length > 0 ? data[0].version + 1 : 1;
}
```

### Сравнение версий
```typescript
interface VersionComparison {
  tender1: Tender;
  tender2: Tender;
  positions1: ClientPosition[];
  positions2: ClientPosition[];
  matches: PositionMatch[];
  unmatched1: string[];  // ID позиций
  unmatched2: string[];
}

async function compareVersions(
  tenderId1: string,
  tenderId2: string
): Promise<VersionComparison> {
  // Получить оба тендера и позиции
  // Запустить алгоритм сопоставления
  // Вернуть результаты сравнения
}
```

## Управление состоянием

### Состояние компонента
```typescript
interface TendersState {
  tendersData: TenderRecord[];
  loading: boolean;
  searchText: string;
  selectedRowKeys: React.Key[];
  uploadBOQVisible: boolean;
  selectedTenderForUpload: TenderRecord | null;
}

interface TenderRecord extends Tender {
  key: string;  // Для ключа строки таблицы
  daysRemaining?: number;  // Вычисляемое
}
```

### Пользовательские хуки
- `useTendersData`: Получает и управляет списком тендеров
- `useTenderActions`: Обрабатывает CRUD операции
- `useUploadBOQ`: Управляет мастером загрузки BOQ

## Конфигурация колонок таблицы

```typescript
const columns = [
  {
    title: '№ Тендера',
    dataIndex: 'tender_number',
    key: 'tender_number',
    width: 120,
    sorter: (a, b) => a.tender_number.localeCompare(b.tender_number),
  },
  {
    title: 'Наименование',
    dataIndex: 'title',
    key: 'title',
    width: 200,
  },
  {
    title: 'Заказчик',
    dataIndex: 'client_name',
    key: 'client_name',
    width: 150,
  },
  {
    title: 'Срок сдачи',
    dataIndex: 'deadline_date',
    key: 'deadline',
    width: 120,
    render: (date, record) => (
      <div>
        <div>{dayjs(date).format('DD.MM.YYYY')}</div>
        <Tag color={record.daysRemaining < 7 ? 'red' : 'green'}>
          {record.daysRemaining} дней
        </Tag>
      </div>
    ),
  },
  {
    title: 'Версия',
    dataIndex: 'version',
    key: 'version',
    width: 80,
    render: (version) => <Tag color="blue">v{version}</Tag>,
  },
  {
    title: 'Действия',
    key: 'actions',
    width: 100,
    render: (_, record) => <TendersActionMenu record={record} />,
  },
];
```

## Оптимизации производительности

### Пакетный импорт
```typescript
// Импорт пакетами для избежания таймаута
const BATCH_SIZE = 100;

for (let i = 0; i < positions.length; i += BATCH_SIZE) {
  const batch = positions.slice(i, i + BATCH_SIZE);
  await insertBatch(batch);

  // Обновить прогресс
  setProgress((i + batch.length) / positions.length * 100);
}
```

### Отложенный поиск
```typescript
const debouncedSearch = useMemo(
  () => debounce((value: string) => {
    setSearchText(value);
  }, 300),
  []
);
```

### Мемоизированные отфильтрованные данные
```typescript
const filteredData = useMemo(() =>
  tendersData.filter(item =>
    searchText === '' ||
    item.tender_number.toLowerCase().includes(searchText.toLowerCase()) ||
    item.title.toLowerCase().includes(searchText.toLowerCase()) ||
    item.description?.toLowerCase().includes(searchText.toLowerCase())
  ),
  [tendersData, searchText]
);
```

## Обработка ошибок

### Ошибки загрузки
```typescript
try {
  const positions = parseExcelFile(file);
  const errors = validatePositions(positions);

  if (errors.length > 0) {
    Modal.error({
      title: 'Ошибки валидации',
      content: (
        <ul>
          {errors.map(e => (
            <li key={e.row}>
              Строка {e.row}: {e.field} - {e.message}
            </li>
          ))}
        </ul>
      ),
    });
    return;
  }

  await insertPositions(positions);
  message.success('Позиции успешно загружены');
} catch (error) {
  message.error('Ошибка загрузки файла');
}
```

### Подтверждение удаления
```typescript
Modal.confirm({
  title: 'Удалить тендер?',
  content: 'Это действие удалит тендер и все связанные данные. Отменить будет невозможно.',
  okText: 'Удалить',
  okButtonProps: { danger: true },
  cancelText: 'Отмена',
  onOk: async () => {
    await deleteTender(tenderId);
  },
});
```

## Связанные страницы

- **[Позиции заказчика](CLIENT_POSITIONS.md)**: Управляет импортированными позициями
- **[Дашборд](DASHBOARD_DESIGN_SYSTEM.md)**: Показывает статистику тендеров
- **[Коммерция](COMMERCE_PAGE.md)**: Использует данные тендеров для расчётов

## Скриншоты

_Здесь будут размещены скриншоты, показывающие:_
1. Таблица тендеров со всеми колонками
2. Модальное окно создания тендера
3. Мастер загрузки BOQ - Шаг 1 (Загрузка)
4. Мастер загрузки BOQ - Шаг 2 (Маппинг)
5. Мастер загрузки BOQ - Шаг 3 (Предпросмотр)
6. Выпадающее меню действий
7. Модальное окно сопоставления версий

## Будущие улучшения

- [ ] Шаблоны тендеров
- [ ] Массовые операции с тендерами
- [ ] Расширенные фильтры поиска
- [ ] Представление сравнения тендеров
- [ ] Уведомления о крайних сроках
- [ ] Автоматизация рабочего процесса статусов
- [ ] Поддержка вложений документов
- [ ] Клонирование тендеров с модификациями
- [ ] Экспорт тендеров в различные форматы
- [ ] Процесс согласования тендеров
