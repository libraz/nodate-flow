/**
 * MemberColorPopover — the colour circle on a Members tab row. Renders a
 * button that opens a palette popover when the viewer may change this
 * member's colour, and a same-looking static circle otherwise. Choosing a
 * colour saves immediately.
 */

import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Popover from '@nodate-flow/ui/primitives/popover';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import { type CSSProperties, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { isHexColor, MEMBER_COLOR_PALETTE } from '../../lib/source-colors';
import styles from './member-color-popover.module.css';
import { type CalendarMember, useUpdateMemberColor } from './members-api';

export interface MemberColorPopoverProps {
  workspaceId: string;
  calendarId: string;
  member: CalendarMember;
  members: readonly CalendarMember[];
  /** True when the viewer is this member or a manager and above. */
  canChange: boolean;
}

function colorStyle(color: string): CSSProperties {
  return { '--member-color': color } as CSSProperties;
}

export default function MemberColorPopover({
  workspaceId,
  calendarId,
  member,
  members,
  canChange,
}: MemberColorPopoverProps): ReactElement {
  const { t } = useTranslation('common');
  const updateColor = useUpdateMemberColor();
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState('');

  if (!canChange) {
    return (
      <span
        role="img"
        className={styles.circle}
        style={colorStyle(member.memberColor)}
        aria-label={t('calendar.settings.members.color_static', { name: member.displayName })}
      />
    );
  }

  const label = t('calendar.settings.members.color_change', { name: member.displayName });
  const draftInvalid = draft.length > 0 && !isHexColor(draft);

  const usersOf = (color: string): string[] =>
    members
      .filter(
        (m) => m.userId !== member.userId && m.memberColor.toLowerCase() === color.toLowerCase(),
      )
      .map((m) => m.displayName);

  const handlePick = (color: string): void => {
    setOpen(false);
    setDraft('');
    if (color.toLowerCase() === member.memberColor.toLowerCase()) return;
    updateColor.mutate({ wsId: workspaceId, calId: calendarId, userId: member.userId, color });
  };

  const handleDraftChange = (value: string): void => {
    setDraft(value);
    if (isHexColor(value)) handlePick(value);
  };

  const content = (
    <div className={styles.panel}>
      <div className={styles.grid}>
        {MEMBER_COLOR_PALETTE.map((color) => (
          <MemberSwatch
            key={color}
            color={color}
            selected={color.toLowerCase() === member.memberColor.toLowerCase()}
            usedBy={usersOf(color)}
            onPick={handlePick}
          />
        ))}
      </div>
      <FormField
        label={t('calendar.settings.members.color_custom')}
        error={draftInvalid ? t('calendar.labels.custom_color_invalid') : undefined}
      >
        {(control) => (
          <Input
            {...control}
            value={draft}
            maxLength={7}
            placeholder={member.memberColor}
            onChange={(e) => handleDraftChange(e.target.value)}
          />
        )}
      </FormField>
    </div>
  );

  return (
    <Popover open={open} onOpenChange={setOpen} content={content} ariaLabel={label}>
      <button
        type="button"
        className={`${styles.circle} ${styles.circleButton}`}
        style={colorStyle(member.memberColor)}
        aria-label={label}
      />
    </Popover>
  );
}

interface MemberSwatchProps {
  color: string;
  selected: boolean;
  /** Names of the other members already using this colour. */
  usedBy: readonly string[];
  onPick: (color: string) => void;
}

function MemberSwatch({ color, selected, usedBy, onPick }: MemberSwatchProps): ReactElement {
  const { t } = useTranslation('common');
  const swatch = (
    <button
      type="button"
      className={styles.swatch}
      style={colorStyle(color)}
      aria-label={t('calendar.labels.swatch', { hex: color })}
      aria-pressed={selected}
      onClick={() => onPick(color)}
    >
      {usedBy.length > 0 ? <span className={styles.inUse} aria-hidden="true" /> : null}
    </button>
  );
  if (usedBy.length === 0) return swatch;
  return (
    <Tooltip content={t('calendar.settings.members.color_in_use', { names: usedBy.join(', ') })}>
      {swatch}
    </Tooltip>
  );
}
