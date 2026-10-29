/**
 * The command-palette shortcut as the reader's keyboard labels it: `⌘K` on
 * Apple platforms, `Ctrl+K` elsewhere. The handler accepts both modifiers,
 * so this only decides what the hint shows.
 */
export function paletteShortcutLabel(): string {
  if (typeof navigator === 'undefined') return 'Ctrl+K';
  const nav = navigator as Navigator & { userAgentData?: { platform?: string } };
  const platform = nav.userAgentData?.platform ?? nav.platform ?? '';
  return /mac|iphone|ipad|ipod/i.test(platform) ? '⌘K' : 'Ctrl+K';
}
