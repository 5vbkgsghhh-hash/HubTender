import React, { useEffect, useRef, useState } from 'react';
import { Button, DatePicker, Input, Select, Space, Tag, Typography, message } from 'antd';
import { DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons';
import dayjs from 'dayjs';
import { patchTenderRegistryFields } from '../../../lib/api/tenderRegistry';
import type { ChronologyItem, TenderPackageItem } from '../../../lib/types';
import { DATE_INPUT_FORMATS, formatDate, getPackageLinkHref } from '../utils/tenderMonitor';
import type { TenderMonitorPalette } from '../utils/tenderMonitorTheme';
import { inputEnterToSave, textareaEnterToSave } from '../../../utils/keyboardSave';

const { Text } = Typography;
const { TextArea } = Input;

type ChronologyType = 'default' | 'call_follow_up';

const CHRONOLOGY_TYPE_OPTIONS = [
  { value: 'default', label: 'Событие' },
  { value: 'call_follow_up', label: 'Звонок' },
];

// Ярлык редактирования строки — тип/цвет как на вкладке «Информация».
function rowSectionStyle(palette: TenderMonitorPalette, danger: boolean): React.CSSProperties {
  return {
    padding: '10px 12px',
    background: palette.sectionBg,
    borderRadius: 10,
    border: `1px solid ${danger ? palette.dangerBorder : palette.borderSoft}`,
  };
}

function RowActions({
  palette,
  onEdit,
  onDelete,
}: {
  palette: TenderMonitorPalette;
  onEdit: () => void;
  onDelete: () => void;
}) {
  return (
    <Space size={2} style={{ flexShrink: 0 }}>
      <Button type="text" size="small" icon={<EditOutlined />} onClick={onEdit} style={{ color: palette.warning }} />
      <Button type="text" size="small" danger icon={<DeleteOutlined />} onClick={onDelete} />
    </Space>
  );
}

function EditActions({ onSave, onCancel }: { onSave: () => void; onCancel: () => void }) {
  return (
    <Space size={6} style={{ marginTop: 8 }}>
      <Button type="primary" size="small" onClick={onSave}>
        Сохранить
      </Button>
      <Button size="small" onClick={onCancel}>
        Отмена
      </Button>
    </Space>
  );
}

// ----- Хронология ------------------------------------------------------------

interface ChronologyRowProps {
  item: ChronologyItem;
  palette: TenderMonitorPalette;
  onSave: (next: ChronologyItem) => void;
  onDelete: () => void;
  onDiscardNew: () => void;
}

function ChronologyRow({ item, palette, onSave, onDelete, onDiscardNew }: ChronologyRowProps) {
  const [editing, setEditing] = useState(item.text === '');
  const [type, setType] = useState<ChronologyType>((item.type as ChronologyType) || 'default');
  const [date, setDate] = useState<dayjs.Dayjs | null>(item.date ? dayjs(item.date) : null);
  const [text, setText] = useState(item.text);

  const reset = () => {
    setType((item.type as ChronologyType) || 'default');
    setDate(item.date ? dayjs(item.date) : null);
    setText(item.text);
  };

  const handleSave = () => {
    onSave({ type, date: date ? date.toISOString() : null, text: text.trim() });
    setEditing(false);
  };

  const handleCancel = () => {
    if (item.text === '') {
      onDiscardNew();
      return;
    }
    reset();
    setEditing(false);
  };

  if (editing) {
    return (
      <div style={rowSectionStyle(palette, type === 'call_follow_up')}>
        <div style={{ display: 'grid', gridTemplateColumns: '115px 128px minmax(0, 1fr)', gap: 8, alignItems: 'start' }}>
          <Select
            size="small"
            value={type}
            onChange={(value) => setType(value as ChronologyType)}
            options={CHRONOLOGY_TYPE_OPTIONS}
          />
          <DatePicker
            size="small"
            value={date}
            onChange={setDate}
            format={DATE_INPUT_FORMATS}
            style={{ width: '100%' }}
          />
          <TextArea
            value={text}
            onChange={(event) => setText(event.target.value)}
            onKeyDown={textareaEnterToSave(handleSave, setText)}
            autoSize={{ minRows: 1, maxRows: 6 }}
            placeholder="Описание события"
          />
        </div>
        <EditActions onSave={handleSave} onCancel={handleCancel} />
      </div>
    );
  }

  return (
    <div style={rowSectionStyle(palette, item.type === 'call_follow_up')}>
      <div style={{ display: 'flex', justifyContent: 'space-between', gap: 8, alignItems: 'flex-start' }}>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6, flexWrap: 'wrap' }}>
            <span style={{ color: palette.warning, fontSize: 11, fontWeight: 700 }}>{formatDate(item.date)}</span>
            {item.type === 'call_follow_up' ? (
              <Tag color="error" style={{ marginInlineEnd: 0 }}>
                Звонок
              </Tag>
            ) : null}
          </div>
          <div
            style={{
              color: palette.textSecondary,
              fontSize: 12,
              lineHeight: 1.35,
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-word',
            }}
          >
            {item.text}
          </div>
        </div>
        <RowActions palette={palette} onEdit={() => setEditing(true)} onDelete={onDelete} />
      </div>
    </div>
  );
}

