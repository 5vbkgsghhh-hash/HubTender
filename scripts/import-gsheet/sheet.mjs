// Чтение выгрузки расчёта из Google Таблиц (xlsx): значения, формулы,
// гиперссылки (подпись в ячейке, адрес — в cell.l.Target) и поиск по подписям.

import fs from 'node:fs';
import * as XLSX from 'xlsx/xlsx.mjs';

XLSX.set_fs(fs);

export const readWorkbook = (file) => XLSX.readFile(file, { cellFormula: true, cellNF: true });

export const colName = (i) => XLSX.utils.encode_col(i);

/** Доступ к ячейкам листа по колонке-букве и номеру строки (с 1). */
export const sheetAccess = (wb, name) => {
  const ws = wb.Sheets[name];
  if (!ws) throw new Error(`в файле нет листа «${name}»`);
  const range = XLSX.utils.decode_range(ws['!ref']);
  const cell = (col, row) => ws[`${col}${row}`];
  return {
    lastRow: range.e.r + 1,
    lastCol: range.e.c,
    raw: (col, row) => cell(col, row)?.v,
    formula: (col, row) => cell(col, row)?.f ?? null,
    link: (col, row) => cell(col, row)?.l?.Target ?? null,
    format: (col, row) => cell(col, row)?.z ?? null,
    /** Отображаемый текст ячейки («60%»). */
    shown: (col, row) => cell(col, row)?.w ?? null,
    /** Текст ячейки без лишних пробелов или null. */
    text: (col, row) => {
      const c = cell(col, row);
      if (!c || c.v === undefined || c.v === null || c.t === 'e') return null;
      return String(c.v).replace(/\s+/g, ' ').trim() || null;
    },
    /** Число (только числовая ячейка, не ошибка и не текст) или null. */
    num: (col, row) => {
      const c = cell(col, row);
      return c && c.t === 'n' && Number.isFinite(c.v) ? c.v : null;
    },
  };
};

const norm = (s) => String(s ?? '').toLowerCase().replace(/ё/g, 'е').replace(/\s+/g, ' ').trim();

/**
 * Ячейка с подписью, подходящей под re, в строках from..to; значение — первая
 * непустая ячейка правее. Возвращает { row, col, valueCol } или null.
 */
export const findLabel = (s, re, { from = 1, to = 40 } = {}) => {
  for (let row = from; row <= Math.min(to, s.lastRow); row += 1) {
    for (let c = 0; c <= s.lastCol; c += 1) {
      const t = s.text(colName(c), row);
      if (!t || !re.test(norm(t))) continue;
      for (let v = c + 1; v <= s.lastCol; v += 1) {
        if (s.raw(colName(v), row) !== undefined) return { row, col: colName(c), valueCol: colName(v) };
      }
      return { row, col: colName(c), valueCol: null };
    }
  }
  return null;
};

export { norm as normLabel };
