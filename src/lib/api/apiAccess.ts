// Управление машинным доступом к API («Настройки → Доступ к API»).
//
// Секрет ключа возвращается ТОЛЬКО в ответе на выпуск и больше нигде не
// доступен: в БД лежит лишь его SHA-256 хеш.

import { apiFetch } from './client';

export type ApiKeyScope =
  | 'archive:read'
  | 'archive:write'
  | 'tenders:read'
  | 'tenders:write'
  | 'verification:read'
  | 'verification:write'
  | 'registry:read'
  | 'registry:write';
export type ApiKeyStatus = 'active' | 'revoked' | 'expired';

export interface ApiKey {
  id: string;
  name: string;
  key_prefix: string;
  scopes: ApiKeyScope[];
  allowed_tender_ids: string[];
  expires_at: string | null;
  revoked_at: string | null;
  last_used_at: string | null;
  created_at: string;
  created_by: string;
  created_by_name: string | null;
  revoked_by_name: string | null;
  status: ApiKeyStatus;
  calls_last_24h: number;
}

export interface IssuedApiKey {
  key: ApiKey;
  /** Показывается пользователю один раз, восстановить нельзя. */
  secret: string;
}

export interface CreateApiKeyInput {
  name: string;
  scopes: ApiKeyScope[];
  allowed_tender_ids?: string[];
  expires_at?: string | null;
}

export interface ApiAccessSettings {
  archive_search_enabled: boolean;
  archive_read_enabled: boolean;
  archive_suggest_enabled: boolean;
  archive_compose_enabled: boolean;
  max_search_limit: number;
  max_candidate_limit: number;
  max_suggest_queries: number;
  rate_limit_per_minute: number;
  call_log_retention_days: number;
  updated_at: string;
  updated_by: string | null;
  updated_by_name: string | null;
}

export interface ApiCallLogEntry {
  id: string;
  api_key_id: string | null;
  api_key_name: string | null;
  user_id: string | null;
  user_name: string | null;
  method: string;
  path: string;
  status: number;
  duration_ms: number;
  error_code: string | null;
  items_affected: number | null;
  dry_run: boolean | null;
  called_at: string;
}

const BASE = '/api/v1/admin/api-access';

export async function listApiKeys(): Promise<ApiKey[]> {
  const res = await apiFetch<{ data: ApiKey[] }>(`${BASE}/keys`);
  return res.data ?? [];
}

export async function createApiKey(input: CreateApiKeyInput): Promise<IssuedApiKey> {
  const res = await apiFetch<{ data: IssuedApiKey }>(`${BASE}/keys`, {
    method: 'POST',
    body: JSON.stringify({
      name: input.name,
      scopes: input.scopes,
      allowed_tender_ids: input.allowed_tender_ids ?? [],
      expires_at: input.expires_at ?? null,
    }),
  });
  return res.data;
}

export async function revokeApiKey(id: string): Promise<ApiKey> {
  const res = await apiFetch<{ data: ApiKey }>(
    `${BASE}/keys/${encodeURIComponent(id)}/revoke`,
    { method: 'POST' },
  );
  return res.data;
}

export async function deleteApiKey(id: string): Promise<void> {
  await apiFetch<void>(`${BASE}/keys/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

export async function getApiAccessSettings(): Promise<ApiAccessSettings> {
  const res = await apiFetch<{ data: ApiAccessSettings }>(`${BASE}/settings`);
  return res.data;
}

export async function updateApiAccessSettings(
  settings: Omit<ApiAccessSettings, 'updated_at' | 'updated_by' | 'updated_by_name'>,
): Promise<ApiAccessSettings> {
  const res = await apiFetch<{ data: ApiAccessSettings }>(`${BASE}/settings`, {
    method: 'PUT',
    body: JSON.stringify(settings),
  });
  return res.data;
}

export interface CallLogFilter {
  apiKeyId?: string;
  onlyErrors?: boolean;
  limit?: number;
}

export async function listApiCallLog(filter: CallLogFilter = {}): Promise<ApiCallLogEntry[]> {
  const params = new URLSearchParams();
  if (filter.apiKeyId) params.set('api_key_id', filter.apiKeyId);
  if (filter.onlyErrors) params.set('only_errors', 'true');
  params.set('limit', String(filter.limit ?? 100));

  const res = await apiFetch<{ data: ApiCallLogEntry[] }>(`${BASE}/calls?${params.toString()}`);
  return res.data ?? [];
}
