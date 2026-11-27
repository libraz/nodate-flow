/**
 * LabelsTab — body of the Calendar Settings Drawer's "Labels" tab.
 *
 * Everyone on the calendar sees the label list; owners and managers can
 * add, edit and delete. The add / edit form opens inline (an edit takes
 * the place of its row) and ends in a preview pill drawn with the same
 * CSS as the calendar's real pills.
 */

import { CalendarErrors } from '@nodate-flow/sdk';
import Button from '@nodate-flow/ui/primitives/button';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import { Pencil, Plus, Tag, Trash2 } from 'lucide-react';
import {
  type FormEvent,
  type KeyboardEvent,
  type ReactElement,
  useId,
  useRef,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';

import { ApiError } from '../../lib/api-error';
import { confirmAction } from '../../lib/confirm-action';
import { FALLBACK_EVENT_COLOR, isHexColor, LABEL_SWATCH_ROWS } from '../../lib/source-colors';
import { EventPillPreview, eventColorStyle } from '../calendar-events/event-pill';
import { useCalendarQuery } from './api';
import {
  type CalendarLabel,
  useCalendarLabelsQuery,
  useCreateCalendarLabelMutation,
  useDeleteCalendarLabelMutation,
  useUpdateCalendarLabelMutation,
} from './labels-api';
import styles from './labels-tab.module.css';

const MAX_LABELS = 50;
const NAME_MAX_LENGTH = 50;
const NAME_COUNTER_FROM = 40;
const HEX_MAX_LENGTH = 7;
const SWATCH_COLUMNS = LABEL_SWATCH_ROWS[0]?.length ?? 10;
const SWATCHES = LABEL_SWATCH_ROWS.flat();

type Editor = { mode: 'create' } | { mode: 'edit'; id: string };

function sameColor(a: string, b: string): boolean {
  return a.toLowerCase() === b.toLowerCase();
}

function defaultColor(labels: CalendarLabel[]): string {
  const standard = LABEL_SWATCH_ROWS[1] ?? [];
  const free = standard.find((hex) => !labels.some((l) => sameColor(l.color, hex)));
  return free ?? standard[0] ?? FALLBACK_EVENT_COLOR;
}

export interface LabelsTabProps {
  workspaceId: string;
  calendarId: string;
}

export default function LabelsTab({ workspaceId, calendarId }: LabelsTabProps): ReactElement {
  const { t } = useTranslation('common');
  const { data: calendar } = useCalendarQuery(workspaceId, calendarId);
  const query = useCalendarLabelsQuery(workspaceId, calendarId);
  const createLabel = useCreateCalendarLabelMutation();
  const updateLabel = useUpdateCalendarLabelMutation();
  const deleteLabel = useDeleteCalendarLabelMutation();

  const [editor, setEditor] = useState<Editor | null>(null);
  const [takenName, setTakenName] = useState<string | null>(null);

  const canManage = calendar.role === 'owner' || calendar.role === 'manager';
  const labels = query.data ?? [];
  const atLimit = labels.length >= MAX_LABELS;

  const openEditor = (next: Editor | null): void => {
    setTakenName(null);
    setEditor(next);
  };

  const handleFailure = (err: unknown, name: string | undefined, fallbackKey: string): void => {
    if (
      name !== undefined &&
      err instanceof ApiError &&
      err.code === CalendarErrors.CALENDAR_LABEL_NAME_TAKEN.code
    ) {
      setTakenName(name);
      return;
    }
    toaster.show({ tone: 'danger', message: t(fallbackKey) });
  };

  const handleCreate = (name: string, color: string): void => {
    createLabel.mutate(
      { wsId: workspaceId, calId: calendarId, name, color },
      {
        onSuccess: () => openEditor(null),
        onError: (err) => handleFailure(err, name, 'calendar.labels.create_failed'),
      },
    );
  };

  const handleUpdate = (label: CalendarLabel, name: string, color: string): void => {
    const body: { name?: string; color?: string } = {};
    if (name !== label.name) body.name = name;
    if (!sameColor(color, label.color)) body.color = color;
    updateLabel.mutate(
      { wsId: workspaceId, calId: calendarId, labelId: label.id, body },
      {
        onSuccess: () => openEditor(null),
        onError: (err) => handleFailure(err, body.name, 'calendar.labels.update_failed'),
      },
    );
  };

  const handleDelete = async (label: CalendarLabel): Promise<void> => {
    const ok = await confirmAction({
      title: t('calendar.labels.delete_title', { name: label.name }),
      message: t('calendar.labels.delete_body'),
      tone: 'danger',
      confirmLabel: t('calendar.labels.delete_confirm'),
    });
    if (!ok) return;
    deleteLabel.mutate(
      { wsId: workspaceId, calId: calendarId, labelId: label.id },
      {
        onSuccess: () => {
          if (editor?.mode === 'edit' && editor.id === label.id) openEditor(null);
        },
        onError: (err) => handleFailure(err, undefined, 'calendar.labels.delete_failed'),
      },
    );
  };

  if (query.isPending) {
    return (
      <div className={styles.body} aria-busy="true">
        <Skeleton className={styles.skeletonRow} />
        <Skeleton className={styles.skeletonRow} />
      </div>
    );
  }

  if (query.isError) {
    return (
      <div className={styles.body}>
        <p role="alert" className={styles.loadFailed}>
          {t('calendar.labels.load_failed')}
        </p>
        <Button
          type="button"
          size="sm"
          onClick={() => {
            void query.refetch();
          }}
        >
          {t('calendar.labels.retry')}
        </Button>
      </div>
    );
  }

  return (
    <div className={styles.body}>
      {!canManage && <p className={styles.notice}>{t('calendar.labels.readonly_hint')}</p>}

      <div className={styles.header}>
        <span className={styles.count} data-testid="label-count">
          {t('calendar.labels.count', { count: labels.length, max: MAX_LABELS })}
        </span>
        {canManage && (
          <Button
            type="button"
            size="sm"
            disabled={atLimit || editor?.mode === 'create'}
            onClick={() => openEditor({ mode: 'create' })}
          >
            <Plus aria-hidden="true" size={14} />
            {t('calendar.labels.add')}
          </Button>
        )}
      </div>
      {canManage && atLimit && (
        <p className={styles.supporting}>
          {t('calendar.labels.limit_reached', { max: MAX_LABELS })}
        </p>
      )}

      {editor?.mode === 'create' && (
        <LabelForm
          key="create"
          labels={labels}
          initialName=""
          initialColor={defaultColor(labels)}
          takenName={takenName}
          saving={createLabel.isPending}
          isEdit={false}
          onSubmit={handleCreate}
          onCancel={() => openEditor(null)}
        />
      )}

      {labels.length === 0 ? (
        editor === null && (
          <EmptyState
            icon={<Tag aria-hidden="true" size={24} />}
            title={t('calendar.labels.empty_title')}
            description={t('calendar.labels.empty_body')}
          />
        )
      ) : (
        <ul className={styles.list}>
          {labels.map((label) =>
            editor?.mode === 'edit' && editor.id === label.id ? (
              <li key={label.id}>
                <LabelForm
                  labels={labels}
                  editingId={label.id}
                  initialName={label.name}
                  initialColor={label.color}
                  takenName={takenName}
                  saving={updateLabel.isPending}
                  isEdit
                  onSubmit={(name, color) => handleUpdate(label, name, color)}
                  onCancel={() => openEditor(null)}
                />
              </li>
            ) : (
              <li key={label.id} className={styles.row}>
                <span
                  aria-hidden="true"
                  className={styles.dot}
                  style={eventColorStyle(label.color)}
                />
                <span className={styles.name} title={label.name}>
                  {label.name}
                </span>
                {canManage && (
                  <span className={styles.actions}>
                    <Tooltip content={t('calendar.labels.edit', { name: label.name })}>
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        aria-label={t('calendar.labels.edit', { name: label.name })}
                        onClick={() => openEditor({ mode: 'edit', id: label.id })}
                      >
                        <Pencil aria-hidden="true" size={14} />
                      </Button>
                    </Tooltip>
                    <Tooltip content={t('calendar.labels.delete', { name: label.name })}>
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        aria-label={t('calendar.labels.delete', { name: label.name })}
                        disabled={deleteLabel.isPending}
                        onClick={() => {
                          void handleDelete(label);
                        }}
                      >
                        <Trash2 aria-hidden="true" size={14} />
                      </Button>
                    </Tooltip>
                  </span>
                )}
              </li>
            ),
          )}
        </ul>
      )}
    </div>
  );
}

interface LabelFormProps {
  labels: CalendarLabel[];
  editingId?: string;
  initialName: string;
  initialColor: string;
  /** Name the server just rejected as a duplicate; the error shows while the field still holds it. */
  takenName: string | null;
  saving: boolean;
  isEdit: boolean;
  onSubmit: (name: string, color: string) => void;
  onCancel: () => void;
}

function LabelForm({
  labels,
  editingId,
  initialName,
  initialColor,
  takenName,
  saving,
  isEdit,
  onSubmit,
  onCancel,
}: LabelFormProps): ReactElement {
  const { t } = useTranslation('common');
  const colorLabelId = useId();
  const [name, setName] = useState(initialName);
  const [color, setColor] = useState(initialColor);
  const swatchRefs = useRef<Array<HTMLButtonElement | null>>([]);

  const trimmed = name.trim();
  const colorValid = isHexColor(color);
  const dirty = !isEdit || trimmed !== initialName || !sameColor(color, initialColor);
  const nameTaken = takenName !== null && trimmed === takenName;
  const canSave = trimmed.length > 0 && colorValid && dirty && !saving;

  const usedBy = (hex: string): string[] =>
    labels.filter((l) => l.id !== editingId && sameColor(l.color, hex)).map((l) => l.name);

  const selectedIndex = SWATCHES.findIndex((hex) => sameColor(hex, color));
  const tabbableIndex = Math.max(selectedIndex, 0);

  const handleSwatchKeyDown = (event: KeyboardEvent<HTMLButtonElement>, index: number): void => {
    let next = index;
    switch (event.key) {
      case 'ArrowRight':
      case 'ArrowDown':
        next = event.key === 'ArrowDown' ? index + SWATCH_COLUMNS : index + 1;
        break;
      case 'ArrowLeft':
      case 'ArrowUp':
        next = event.key === 'ArrowUp' ? index - SWATCH_COLUMNS : index - 1;
        break;
      case 'Home':
        next = 0;
        break;
      case 'End':
        next = SWATCHES.length - 1;
        break;
      default:
        return;
    }
    event.preventDefault();
    const hex = SWATCHES[(next + SWATCHES.length) % SWATCHES.length];
    if (hex === undefined) return;
    setColor(hex);
    swatchRefs.current[(next + SWATCHES.length) % SWATCHES.length]?.focus();
  };

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    if (!canSave) return;
    onSubmit(trimmed, color);
  };

  const remaining = NAME_MAX_LENGTH - name.length;

  return (
    <form className={styles.form} onSubmit={handleSubmit} noValidate>
      <FormField
        label={t('calendar.labels.name')}
        error={nameTaken ? t('calendar.labels.name_taken') : undefined}
        description={
          name.length >= NAME_COUNTER_FROM
            ? t('calendar.labels.name_remaining', { count: remaining })
            : undefined
        }
      >
        {(control) => (
          <Input
            {...control}
            value={name}
            maxLength={NAME_MAX_LENGTH}
            placeholder={t('calendar.labels.name_placeholder')}
            onChange={(e) => setName(e.target.value)}
            autoFocus
          />
        )}
      </FormField>

      <div className={styles.colorField}>
        <span id={colorLabelId} className={styles.fieldLabel}>
          {t('calendar.labels.color')}
        </span>
        <div role="radiogroup" aria-labelledby={colorLabelId} className={styles.swatches}>
          {SWATCHES.map((hex, index) => {
            const users = usedBy(hex);
            const selected = index === selectedIndex;
            const button = (
              <button
                key={hex}
                ref={(el) => {
                  swatchRefs.current[index] = el;
                }}
                type="button"
                role="radio"
                aria-checked={selected}
                aria-label={t('calendar.labels.swatch', { hex })}
                tabIndex={index === tabbableIndex ? 0 : -1}
                className={styles.swatch}
                style={eventColorStyle(hex)}
                onClick={() => setColor(hex)}
                onKeyDown={(e) => handleSwatchKeyDown(e, index)}
              >
                {users.length > 0 && <span aria-hidden="true" className={styles.inUse} />}
              </button>
            );
            return users.length > 0 ? (
              <Tooltip key={hex} content={t('calendar.labels.in_use', { names: users.join(', ') })}>
                {button}
              </Tooltip>
            ) : (
              button
            );
          })}
        </div>
      </div>

      <FormField
        label={t('calendar.labels.custom_color')}
        error={colorValid ? undefined : t('calendar.labels.custom_color_invalid')}
      >
        {(control) => (
          <Input
            {...control}
            dir="ltr"
            value={color}
            maxLength={HEX_MAX_LENGTH}
            spellCheck={false}
            onChange={(e) => setColor(e.target.value)}
          />
        )}
      </FormField>

      <div className={styles.preview}>
        <span className={styles.fieldLabel}>{t('calendar.labels.preview')}</span>
        <EventPillPreview
          color={colorValid ? color : FALLBACK_EVENT_COLOR}
          title={trimmed.length > 0 ? trimmed : t('calendar.labels.preview_title')}
        />
      </div>

      <div className={styles.formActions}>
        <Button type="button" variant="ghost" size="sm" onClick={onCancel}>
          {t('common.cancel')}
        </Button>
        <Button
          type="submit"
          variant="primary"
          size="sm"
          disabled={!canSave}
          aria-busy={saving || undefined}
        >
          {t('calendar.labels.save')}
        </Button>
      </div>
    </form>
  );
}
