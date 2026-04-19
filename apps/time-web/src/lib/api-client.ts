import { useAuthStore } from '../stores/auth-store';

const BASE_URL = import.meta.env.VITE_API_URL ?? 'http://localhost:8081';
const AUTH_API_URL = import.meta.env.VITE_AUTH_API_BASE_URL ?? 'http://localhost:8082';
const ACCOUNTS_WEB_URL = import.meta.env.VITE_ACCOUNTS_WEB_URL ?? 'http://localhost:5175';

let isRefreshing = false;
let refreshPromise: Promise<boolean> | null = null;

async function tryRefreshToken(): Promise<boolean> {
  if (isRefreshing && refreshPromise) return refreshPromise;
  isRefreshing = true;
  refreshPromise = (async () => {
    try {
      const res = await fetch(`${AUTH_API_URL}/auth/refresh`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
      });
      if (!res.ok) return false;
      const data = (await res.json()) as { accessToken: string };
      const { setAuth, user } = useAuthStore.getState();
      if (user) {
        setAuth(data.accessToken, user);
      } else {
        localStorage.setItem('nt_token', data.accessToken);
        useAuthStore.setState({ accessToken: data.accessToken, isAuthenticated: true });
      }
      return true;
    } catch {
      return false;
    } finally {
      isRefreshing = false;
      refreshPromise = null;
    }
  })();
  return refreshPromise;
}

async function apiFetch<T>(path: string, options?: RequestInit): Promise<T> {
  const token = useAuthStore.getState().accessToken;
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    // biome-ignore lint/style/useNamingConvention: HTTP header name
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
    ...(options?.headers as Record<string, string>),
  };

  const res = await fetch(`${BASE_URL}${path}`, {
    ...options,
    headers,
    credentials: 'include',
  });

  if (res.status === 401 && !path.startsWith('/auth/')) {
    const refreshed = await tryRefreshToken();
    if (refreshed) {
      const newToken = useAuthStore.getState().accessToken;
      const retryRes = await fetch(`${BASE_URL}${path}`, {
        ...options,
        headers: {
          ...headers,
          // biome-ignore lint/style/useNamingConvention: HTTP header name
          ...(newToken ? { Authorization: `Bearer ${newToken}` } : {}),
        },
        credentials: 'include',
      });
      if (!retryRes.ok) throw new Error(`API error: ${retryRes.status}`);
      return retryRes.json();
    }
    useAuthStore.getState().clearAuth();
    window.location.href = `${ACCOUNTS_WEB_URL}/login?redirect=${encodeURIComponent(window.location.href)}`;
    throw new Error('Session expired');
  }

  if (!res.ok) {
    const body = await res.json().catch(() => null);
    const message = body?.detail ?? body?.message ?? `API error: ${res.status}`;
    throw new Error(message);
  }
  return res.json();
}

export const api = {
  get: <T>(path: string) => apiFetch<T>(path),
  post: <T>(path: string, body: unknown) =>
    apiFetch<T>(path, { method: 'POST', body: JSON.stringify(body) }),
  patch: <T>(path: string, body: unknown) =>
    apiFetch<T>(path, { method: 'PATCH', body: JSON.stringify(body) }),
  delete: <T>(path: string) => apiFetch<T>(path, { method: 'DELETE' }),
};

interface LoginResponse {
  step: string;
  accessToken: string;
  expiresAt: number;
  userId: string;
}

interface RegisterResponse {
  step: string;
  accessToken: string;
  expiresAt: number;
  userId: string;
}

interface MeResponse {
  id: string;
  email: string;
  displayName: string;
  themePreference?: string;
}

async function authFetch<T>(path: string, options?: RequestInit): Promise<T> {
  const token = useAuthStore.getState().accessToken;
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    // biome-ignore lint/style/useNamingConvention: HTTP header name
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
    ...(options?.headers as Record<string, string>),
  };

  const res = await fetch(`${AUTH_API_URL}${path}`, {
    ...options,
    headers,
    credentials: 'include',
  });

  if (!res.ok) {
    const body = await res.json().catch(() => null);
    const message = body?.detail ?? body?.message ?? `API error: ${res.status}`;
    throw new Error(message);
  }
  return res.json();
}

export const authApi = {
  register: (data: { email: string; password: string; displayName: string }) =>
    authFetch<RegisterResponse>('/auth/register', { method: 'POST', body: JSON.stringify(data) }),
  login: (data: { email: string; password: string }) =>
    authFetch<LoginResponse>('/auth/login', { method: 'POST', body: JSON.stringify(data) }),
  refresh: () =>
    authFetch<{ accessToken: string }>('/auth/refresh', {
      method: 'POST',
      body: JSON.stringify({}),
    }),
  logout: () => authFetch<void>('/auth/logout', { method: 'POST', body: JSON.stringify({}) }),
  me: () => authFetch<MeResponse>('/me'),
};

interface Workspace {
  id: string;
  name: string;
  slug: string;
}

export const workspaceApi = {
  list: () =>
    api.get<{ workspaces: Workspace[]; total: number }>('/workspaces').then((res) => ({
      items: res.workspaces,
      total: res.total,
    })),
  create: (data: { name: string; slug: string }) => api.post<Workspace>('/workspaces', data),
};
