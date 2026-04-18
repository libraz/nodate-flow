import { useAuthStore } from '../stores/auth-store';

const BASE_URL = import.meta.env.VITE_API_URL ?? 'http://localhost:8081';

let isRefreshing = false;
let refreshPromise: Promise<boolean> | null = null;

async function tryRefreshToken(): Promise<boolean> {
  if (isRefreshing && refreshPromise) return refreshPromise;
  isRefreshing = true;
  refreshPromise = (async () => {
    try {
      const res = await fetch(`${BASE_URL}/auth/refresh`, {
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
    window.location.href = '/login';
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

interface AuthTokenResponse {
  accessToken: string;
  user: {
    id: string;
    email: string;
    displayName: string;
  };
}

export const authApi = {
  register: (data: { email: string; password: string; displayName: string }) =>
    api.post<AuthTokenResponse>('/auth/register', data),
  login: (data: { email: string; password: string }) =>
    api.post<AuthTokenResponse>('/auth/login', data),
  refresh: () => api.post<{ accessToken: string }>('/auth/refresh', {}),
  logout: () => api.post<void>('/auth/logout', {}),
  me: () => api.get<{ id: string; email: string; displayName: string }>('/me'),
};

interface Workspace {
  id: string;
  name: string;
  slug: string;
}

export const workspaceApi = {
  list: () => api.get<{ items: Workspace[] }>('/workspaces'),
  create: (data: { name: string; slug: string }) => api.post<Workspace>('/workspaces', data),
};
