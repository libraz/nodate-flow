/** `g s` opens the settings area root, which redirects to notifications. */

import { renderHook } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

const navigate = vi.hoisted(() => vi.fn());

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigate,
}));

import { useKeyboardShortcuts } from '../use-keyboard-shortcuts';

describe('useKeyboardShortcuts', () => {
  it('navigates g s to /settings', () => {
    renderHook(() => useKeyboardShortcuts({ onCreateTask: vi.fn(), onShowHelp: vi.fn() }));
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'g' }));
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 's' }));
    expect(navigate).toHaveBeenCalledWith({ to: '/settings' });
  });
});
