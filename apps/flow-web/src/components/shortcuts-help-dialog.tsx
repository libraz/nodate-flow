/**
 * ShortcutsHelpDialog — lists all global keyboard shortcuts grouped by
 * section. Triggered by pressing "?" or from a menu action. Sequences
 * ("g i") are drawn as keys pressed one after another; the command
 * palette shortcut is a modifier combination, drawn as one key cap.
 */

import Dialog from '@nodate-flow/ui/primitives/dialog';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { SHORTCUT_BINDINGS } from '../lib/use-keyboard-shortcuts';
import { paletteShortcutLabel } from './layout/palette-shortcut';
import styles from './shortcuts-help-dialog.module.css';

export interface ShortcutsHelpDialogProps {
  open: boolean;
  onClose: () => void;
}

interface ShortcutRow {
  /** Space-separated keys pressed in sequence; a single entry is one key or combination. */
  keys: string[];
  label: string;
}

function KeySequence({ keys }: { keys: string[] }): ReactElement {
  return (
    <span className={styles.keys}>
      {keys.map((k, i) => (
        <span key={k} className={styles.step}>
          {i > 0 ? (
            <span aria-hidden className={styles.then}>
              →
            </span>
          ) : null}
          <kbd className={styles.kbd}>{k}</kbd>
        </span>
      ))}
    </span>
  );
}

export default function ShortcutsHelpDialog({
  open,
  onClose,
}: ShortcutsHelpDialogProps): ReactElement {
  const { t } = useTranslation(['common', 'help']);

  // Group bindings by section.
  const sections = new Map<string, ShortcutRow[]>();
  for (const b of SHORTCUT_BINDINGS) {
    const existing = sections.get(b.sectionKey) ?? [];
    existing.push({ keys: b.keys.split(' '), label: t(b.labelKey) });
    sections.set(b.sectionKey, existing);
  }
  // The palette shortcut is bound by the top bar, not the global key handler.
  const general = sections.get('shortcuts.section_general') ?? [];
  general.unshift({ keys: [paletteShortcutLabel()], label: t('shortcuts.command_palette') });
  sections.set('shortcuts.section_general', general);

  return (
    <Dialog open={open} onClose={onClose} title={t('shortcuts.title')}>
      <div className={styles.body}>
        <p className={styles.summary}>{t('help:shortcuts.summary')}</p>
        {[...sections.entries()].map(([sectionKey, rows]) => (
          <section key={sectionKey} className={styles.section}>
            <h3 className={styles.sectionTitle}>{t(sectionKey)}</h3>
            <ul className={styles.rows}>
              {rows.map((row) => (
                <li key={row.keys.join(' ')} className={styles.row}>
                  <span className={styles.label}>{row.label}</span>
                  <KeySequence keys={row.keys} />
                </li>
              ))}
            </ul>
            {rows.some((row) => row.keys.length > 1) ? (
              <p className={styles.note}>{t('shortcuts.chord_hint')}</p>
            ) : null}
          </section>
        ))}
        <ul className={styles.tips}>
          <li>{t('help:shortcuts.tips.t2')}</li>
          <li>{t('help:shortcuts.tips.t3')}</li>
        </ul>
      </div>
    </Dialog>
  );
}
