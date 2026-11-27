/**
 * ActivityTabs — tablist for the block at the end of the task detail main
 * column. A panel mounts the first time its tab is opened and then stays
 * mounted (hidden when inactive), so a draft in one survives switching tabs.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import {
  type KeyboardEvent,
  type ReactElement,
  type ReactNode,
  useId,
  useRef,
  useState,
} from 'react';
import css from './activity-tabs.module.css';

export interface ActivityTab {
  value: string;
  label: ReactNode;
  content: ReactNode;
}

export interface ActivityTabsProps {
  tabs: readonly ActivityTab[];
  ariaLabel: string;
}

export default function ActivityTabs({ tabs, ariaLabel }: ActivityTabsProps): ReactElement {
  const baseId = useId();
  const first = tabs[0]?.value ?? '';
  const [active, setActive] = useState(first);
  const [opened, setOpened] = useState<ReadonlySet<string>>(new Set([first]));
  const tabRefs = useRef(new Map<string, HTMLButtonElement>());

  const handleSelect = (value: string): void => {
    setActive(value);
    setOpened((prev) => (prev.has(value) ? prev : new Set(prev).add(value)));
  };

  const handleKeyDown = (e: KeyboardEvent, index: number): void => {
    const last = tabs.length - 1;
    const rtl = document.documentElement.dir === 'rtl';
    const forward = rtl ? 'ArrowLeft' : 'ArrowRight';
    const backward = rtl ? 'ArrowRight' : 'ArrowLeft';
    let next = index;
    if (e.key === forward) next = index === last ? 0 : index + 1;
    else if (e.key === backward) next = index === 0 ? last : index - 1;
    else if (e.key === 'Home') next = 0;
    else if (e.key === 'End') next = last;
    else return;
    e.preventDefault();
    const target = tabs[next];
    if (!target) return;
    handleSelect(target.value);
    tabRefs.current.get(target.value)?.focus();
  };

  return (
    <div className={css.root}>
      <div role="tablist" aria-label={ariaLabel} className={css.tablist}>
        {tabs.map((tab, index) => {
          const selected = tab.value === active;
          return (
            <button
              key={tab.value}
              ref={(node) => {
                if (node) tabRefs.current.set(tab.value, node);
                else tabRefs.current.delete(tab.value);
              }}
              type="button"
              role="tab"
              id={`${baseId}-tab-${tab.value}`}
              aria-selected={selected}
              aria-controls={`${baseId}-panel-${tab.value}`}
              tabIndex={selected ? 0 : -1}
              className={cx(css.tab, selected && css.tabActive)}
              onClick={() => handleSelect(tab.value)}
              onKeyDown={(e) => handleKeyDown(e, index)}
            >
              {tab.label}
            </button>
          );
        })}
      </div>
      {tabs.map((tab) => (
        <div
          key={tab.value}
          role="tabpanel"
          id={`${baseId}-panel-${tab.value}`}
          aria-labelledby={`${baseId}-tab-${tab.value}`}
          hidden={tab.value !== active}
        >
          {opened.has(tab.value) ? tab.content : null}
        </div>
      ))}
    </div>
  );
}
