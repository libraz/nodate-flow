/**
 * ReactionBar — displays grouped emoji reactions for a task.
 *
 * Shows each unique emoji as a pill with its count. The current user can
 * toggle their own reaction by clicking a pill, or pick a new emoji from
 * the quick-pick popover.
 */
import Button from '@nodate-flow/ui/primitives/button';
import Popover from '@nodate-flow/ui/primitives/popover';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import type { Reaction } from './api';
import { useAddReaction, useRemoveReaction } from './api';

/** Common emoji quick-picks. */
const QUICK_EMOJIS = [
  '\u{1F44D}',
  '\u{1F44E}',
  '\u2764\uFE0F',
  '\u{1F389}',
  '\u{1F604}',
  '\u{1F680}',
  '\u{1F440}',
  '\u{1F4AF}',
];

export interface ReactionBarProps {
  taskId: string;
  reactions: Reaction[];
  currentUserId: string;
}

interface GroupedReaction {
  emoji: string;
  count: number;
  userNames: string[];
  /** The current user's reaction ID, if they reacted with this emoji. */
  myReactionId: string | undefined;
}

function groupReactions(reactions: Reaction[], currentUserId: string): GroupedReaction[] {
  const map = new Map<string, GroupedReaction>();
  for (const r of reactions) {
    const existing = map.get(r.emoji);
    if (existing) {
      existing.count++;
      existing.userNames.push(r.userDisplayName);
      if (r.userId === currentUserId) existing.myReactionId = r.id;
    } else {
      map.set(r.emoji, {
        emoji: r.emoji,
        count: 1,
        userNames: [r.userDisplayName],
        myReactionId: r.userId === currentUserId ? r.id : undefined,
      });
    }
  }
  return Array.from(map.values());
}

export default function ReactionBar({
  taskId,
  reactions,
  currentUserId,
}: ReactionBarProps): ReactElement {
  const { t } = useTranslation('reactions');
  const addReaction = useAddReaction();
  const removeReaction = useRemoveReaction();

  const grouped = groupReactions(reactions, currentUserId);

  const handleToggle = (emoji: string, myReactionId?: string): void => {
    if (myReactionId) {
      void removeReaction.mutateAsync({ taskId, reactionId: myReactionId });
    } else {
      void addReaction.mutateAsync({ taskId, emoji });
    }
  };

  return (
    <div className="flex items-center gap-1.5 flex-wrap">
      {grouped.map((g) => (
        <button
          key={g.emoji}
          type="button"
          onClick={() => handleToggle(g.emoji, g.myReactionId)}
          className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs transition-colors ${
            g.myReactionId
              ? 'border-[var(--nf-color-accent)] bg-[var(--nf-color-accent-subtle)]'
              : 'border-[var(--nf-color-border)] hover:bg-[var(--nf-color-bg-hover)]'
          }`}
          title={g.userNames.join(', ')}
          aria-label={`${g.emoji} ${g.count}`}
        >
          <span>{g.emoji}</span>
          <span className="text-[var(--nf-color-fg-muted)]">{g.count}</span>
        </button>
      ))}
      <Popover
        content={
          <div className="grid grid-cols-4 gap-1 p-2">
            {QUICK_EMOJIS.map((emoji) => (
              <button
                key={emoji}
                type="button"
                onClick={() => handleToggle(emoji)}
                className="rounded p-1.5 text-lg hover:bg-[var(--nf-color-bg-hover)]"
              >
                {emoji}
              </button>
            ))}
          </div>
        }
      >
        <Button variant="ghost" size="sm" aria-label={t('add')}>
          <svg
            xmlns="http://www.w3.org/2000/svg"
            viewBox="0 0 20 20"
            fill="currentColor"
            className="h-4 w-4"
            aria-hidden="true"
          >
            <path
              fillRule="evenodd"
              d="M10 18a8 8 0 1 0 0-16 8 8 0 0 0 0 16Zm3.536-4.464a.75.75 0 1 0-1.06-1.06 3.5 3.5 0 0 1-4.95 0 .75.75 0 0 0-1.06 1.06 5 5 0 0 0 7.07 0ZM9 8.5c0 .828-.448 1.5-1 1.5s-1-.672-1-1.5S7.448 7 8 7s1 .672 1 1.5Zm3 1.5c.552 0 1-.672 1-1.5S12.552 7 12 7s-1 .672-1 1.5.448 1.5 1 1.5Z"
              clipRule="evenodd"
            />
          </svg>
        </Button>
      </Popover>
    </div>
  );
}