interface EditableChronologySectionProps {
  tenderId: string;
  items: ChronologyItem[];
  palette: TenderMonitorPalette;
  onUpdated: () => Promise<void> | void;
}

export function EditableChronologySection({ tenderId, items, palette, onUpdated }: EditableChronologySectionProps) {
  const keyRef = useRef(0);
  const [rows, setRows] = useState<Array<ChronologyItem & { _k: number }>>([]);

  // Строки сбрасываются только при смене содержимого: фоновое обновление перечня
  // (другие пользователи, Telegram-бот) не стирает незаконченную правку.
  const itemsKey = JSON.stringify(items);
  useEffect(() => {
    setRows(items.map((item) => ({ ...item, _k: keyRef.current++ })));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [itemsKey]);

  const persist = async (next: Array<ChronologyItem & { _k: number }>) => {
    const payload = next
      .map((item) => ({
        date: item.date ? dayjs(item.date).toISOString() : null,
        text: item.text.trim(),
        type: item.type || 'default',
      }))
      .filter((item) => item.text);

    try {
      await patchTenderRegistryFields(tenderId, { chronology_items: payload });
    } catch (err) {
      message.error((err as Error).message);
      return;
    }

    await onUpdated();
    message.success('Хронология обновлена');
  };

  const addItem = () => {
    setRows((prev) => [...prev, { date: dayjs().toISOString(), text: '', type: 'default', _k: keyRef.current++ }]);
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      {rows.length > 0 ? (
        rows.map((row) => (
          <ChronologyRow
            key={row._k}
            item={row}
            palette={palette}
            onSave={(next) => void persist(rows.map((r) => (r._k === row._k ? { ...next, _k: row._k } : r)))}
            onDelete={() => void persist(rows.filter((r) => r._k !== row._k))}
            onDiscardNew={() => setRows((prev) => prev.filter((r) => r._k !== row._k))}
          />
        ))
      ) : (
        <Text style={{ color: palette.muted, fontSize: 12 }}>Хронология пока не заполнена</Text>
      )}

      <div>
        <Button size="small" icon={<PlusOutlined />} onClick={addItem}>
          Добавить строку
        </Button>
      </div>
    </div>
  );
}

export function ReadOnlyChronologySection({
  items,
  palette,
}: {
  items: ChronologyItem[];
  palette: TenderMonitorPalette;
}) {
  if (items.length === 0) {
    return <Text style={{ color: palette.muted, fontSize: 12 }}>Хронология пока не заполнена</Text>;
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      {items.map((item, index) => (
        <div key={`${item.date || 'empty'}-${index}`} style={rowSectionStyle(palette, item.type === 'call_follow_up')}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6, flexWrap: 'wrap' }}>
            <span style={{ color: palette.warning, fontSize: 11, fontWeight: 700 }}>{formatDate(item.date)}</span>
            {item.type === 'call_follow_up' ? (
              <Tag color="error" style={{ marginInlineEnd: 0 }}>
                Звонок
              </Tag>
            ) : null}
          </div>
          <div style={{ color: palette.textSecondary, fontSize: 12, lineHeight: 1.35 }}>{item.text}</div>
        </div>
      ))}
    </div>
  );
}

// ----- Тендерный пакет -------------------------------------------------------

interface PackageRowProps {
  item: TenderPackageItem;
  palette: TenderMonitorPalette;
  onSave: (next: TenderPackageItem) => void;
  onDelete: () => void;
  onDiscardNew: () => void;
}

