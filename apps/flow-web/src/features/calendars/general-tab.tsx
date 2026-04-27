/**
 * GeneralTab — body of the Calendar Settings Drawer's "General" tab.
 *
 * Lets the actor rename the calendar, change its display color from a
 * curated palette, and edit the description. A Danger zone at the
 * bottom houses the delete affordance, gated behind a themed confirm
 * dialog that surfaces the calendar's event count so the user
 * understands the blast radius of the operation.
 *
 * Personal / system calendars (kind !== 'team' / 'project') usually
 * cannot be deleted; the destroy button stays disabled in that case
 * and the hint explains why.
 */

import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import { confirmAction } from '../../lib/confirm-action';
import {
  useCalendarEventCountQuery,
  useCalendarQuery,
  useDeleteCalendarMutation,
  useUpdateCalendarMutation,
} from './api';
import styles from './calendar-settings-drawer.module.css';

/**
 * Curated 10-swatch calendar palette. Each swatch carries a stable
 * hex (the value persisted by the API and rendered on event chips
 * outside the themed canvas) and a paired design token used to paint
 * the picker swatch. Swatch rendering goes through the token so the
 * picker reads as native to the active theme; the persisted hex is
 * retained as the canonical cross-theme identity. Custom hex pickers
 * are out of scope for v1.
 */
export const COLOR_PALETTE: ReadonlyArray<{
  hex: string;
  token: string;
  nameKey: string;
}> = [
  {
    hex: '#2563eb',
    token: 'var(--nf-cal-color-1)',
    nameKey: 'calendar.settings.general.color.blue',
  },
  {
    hex: '#0891b2',
    token: 'var(--nf-cal-color-2)',
    nameKey: 'calendar.settings.general.color.cyan',
  },
  {
    hex: '#16a34a',
    token: 'var(--nf-cal-color-3)',
    nameKey: 'calendar.settings.general.color.green',
  },
  {
    hex: '#ca8a04',
    token: 'var(--nf-cal-color-4)',
    nameKey: 'calendar.settings.general.color.amber',
  },
  {
    hex: '#ea580c',
    token: 'var(--nf-cal-color-5)',
    nameKey: 'calendar.settings.general.color.orange',
  },
  {
    hex: '#dc2626',
    token: 'var(--nf-cal-color-6)',
    nameKey: 'calendar.settings.general.color.red',
  },
  {
    hex: '#db2777',
    token: 'var(--nf-cal-color-7)',
    nameKey: 'calendar.settings.general.color.pink',
  },
  {
    hex: '#9333ea',
    token: 'var(--nf-cal-color-8)',
    nameKey: 'calendar.settings.general.color.purple',
  },
  {
    hex: '#475569',
    token: 'var(--nf-cal-color-9)',
    nameKey: 'calendar.settings.general.color.slate',
  },
  {
    hex: '#0f172a',
    token: 'var(--nf-cal-color-10)',
    nameKey: 'calendar.settings.general.color.ink',
  },
];

export interface GeneralTabProps {
  workspaceId: string;
  calendarId: string;
  onAfterDelete: () => void;
}

