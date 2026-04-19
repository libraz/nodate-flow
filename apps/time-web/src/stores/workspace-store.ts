/**
 * Workspace slice (Zustand). Holds the active workspace ID and name.
 * Persisted to localStorage under `nf_workspace_id` / `nf_workspace_name`
 * so the selection survives page reloads.
 */

import { useStore } from 'zustand';
import { createStore } from 'zustand/vanilla';

export interface WorkspaceState {
  workspaceId: string | null;
  workspaceName: string | null;
  setWorkspace: (id: string, name: string) => void;
  clearWorkspace: () => void;
}

/**
 * Vanilla store, exported so non-React modules (e.g. mutation callbacks)
 * can read the workspace ID without going through React.
 */
export const workspaceStore = createStore<WorkspaceState>((set) => ({
  workspaceId: localStorage.getItem('nf_workspace_id'),
  workspaceName: localStorage.getItem('nf_workspace_name'),
  setWorkspace: (id, name) => {
    localStorage.setItem('nf_workspace_id', id);
    localStorage.setItem('nf_workspace_name', name);
    set({ workspaceId: id, workspaceName: name });
  },
  clearWorkspace: () => {
    localStorage.removeItem('nf_workspace_id');
    localStorage.removeItem('nf_workspace_name');
    set({ workspaceId: null, workspaceName: null });
  },
}));

/** React hook with selector. Always pass a selector to avoid over-rendering. */
export function useWorkspace<T>(selector: (state: WorkspaceState) => T): T {
  return useStore(workspaceStore, selector);
}

/** Convenience selectors. */
export const selectWorkspaceId = (s: WorkspaceState): string | null => s.workspaceId;
export const selectWorkspaceName = (s: WorkspaceState): string | null => s.workspaceName;
