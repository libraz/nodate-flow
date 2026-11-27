/**
 * IntakeConvertDialog — modal for promoting an intake item to a task.
 * The source title and body stay editable, and the actor can choose a
 * project, assignees, and due date before confirming.
 *
 * Rendered from {@link IntakeList} when the user clicks the "Convert"
 * row action. Workspace projects and members are loaded by the caller's
 * suspense boundary before the modal can be used.
 *
 * Project options are scoped to the active workspace via
 * `useProjectsQuery`. Once the mutation resolves we surface a success
 * toast that links the user to the freshly created task and close.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Checkbox from '@nodate-flow/ui/primitives/checkbox';
import Combobox, { type ComboboxOption } from '@nodate-flow/ui/primitives/combobox';
import DatePicker from '@nodate-flow/ui/primitives/date-picker';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { useNavigate } from '@tanstack/react-router';
import {
  type FormEvent,
  type ReactElement,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';
import FormActions from '../../../components/form-actions';
import { formatApiError } from '../../../lib/api-error';
import { formatDate } from '../../../lib/format';
import { onSubmitShortcut } from '../../../lib/submit-shortcut';
import { useWeekStart } from '../../../lib/use-week-start';
import { useProjectsQuery } from '../../projects/api';
import { useWorkspaceMembersQuery } from '../../workspaces/api';
import { safeSourceUrl } from '../source-intake/safe-source-url';
import { type IntakeItem, useConvertIntakeItemMutation } from './api';

export interface IntakeConvertDialogProps {
  workspaceId: string;
  item: IntakeItem | null;
  open: boolean;
  onClose: () => void;
}

export default function IntakeConvertDialog({
  workspaceId,
  item,
  open,
  onClose,
}: IntakeConvertDialogProps): ReactElement {
  const { t, i18n } = useTranslation('inbox');
  const locale = i18n.resolvedLanguage ?? 'en';
  const weekStart = useWeekStart();
  const weekdayLabels = t('common.date.weekdays', {
    ns: 'common',
    returnObjects: true,
  }) as string[];
  const formatMonthYear = (year: number, month: number): string =>
    t('common.date.monthYear', { ns: 'common', year, month });
  const { data: allProjects } = useProjectsQuery(workspaceId);
  const projects = allProjects.filter((p) => !p.isArchived);
  const { data: members } = useWorkspaceMembersQuery(workspaceId);
  const convert = useConvertIntakeItemMutation();
  const navigate = useNavigate();

  const [projectId, setProjectId] = useState<string>('');
  const [projectError, setProjectError] = useState<string | undefined>(undefined);
  const [titleError, setTitleError] = useState<string | undefined>(undefined);
  const [title, setTitle] = useState('');
  const [body, setBody] = useState('');
  const [assigneeUserIds, setAssigneeUserIds] = useState<Set<string>>(() => new Set());
  const [dueOn, setDueOn] = useState('');
  const resetKeyRef = useRef('');
  const assigneeIdPrefix = useId();

  // Reset selection whenever the dialog re-opens for a different item so
  // the picker doesn't carry over stale state from a prior conversion.
  useEffect(() => {
    if (open) {
      const resetKey = `${workspaceId}:${item?.id ?? ''}`;
      if (resetKeyRef.current === resetKey) return;
      resetKeyRef.current = resetKey;
      setProjectId('');
      setProjectError(undefined);
      setTitleError(undefined);
      setTitle(item?.title ?? '');
      setBody(item?.body ?? '');
      setAssigneeUserIds(new Set());
      setDueOn('');
    } else {
      resetKeyRef.current = '';
    }
  }, [open, item?.id, item?.title, item?.body, workspaceId]);

  useEffect(() => {
    if (open && !projectId && projects.length > 0) {
      setProjectId(projects[0]?.id ?? '');
    }
  }, [open, projectId, projects]);

  const projectOptions = useMemo<ComboboxOption[]>(
    () =>
      projects.map((p) => ({
        value: p.id,
        label: p.identifier ? `${p.name} (${p.identifier})` : p.name,
      })),
    [projects],
  );

  const handleSubmit = (e: FormEvent<HTMLFormElement>): void => {
    e.preventDefault();
    if (!item) return;
    if (!title.trim()) {
      setTitleError(t('sourceIntake.title_required'));
      return;
    }
    if (!projectId) {
      setProjectError(t('intake.convert.project_required'));
      return;
    }
    setProjectError(undefined);
    convert.mutate(
      {
        wsId: workspaceId,
        id: item.id,
        projectId,
        title: title.trim(),
        body,
        assigneeUserIds: [...assigneeUserIds],
        ...(dueOn ? { dueOn } : {}),
      },
      {
        onSuccess: ({ taskId }) => {
          toaster.show({ tone: 'success', message: t('intake.convert.success') });
          onClose();
          void navigate({ to: '/tasks/$taskId', params: { taskId } });
        },
        onError: (err) => {
          const message = formatApiError(err, t, 'intake.convert.error');
          toaster.show({ tone: 'danger', message });
        },
      },
    );
  };

  const toggleAssignee = (userId: string): void => {
    setAssigneeUserIds((previous) => {
      const next = new Set(previous);
      if (next.has(userId)) next.delete(userId);
      else next.add(userId);
      return next;
    });
  };

  const sourceUrl = safeSourceUrl(item?.sourceUrl);

  return (
    <Dialog open={open} onClose={onClose} title={t('intake.convert.title')}>
      <form
        onSubmit={handleSubmit}
        style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-4)' }}
        onKeyDown={onSubmitShortcut}
      >
        <FormField
          label={t('sourceIntake.task_title')}
          required
          {...(titleError ? { error: titleError } : {})}
        >
          {(control) => (
            <Input
              {...control}
              value={title}
              onChange={(event) => {
                setTitle(event.target.value);
                if (titleError) setTitleError(undefined);
              }}
            />
          )}
        </FormField>
        <FormField label={t('sourceIntake.task_body')}>
          {(control) => (
            <Textarea
              {...control}
              value={body}
              onChange={(event) => setBody(event.target.value)}
              rows={4}
            />
          )}
        </FormField>
        {sourceUrl ? (
          <a href={sourceUrl} target="_blank" rel="noreferrer">
            {t('sourceIntake.open_source')}
          </a>
        ) : null}
        <FormField label={t('intake.convert.project_label')} error={projectError}>
          {(control) => (
            <Combobox
              {...control}
              value={projectId}
              onChange={(v) => {
                setProjectId(v);
                setProjectError(undefined);
              }}
              options={projectOptions}
              placeholder={t('intake.convert.project_placeholder')}
              aria-label={t('intake.convert.project_label')}
            />
          )}
        </FormField>
        {members.length > 0 ? (
          <fieldset style={{ margin: 0, padding: 0, border: 0 }}>
            <legend>{t('sourceIntake.assignees')}</legend>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-2)' }}>
              {members.map((member) => (
                <label
                  key={member.userId}
                  htmlFor={`${assigneeIdPrefix}-${member.userId}`}
                  style={{ display: 'flex', alignItems: 'center', gap: 'var(--nf-space-2)' }}
                >
                  <Checkbox
                    id={`${assigneeIdPrefix}-${member.userId}`}
                    checked={assigneeUserIds.has(member.userId)}
                    onChange={() => toggleAssignee(member.userId)}
                  />
                  <span>{member.displayName}</span>
                </label>
              ))}
            </div>
          </fieldset>
        ) : null}
        <FormField label={t('sourceIntake.due_date')}>
          {() => (
            <DatePicker
              value={dueOn}
              onChange={setDueOn}
              weekdayLabels={weekdayLabels}
              weekStart={weekStart}
              formatMonthYear={formatMonthYear}
              prevLabel={t('calendar.prev', { ns: 'common' })}
              nextLabel={t('calendar.next', { ns: 'common' })}
              triggerLabel={
                dueOn ? formatDate(dueOn, locale) : t('common.date.placeholder', { ns: 'common' })
              }
            />
          )}
        </FormField>
        <FormActions hint={{ disabled: convert.isPending || projects.length === 0 }}>
          <Button type="button" variant="ghost" size="sm" onClick={onClose}>
            {t('intake.convert.cancel')}
          </Button>
          <Button
            type="submit"
            variant="primary"
            size="sm"
            disabled={convert.isPending || projects.length === 0}
          >
            {t('intake.convert.submit')}
          </Button>
        </FormActions>
        {projects.length === 0 ? (
          <p
            style={{ margin: 0, color: 'var(--nf-color-fg-muted)', fontSize: 'var(--nf-text-xs)' }}
          >
            {t('intake.convert.no_projects')}
          </p>
        ) : null}
      </form>
    </Dialog>
  );
}