export default function GeneralTab({
  workspaceId,
  calendarId,
  onAfterDelete,
}: GeneralTabProps): ReactElement {
  const { t } = useTranslation('common');
  const { data: calendar } = useCalendarQuery(workspaceId, calendarId);
  const update = useUpdateCalendarMutation();
  const destroy = useDeleteCalendarMutation();
  const eventCountQuery = useCalendarEventCountQuery(workspaceId, calendarId, true);

  const [name, setName] = useState(calendar.name);
  const [description, setDescription] = useState(calendar.description ?? '');
  const [color, setColor] = useState(calendar.color);

  const isReadonly = calendar.role !== 'owner' && calendar.role !== 'manager';
  const isDeletable = !isReadonly && calendar.kind !== 'personal' && !calendar.systemSlug;

  const dirty =
    name !== calendar.name ||
    description !== (calendar.description ?? '') ||
    color !== calendar.color;

  const handleSave = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    if (!dirty) return;
    const trimmed = name.trim();
    if (trimmed.length === 0) {
      toaster.show({ tone: 'danger', message: t('calendar.settings.general.name_required') });
      return;
    }
    update.mutate(
      {
        wsId: workspaceId,
        calId: calendarId,
        body: {
          name: trimmed,
          description: description.trim(),
          color,
        },
      },
      {
        onSuccess: () => {
          toaster.show({ tone: 'success', message: t('calendar.settings.general.saved') });
        },
        onError: (err) => {
          const message = formatApiError(err, t, 'calendar.settings.general.save_error');
          toaster.show({ tone: 'danger', message });
        },
      },
    );
  };

  const handleDelete = async (): Promise<void> => {
    const count = eventCountQuery.data ?? 0;
    const ok = await confirmAction({
      title: t('calendar.settings.delete.confirm_title', { name: calendar.name }),
      message: t('calendar.settings.delete.confirm_count', { count }),
      tone: 'danger',
      confirmLabel: t('calendar.settings.delete.confirm'),
      cancelLabel: t('calendar.settings.delete.cancel'),
    });
    if (!ok) return;
    destroy.mutate(
      { wsId: workspaceId, calId: calendarId },
      {
        onSuccess: () => {
          toaster.show({ tone: 'success', message: t('calendar.settings.delete.success') });
          onAfterDelete();
        },
        onError: (err) => {
          const message = formatApiError(err, t, 'calendar.settings.delete.error');
          toaster.show({ tone: 'danger', message });
        },
      },
    );
  };

  return (
    <form
      onSubmit={handleSave}
      style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-3)' }}
    >
      <FormField label={t('calendar.settings.general.name_label')} required>
        {(control) => (
          <Input
            {...control}
            value={name}
            onChange={(e) => setName(e.target.value)}
            disabled={isReadonly || update.isPending}
            maxLength={120}
          />
        )}
      </FormField>

      <div className={styles.field}>
        <span className={styles.fieldLabel}>{t('calendar.settings.general.color_label')}</span>
        <div
          className={styles.swatchGrid}
          role="radiogroup"
          aria-label={t('calendar.settings.general.color_label')}
        >
          {COLOR_PALETTE.map((swatch) => {
            const active = swatch.hex.toLowerCase() === color.toLowerCase();
            return (
              <button
                key={swatch.hex}
                type="button"
                role="radio"
                aria-checked={active}
                aria-label={t(swatch.nameKey)}
                data-color={swatch.hex}
                className={active ? `${styles.swatch} ${styles.swatchActive}` : styles.swatch}
                style={{ background: swatch.token }}
                onClick={() => setColor(swatch.hex)}
                disabled={isReadonly || update.isPending}
              />
            );
          })}
        </div>
      </div>

      <FormField label={t('calendar.settings.general.description_label')}>
        {(control) => (
          <Textarea
            {...control}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            disabled={isReadonly || update.isPending}
            rows={3}
            maxLength={500}
          />
        )}
      </FormField>

      <div className={styles.actions}>
        <Button
          type="submit"
          variant="primary"
          size="sm"
          disabled={isReadonly || !dirty || update.isPending}
        >
          {t('calendar.settings.general.save')}
        </Button>
      </div>

      <section className={styles.dangerZone} aria-label={t('calendar.settings.delete.title')}>
        <h3 className={styles.dangerTitle}>{t('calendar.settings.delete.title')}</h3>
        <p className={styles.dangerHint}>
          {isDeletable
            ? t('calendar.settings.delete.hint')
            : t('calendar.settings.delete.cannot_delete')}
        </p>
        <div className={styles.actions}>
          <Button
            type="button"
            variant="danger"
            size="sm"
            onClick={() => {
              void handleDelete();
            }}
            disabled={!isDeletable || destroy.isPending}
          >
            {t('calendar.settings.delete.trigger')}
          </Button>
        </div>
      </section>
    </form>
  );
}
