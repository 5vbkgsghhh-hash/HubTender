// Import sessions log helpers — Go BFF only.
// Cancel — одна атомарная транзакция на сервере (delete+restore+mark).

import { apiFetch } from './client';
import type { CreatedAdditionalPosition } from '../../utils/boq/additionalImport';

export interface ImportSession {
  id: string;
  user_id: string;
  tender_id: string;
  file_name: string | null;
  items_count: number;
  imported_at: string;
  cancelled_at: string | null;
  cancelled_by: string | null;
  positions_snapshot: Array<{
    id: string;
    manual_volume: number | null;
    manual_note: string | null;
  }> | null;
  /** ДОП, созданные строками «доп»; отмена импорта удаляет их, если они пусты. */
  created_positions?: CreatedAdditionalPosition[] | null;
}

export interface ImportLogUser {
  id: string;
  full_name: string;
  role_code: string;
  roles: { name: string; color: string | null } | null;
}

export interface ImportLogTender {
  id: string;
  title: string;
  tender_number: string;
  version: number;
}

export interface CancelImportSessionResult {
  boq_deleted: number;
  positions_restored: number;
  /** Удалённые ДОП импорта и оставленные (в них есть элементы не из этого импорта). */
  additional_deleted: number;
  additional_kept: number;
}

export async function fetchImportSessions(tenderId?: string | null): Promise<ImportSession[]> {
  const qs = tenderId ? `?tender_id=${encodeURIComponent(tenderId)}` : '';
  const res = await apiFetch<{ data: ImportSession[] }>(
    `/api/v1/import-sessions${qs}`,
    { cache: 'no-store' },
  );
  return res.data ?? [];
}

export async function fetchImportLogUsers(ids: string[]): Promise<ImportLogUser[]> {
  if (ids.length === 0) return [];
  const qs = encodeURIComponent(ids.join(','));
  const res = await apiFetch<{ data: ImportLogUser[] }>(
    `/api/v1/import-sessions/users?ids=${qs}`,
  );
  return res.data ?? [];
}

export async function fetchImportLogTenders(ids: string[]): Promise<ImportLogTender[]> {
  if (ids.length === 0) return [];
  const qs = encodeURIComponent(ids.join(','));
  const res = await apiFetch<{ data: ImportLogTender[] }>(
    `/api/v1/import-sessions/tenders?ids=${qs}`,
  );
  return res.data ?? [];
}

export async function fetchAllTendersForFilter(): Promise<ImportLogTender[]> {
  const res = await apiFetch<{ data: ImportLogTender[] }>(
    '/api/v1/import-sessions/all-tenders',
  );
  return res.data ?? [];
}

/**
 * Cancel an import session. Go BFF performs delete (items + empty ДОП of the
 * import) + restore + recompute + mark in a single transaction (cancelledBy
 * берётся из JWT — параметр оставлен для совместимости сигнатуры).
 */
export async function cancelImportSession(
  session: ImportSession,
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  _cancelledBy: string,
): Promise<CancelImportSessionResult> {
  const res = await apiFetch<{ data: CancelImportSessionResult }>(
    `/api/v1/import-sessions/${encodeURIComponent(session.id)}/cancel`,
    { method: 'POST' },
  );
  return res.data;
}