function PackageRow({ item, palette, onSave, onDelete, onDiscardNew }: PackageRowProps) {
  const [editing, setEditing] = useState(item.text === '');
  const [date, setDate] = useState<dayjs.Dayjs | null>(item.date ? dayjs(item.date) : null);
  const [text, setText] = useState(item.text);
  const [link, setLink] = useState(item.link || '');

  const reset = () => {
    setDate(item.date ? dayjs(item.date) : null);
    setText(item.text);
    setLink(item.link || '');
  };

  const handleSave = () => {
    onSave({ date: date ? date.toISOString() : null, text: text.trim(), link: link.trim() || null });
    setEditing(false);
  };

  const handleCancel = () => {
    if (item.text === '') {
      onDiscardNew();
      return;
    }
    reset();
    setEditing(false);
  };

  if (editing) {
    return (
      <div style={rowSectionStyle(palette, false)}>
        <div style={{ display: 'grid', gridTemplateColumns: '128px minmax(0, 1fr)', gap: 8, alignItems: 'start' }}>
          <DatePicker
            size="small"
            value={date}
            onChange={setDate}
            format={DATE_INPUT_FORMATS}
            style={{ width: '100%' }}
          />
          <TextArea
            value={text}
            onChange={(event) => setText(event.target.value)}
            onKeyDown={textareaEnterToSave(handleSave, setText)}
            autoSize={{ minRows: 1, maxRows: 6 }}
            placeholder="Наименование"
          />
        </div>
        <Input
          size="small"
          value={link}
          onChange={(event) => setLink(event.target.value)}
          onKeyDown={inputEnterToSave(handleSave)}
          placeholder="Ссылка"
          style={{ marginTop: 8 }}
        />
        <EditActions onSave={handleSave} onCancel={handleCancel} />
      </div>
    );
  }

  const href = getPackageLinkHref(item.link);

  return (
    <div style={rowSectionStyle(palette, false)}>
      <div style={{ display: 'flex', justifyContent: 'space-between', gap: 8, alignItems: 'flex-start' }}>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', gap: 8, alignItems: 'baseline' }}>
            <div
              style={{
                color: palette.text,
                fontSize: 12,
                fontWeight: 600,
                whiteSpace: 'pre-wrap',
                wordBreak: 'break-word',
              }}
            >
              {item.text}
            </div>
            {item.date ? (
              <span style={{ color: palette.muted, fontSize: 11, flexShrink: 0 }}>{formatDate(item.date)}</span>
            ) : null}
          </div>
          {href ? (
            <a
              href={href}
              target="_blank"
              rel="noreferrer"
              style={{ color: palette.info, fontSize: 11, wordBreak: 'break-word' }}
            >
              Открыть ссылку
            </a>
          ) : null}
        </div>
        <RowActions palette={palette} onEdit={() => setEditing(true)} onDelete={onDelete} />
      </div>
    </div>
  );
}

interface EditablePackageSectionProps {
  tenderId: string;
  items: TenderPackageItem[];
  palette: TenderMonitorPalette;
  onUpdated: () => Promise<void> | void;
}

export function EditablePackageSection({ tenderId, items, palette, onUpdated }: EditablePackageSectionProps) {
  const keyRef = useRef(0);
  const [rows, setRows] = useState<Array<TenderPackageItem & { _k: number }>>([]);

  // Строки сбрасываются только при смене содержимого: фоновое обновление перечня
  // (другие пользователи, Telegram-бот) не стирает незаконченную правку.
  const itemsKey = JSON.stringify(items);
  useEffect(() => {
    setRows(items.map((item) => ({ ...item, _k: keyRef.current++ })));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [itemsKey]);

  const persist = async (next: Array<TenderPackageItem & { _k: number }>) => {
    const payload = next
      .map((item) => ({
        date: item.date ? dayjs(item.date).toISOString() : null,
        text: item.text.trim(),
        link: item.link?.trim() || null,
      }))
      .filter((item) => item.text);

    try {
      await patchTenderRegistryFields(tenderId, { tender_package_items: payload });
    } catch (err) {
      message.error((err as Error).message);
      return;
    }

    await onUpdated();
    message.success('Тендерный пакет обновлен');
  };

  const addItem = () => {
    setRows((prev) => [...prev, { date: dayjs().toISOString(), text: '', link: '', _k: keyRef.current++ }]);
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      {rows.length > 0 ? (
        rows.map((row) => (
          <PackageRow
            key={row._k}
            item={row}
            palette={palette}
            onSave={(next) => void persist(rows.map((r) => (r._k === row._k ? { ...next, _k: row._k } : r)))}
            onDelete={() => void persist(rows.filter((r) => r._k !== row._k))}
            onDiscardNew={() => setRows((prev) => prev.filter((r) => r._k !== row._k))}
          />
        ))
      ) : (
        <Text style={{ color: palette.muted, fontSize: 12 }}>Тендерный пакет не заполнен</Text>
      )}

      <div>
        <Button size="small" icon={<PlusOutlined />} onClick={addItem}>
          Добавить строку
        </Button>
      </div>
    </div>
  );
}

export function ReadOnlyPackageSection({
  items,
  palette,
}: {
  items: TenderPackageItem[];
  palette: TenderMonitorPalette;
}) {
  const visibleItems = items.filter((item) => item.text?.trim());

  if (visibleItems.length === 0) {
    return <Text style={{ color: palette.muted, fontSize: 12 }}>Тендерный пакет не заполнен</Text>;
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      {visibleItems.map((item, index) => {
        const href = getPackageLinkHref(item.link);

        return (
          <div key={`${item.date || 'empty'}-${index}`} style={rowSectionStyle(palette, false)}>
            <div style={{ display: 'flex', justifyContent: 'space-between', gap: 8, alignItems: 'baseline' }}>
              <div style={{ color: palette.text, fontSize: 12, fontWeight: 600, wordBreak: 'break-word' }}>{item.text}</div>
              {item.date ? (
                <span style={{ color: palette.muted, fontSize: 11, flexShrink: 0 }}>{formatDate(item.date)}</span>
              ) : null}
            </div>
            {href ? (
              <a
                href={href}
                target="_blank"
                rel="noreferrer"
                style={{ color: palette.info, fontSize: 11, wordBreak: 'break-word' }}
              >
                Открыть ссылку
              </a>
            ) : null}
          </div>
        );
      })}
    </div>
  );
}
