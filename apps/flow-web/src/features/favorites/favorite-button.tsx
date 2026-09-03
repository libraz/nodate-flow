/**
 * FavoriteButton — star toggle for adding/removing favorites.
 */
import Button from '@nodate-flow/ui/primitives/button';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import { type FavoriteTargetType, useAddFavorite, useRemoveFavorite } from './api';

export interface FavoriteButtonProps {
  workspaceId: string;
  targetType: FavoriteTargetType;
  targetId: string;
  /** The favorite entry ID, if already favorited. */
  favoriteId?: string;
}

export default function FavoriteButton({
  workspaceId,
  targetType,
  targetId,
  favoriteId,
}: FavoriteButtonProps): ReactElement {
  const { t } = useTranslation('labels');
  const add = useAddFavorite();
  const remove = useRemoveFavorite();
  const [pending, setPending] = useState(false);
  const isFavorite = !!favoriteId;

  const handleToggle = async (): Promise<void> => {
    if (pending) return;
    setPending(true);
    try {
      if (isFavorite && favoriteId) {
        await remove.mutateAsync({ id: favoriteId, workspaceId });
        toaster.show({ tone: 'success', message: t('favorites.removed') });
      } else {
        await add.mutateAsync({ workspaceId, targetType, targetId });
        toaster.show({ tone: 'success', message: t('favorites.added') });
      }
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(
          err,
          t,
          isFavorite ? 'favorites.error_remove' : 'favorites.error_add',
        ),
      });
    } finally {
      setPending(false);
    }
  };

  return (
    <Button
      variant="ghost"
      size="sm"
      onClick={() => {
        void handleToggle();
      }}
      disabled={pending}
      aria-label={t(isFavorite ? 'favorites.remove' : 'favorites.add')}
      aria-pressed={isFavorite}
    >
      <svg
        xmlns="http://www.w3.org/2000/svg"
        viewBox="0 0 20 20"
        className="h-4 w-4"
        fill={isFavorite ? 'var(--nf-color-warning)' : 'none'}
        stroke="currentColor"
        strokeWidth={1.5}
        aria-hidden="true"
      >
        <path
          strokeLinecap="round"
          strokeLinejoin="round"
          d="M9.049 2.927c.3-.921 1.603-.921 1.902 0l1.07 3.292a1 1 0 0 0 .95.69h3.462c.969 0 1.371 1.24.588 1.81l-2.8 2.034a1 1 0 0 0-.364 1.118l1.07 3.292c.3.921-.755 1.688-1.54 1.118l-2.8-2.034a1 1 0 0 0-1.175 0l-2.8 2.034c-.784.57-1.838-.197-1.539-1.118l1.07-3.292a1 1 0 0 0-.364-1.118L2.98 8.72c-.783-.57-.38-1.81.588-1.81h3.461a1 1 0 0 0 .951-.69l1.07-3.292Z"
        />
      </svg>
    </Button>
  );
}
