import { isApplePlatform } from '../../lib/submit-shortcut';

/**
 * The command-palette shortcut as the reader's keyboard labels it: `⌘K` on
 * Apple platforms, `Ctrl+K` elsewhere. The handler accepts both modifiers,
 * so this only decides what the hint shows.
 */
export function paletteShortcutLabel(): string {
  return isApplePlatform() ? '⌘K' : 'Ctrl+K';
}
