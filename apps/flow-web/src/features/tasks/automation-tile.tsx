/**
 * AutomationTile — one capability of the "AI & automation" card: icon, title
 * with its hint, a one-line summary, then the current status and one action.
 * A tile with a `disclosure` keeps its working UI collapsed until the action
 * is used.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Collapse from '@nodate-flow/ui/primitives/collapse';
import type { LucideIcon } from 'lucide-react';
import { type ReactElement, type ReactNode, useId, useState } from 'react';
import css from './automation-tile.module.css';
import TaskActionButton from './task-action-button';
import { TaskSideCardHeading, TaskSideGroup } from './task-side-card';

export interface AutomationTileDisclosure {
  /** Label of the action that expands the tile. */
  openLabel: string;
  /** When set, a button with this label at the end of the expanded body collapses it again. */
  closeLabel?: string;
  /** `secondary` for a commit-like action, `tertiary` for a property-level one. */
  tier: 'secondary' | 'tertiary';
  /** Icon of the tertiary action; the secondary one is text only. */
  icon?: ReactNode;
  children: ReactNode;
}

export interface AutomationTileProps {
  icon: LucideIcon;
  title: string;
  hint: string;
  /** Small marker at the end of the title row, such as "suggests only". */
  marker?: ReactElement;
  summary: string;
  /** Current state of the capability, next to the action. */
  status?: ReactNode;
  /** Self-contained content under the summary, for a capability that needs no expansion. */
  body?: ReactNode;
  disclosure?: AutomationTileDisclosure;
}

export default function AutomationTile({
  icon: Icon,
  title,
  hint,
  marker,
  summary,
  status,
  body,
  disclosure,
}: AutomationTileProps): ReactElement {
  const [open, setOpen] = useState(false);
  const regionId = useId();
  return (
    <TaskSideGroup role="group" aria-label={title}>
      <div className={css.head}>
        <span className={css.icon} aria-hidden>
          <Icon size={16} />
        </span>
        <div className={css.heading}>
          <TaskSideCardHeading
            level="sub"
            title={title}
            hint={hint}
            {...(marker ? { marker } : {})}
          />
        </div>
      </div>
      <p className={css.summary}>{summary}</p>
      {status || disclosure ? (
        <div className={css.footer}>
          {status ? <div className={css.status}>{status}</div> : null}
          {disclosure ? (
            disclosure.tier === 'secondary' ? (
              <Button
                type="button"
                size="sm"
                aria-expanded={open}
                aria-controls={regionId}
                onClick={() => setOpen((v) => !v)}
              >
                {disclosure.openLabel}
              </Button>
            ) : (
              <TaskActionButton
                icon={disclosure.icon}
                aria-expanded={open}
                aria-controls={regionId}
                onClick={() => setOpen((v) => !v)}
              >
                {disclosure.openLabel}
              </TaskActionButton>
            )
          ) : null}
        </div>
      ) : null}
      {body}
      {disclosure ? (
        <Collapse open={open} id={regionId}>
          <div className={css.expanded}>
            {disclosure.children}
            {disclosure.closeLabel ? (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className={css.close}
                onClick={() => setOpen(false)}
              >
                {disclosure.closeLabel}
              </Button>
            ) : null}
          </div>
        </Collapse>
      ) : null}
    </TaskSideGroup>
  );
}
