/**
 * PageHelp: a quiet "?" button that opens a short explanatory panel for the
 * page it sits beside. Copy comes from the `help` namespace under `topic`:
 * `title`, `summary` and an ordered `tips` object (`t1`, `t2`, ...).
 */

import Popover from '@nodate-flow/ui/primitives/popover';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import { CircleHelp } from 'lucide-react';
import { type ButtonHTMLAttributes, type ReactElement, type Ref, useState } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './page-help.module.css';

/** Window event the authenticated layout listens for to open the shortcuts dialog. */
export const OPEN_SHORTCUTS_HELP_EVENT = 'nf:open-shortcuts-help';

export interface PageHelpProps {
  /** Key prefix inside the `help` namespace, e.g. `inbox`. */
  topic: string;
}

type TriggerProps = ButtonHTMLAttributes<HTMLButtonElement> & { ref?: Ref<HTMLElement> };

/**
 * The popover clones its child with a ref and interaction props; the tooltip
 * needs the button's own ref, so the popover anchors on a wrapping span.
 */
function HelpTrigger({ ref, ...buttonProps }: TriggerProps): ReactElement {
  const { t } = useTranslation('common');
  const label = t('help.trigger');
  return (
    <span ref={ref} className={styles.anchor}>
      <Tooltip content={label}>
        <button type="button" className={styles.trigger} aria-label={label} {...buttonProps}>
          <CircleHelp size={16} aria-hidden="true" />
        </button>
      </Tooltip>
    </span>
  );
}

export default function PageHelp({ topic }: PageHelpProps): ReactElement {
  const { t } = useTranslation('help');
  const { t: tCommon } = useTranslation('common');

  const title = t(`${topic}.title`, { defaultValue: '' });
  const summary = t(`${topic}.summary`, { defaultValue: '' });
  // `returnObjects` yields the raw key string when the bundle lacks the
  // entry; anything but a plain object means "no tips".
  const rawTips = t(`${topic}.tips`, { returnObjects: true, defaultValue: {} });
  const tips: string[] =
    typeof rawTips === 'object' && rawTips !== null && !Array.isArray(rawTips)
      ? Object.values(rawTips).filter((v): v is string => typeof v === 'string')
      : [];

  const [open, setOpen] = useState(false);

  const handleShortcuts = (): void => {
    setOpen(false);
    window.dispatchEvent(new CustomEvent(OPEN_SHORTCUTS_HELP_EVENT));
  };

  return (
    <Popover
      open={open}
      onOpenChange={setOpen}
      placement="bottom-start"
      focusPanel
      className={styles.panel}
      ariaLabel={title || tCommon('help.trigger')}
      content={
        <PanelBody
          eyebrow={tCommon('help.eyebrow')}
          title={title}
          summary={summary}
          tips={tips}
          shortcutsLabel={tCommon('help.shortcuts')}
          onShortcuts={handleShortcuts}
        />
      }
    >
      <HelpTrigger />
    </Popover>
  );
}

interface PanelBodyProps {
  eyebrow: string;
  title: string;
  summary: string;
  tips: string[];
  shortcutsLabel: string;
  onShortcuts: () => void;
}

function PanelBody({
  eyebrow,
  title,
  summary,
  tips,
  shortcutsLabel,
  onShortcuts,
}: PanelBodyProps): ReactElement {
  return (
    <div className={styles.body}>
      <header className={styles.head}>
        <p className={styles.eyebrow}>{eyebrow}</p>
        {title ? <h2 className={styles.title}>{title}</h2> : null}
        {summary ? <p className={styles.summary}>{summary}</p> : null}
      </header>
      {tips.length > 0 ? (
        <ul className={styles.tips}>
          {tips.map((tip) => (
            <li key={tip} className={styles.tip}>
              {tip}
            </li>
          ))}
        </ul>
      ) : null}
      <footer className={styles.foot}>
        <button type="button" className={styles.shortcuts} onClick={onShortcuts}>
          {shortcutsLabel}
        </button>
      </footer>
    </div>
  );
}
