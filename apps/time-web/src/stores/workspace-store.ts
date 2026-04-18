import { create } from 'zustand';

interface WorkspaceState {
  workspaceId: string | null;
  workspaceName: string | null;
  setWorkspace: (id: string, name: string) => void;
  clearWorkspace: () => void;
}

export const useWorkspaceStore = create<WorkspaceState>((set) => ({
  workspaceId: localStorage.getItem('nt_workspace_id'),
  workspaceName: localStorage.getItem('nt_workspace_name'),
  setWorkspace: (id, name) => {
    localStorage.setItem('nt_workspace_id', id);
    localStorage.setItem('nt_workspace_name', name);
    set({ workspaceId: id, workspaceName: name });
  },
  clearWorkspace: () => {
    localStorage.removeItem('nt_workspace_id');
    localStorage.removeItem('nt_workspace_name');
    set({ workspaceId: null, workspaceName: null });
  },
}));
