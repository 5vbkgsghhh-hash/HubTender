// Go BFF API: base URL and realtime flag.

function resolveApiBaseUrl(): string {
  const envUrl = import.meta.env.VITE_API_URL as string | undefined;
  if (envUrl) return envUrl;
  if (import.meta.env.PROD) {
    throw new Error('VITE_API_URL is required in production builds.');
  }
  return 'http://localhost:3005';
}

export const API_BASE_URL = resolveApiBaseUrl();

// Realtime: true → use native WS hub (Go BFF). Falsy → PostgREST channels direct.
export function isRealtimeEnabled(): boolean {
  return import.meta.env.VITE_API_REALTIME_ENABLED === 'true';
}
