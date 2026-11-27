/**
 * Open state of the AI suggestions panel. The glass dock's AI button
 * toggles it and the panel renders from it, so the two never need a
 * direct reference to each other. `returnFocus` asks the dock button to
 * take focus back after the panel closes from inside (Escape / close).
 */

import { create } from 'zustand';

/** DOM id of the panel, for the dock button's `aria-controls`. */
export const AI_PANEL_ID = 'nf-ai-suggestions-panel';

interface AiPanelState {
  open: boolean;
  returnFocus: boolean;
  toggle: () => void;
  /** Close from inside the panel; focus returns to the dock button. */
  close: () => void;
  clearReturnFocus: () => void;
}

export const useAiPanelStore = create<AiPanelState>()((set) => ({
  open: false,
  returnFocus: false,
  toggle: () => set((state) => ({ open: !state.open, returnFocus: false })),
  close: () => set({ open: false, returnFocus: true }),
  clearReturnFocus: () => set({ returnFocus: false }),
}));
