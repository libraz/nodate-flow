/**
 * ShortcutsHelpDialog — lists all global keyboard shortcuts grouped by
 * section. Triggered by pressing "?" or from a menu action.
 */

import Dialog from '@nodate-flow/ui/primitives/dialog';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { SHORTCUT_BINDINGS } from '../lib/use-keyboard-shortcuts';

export interface ShortcutsHelpDialogProps {
  open: boolean;
  onClose: () => void;
}

export default function ShortcutsHelpDialog({
  open,
  onClose,
}: ShortcutsHelpDialogProps): ReactElement {
  const { t } = useTranslation('common');

  // Group bindings by section.
  const sections = new Map<string, typeof SHORTCUT_BINDINGS>();
  for (const b of SHORTCUT_BINDINGS) {
    const existing = sections.get(b.sectionKey) ?? [];
    existing.push(b);
    sections.set(b.sectionKey, existing);
  }

  return (
    <Dialog open={open} onClose={onClose} title={t('shortcuts.title')}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: '1.5rem' }}>
        {[...sections.entries()].map(([sectionKey, bindings]) => (
          <div key={sectionKey}>
            <h3
              style={{
                fontSize: 'var(--nf-text-xs)',
                fontWeight: 600,
                textTransform: 'uppercase',
                letterSpacing: '0.05em',
                opacity: 0.6,
                marginBottom: '0.5rem',
              }}
            >
              {t(sectionKey)}
            </h3>
            <div style={{ display: 'flex', flexDirection: 'column', gap: '0.375rem' }}>
              {bindings.map((b) => (
                <div
                  key={b.keys}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'space-between',
                    padding: '0.25rem 0',
                  }}
                >
                  <span style={{ fontSize: 'var(--nf-text-sm)' }}>{t(b.labelKey)}</span>
                  <span style={{ display: 'flex', gap: '0.25rem' }}>
                    {b.keys.split(' ').map((k) => (
                      <kbd
                        key={k}
                        style={{
                          display: 'inline-flex',
                          alignItems: 'center',
                          justifyContent: 'center',
                          minWidth: '1.5rem',
                          height: '1.5rem',
                          padding: '0 0.375rem',
                          borderRadius: '0.25rem',
                          border: '1px solid var(--nf-color-border)',
                          backgroundColor: 'var(--nf-color-bg-elevated)',
                          fontSize: 'var(--nf-text-xs)',
                          fontFamily: 'inherit',
                        }}
                      >
                        {k}
                      </kbd>
                    ))}
                  </span>
                </div>
              ))}
            </div>
          </div>
        ))}
      </div>
    </Dialog>
  );
}
