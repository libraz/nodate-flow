import { create } from 'zustand';

interface User {
  id: string;
  email: string;
  displayName: string;
}

interface AuthState {
  accessToken: string | null;
  user: User | null;
  isAuthenticated: boolean;
  setAuth: (token: string, user: User) => void;
  clearAuth: () => void;
  setUser: (user: User) => void;
}

export const useAuthStore = create<AuthState>((set) => ({
  accessToken: localStorage.getItem('nt_token'),
  user: null,
  isAuthenticated: !!localStorage.getItem('nt_token'),
  setAuth: (token, user) => {
    localStorage.setItem('nt_token', token);
    set({ accessToken: token, user, isAuthenticated: true });
  },
  setUser: (user) => set({ user }),
  clearAuth: () => {
    localStorage.removeItem('nt_token');
    set({ accessToken: null, user: null, isAuthenticated: false });
  },
}));
