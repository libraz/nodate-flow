/**
 * ReactionBar — displays grouped emoji reactions for a task.
 *
 * Shows each unique emoji as a pill with its count. The current user can
 * toggle their own reaction by clicking a pill, or pick a new emoji from
 * the quick-pick popover.
 */
import Button from '@nodate-flow/ui/primitives/button';
import Popover from '@nodate-flow/ui/primitives/popover';
import { SmilePlus } from 'lucide-react';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import type { Reaction } from './api';
import { useAddReaction, useRemoveReaction } from './api';
import styles from './reaction-bar.module.css';

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

  const busy = addReaction.isPending || removeReaction.isPending;
  const grouped = groupReactions(reactions, currentUserId);

  const handleToggle = (emoji: string, myReactionId?: string): void => {
    if (myReactionId) {
      void removeReaction.mutateAsync({ taskId, reactionId: myReactionId });
    } else {
      void addReaction.mutateAsync({ taskId, emoji });
    }
  };

  return (
    <div className={styles.bar}>
      {grouped.map((g) => (
        <button
          key={g.emoji}
          type="button"
          onClick={() => handleToggle(g.emoji, g.myReactionId)}
          disabled={busy}
          aria-busy={
            (addReaction.isPending && addReaction.variables?.emoji === g.emoji) ||
            (removeReaction.isPending &&
              g.myReactionId !== undefined &&
              removeReaction.variables?.reactionId === g.myReactionId) ||
            undefined
          }
          className={`${styles.pill ?? ''} ${g.myReactionId ? (styles.pillMine ?? '') : ''}`}
          title={g.userNames.join(', ')}
          aria-label={`${g.emoji} ${g.count}`}
        >
          <span>{g.emoji}</span>
          <span className={styles.count}>{g.count}</span>
        </button>
      ))}
      <Popover
        content={
          <div className={styles.picker}>
            {QUICK_EMOJIS.map((emoji) => (
              <button
                key={emoji}
                type="button"
                onClick={() => handleToggle(emoji)}
                disabled={busy}
                className={styles.pickerItem}
              >
                {emoji}
              </button>
            ))}
          </div>
        }
      >
        <Button
          variant="ghost"
          size="sm"
          className={styles.add}
          aria-label={t('add')}
          title={t('add')}
          disabled={busy}
        >
          <SmilePlus size={16} aria-hidden />
        </Button>
      </Popover>
    </div>
  );
}
