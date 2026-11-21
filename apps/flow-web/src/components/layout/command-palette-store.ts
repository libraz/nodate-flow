/**
 * Open state of the single command palette mounted by `AppShell`. Every
 * trigger (top bar search, dock, Cmd/Ctrl+K, window event) goes through here.
 */

import { create } from 'zustand';

/** Event name other components can dispatch to open the palette. */
export const OPEN_COMMAND_PALETTE_EVENT = 'nf:open-command-palette';

interface CommandPaletteState {
  open: boolean;
  commandMode: boolean;
  openPalette: (opts?: { commandMode?: boolean }) => void;
  closePalette: () => void;
}

export const useCommandPaletteStore = create<CommandPaletteState>((set) => ({
  open: false,
  commandMode: false,
  openPalette: (opts) => set({ open: true, commandMode: opts?.commandMode ?? false }),
  closePalette: () => set({ open: false, commandMode: false }),
}));
