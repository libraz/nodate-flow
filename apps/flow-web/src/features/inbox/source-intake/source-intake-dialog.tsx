import Button from '@nodate-flow/ui/primitives/button';
import Checkbox from '@nodate-flow/ui/primitives/checkbox';
import DatePicker from '@nodate-flow/ui/primitives/date-picker';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import SegmentedControl from '@nodate-flow/ui/primitives/segmented-control';
import Select from '@nodate-flow/ui/primitives/select';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { Link } from '@tanstack/react-router';
import { type FormEvent, type ReactElement, useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../../lib/api-error';
import { formatDateOnly } from '../../../lib/format';
import { useWeekStart } from '../../../lib/use-week-start';
import { useProjectsQuery } from '../../projects/api';
import CopyButton from '../../settings/copy-button';
import { useTaskSearch } from '../../tasks/api';
import { useWorkspaceMembersQuery } from '../../workspaces/api';
import type { InboxItem } from '../api';
import { useConvertIntakeItemMutation } from '../intake/api';
import { useCaptureSourceAsIntakeMutation } from './api';
import { safeSourceUrl } from './safe-source-url';
import styles from './source-intake-dialog.module.css';

type Destination = 'create' | 'existing';

export interface SourceIntakeDialogProps {
  workspaceId: string;
  item: InboxItem;
  open: boolean;
  onClose: () => void;
}

interface OperationContext {
  workspaceId: string;
  signalId: string;
  destination: Destination;
  existingTaskId: string;
  projectId: string;
  title: string;
  body: string;
  assigneeUserIds: string[];
  dueOn: string;
}

export default function SourceIntakeDialog({
  workspaceId,
  item,
  open,
  onClose,
}: SourceIntakeDialogProps): ReactElement {
  const { t, i18n } = useTranslation('inbox');
  const weekStart = useWeekStart();
  const locale = i18n.resolvedLanguage ?? 'en';
  const weekdayLabels = t('common.date.weekdays', {
    ns: 'common',
    returnObjects: true,
  }) as string[];
  const formatMonthYear = (year: number, month: number): string =>
    t('common.date.monthYear', { ns: 'common', year, month });
  const { data: projects } = useProjectsQuery(workspaceId);
  const { data: members } = useWorkspaceMembersQuery(workspaceId);
  const [destination, setDestination] = useState<Destination>('create');
  const [projectId, setProjectId] = useState('');
  const [title, setTitle] = useState(item.sourceDetail?.title ?? '');
  const [body, setBody] = useState(item.sourceDetail?.body ?? '');
  const [assigneeUserIds, setAssigneeUserIds] = useState<Set<string>>(() => new Set());
  const [dueOn, setDueOn] = useState('');
  const [taskSearch, setTaskSearch] = useState('');
  const [existingTaskId, setExistingTaskId] = useState('');
  const [intakeItemId, setIntakeItemId] = useState<string | undefined>();
  const [completedTaskId, setCompletedTaskId] = useState<string | undefined>();
  const [errorMessage, setErrorMessage] = useState<string | undefined>();
  const [titleError, setTitleError] = useState<string | undefined>();
  const [projectError, setProjectError] = useState<string | undefined>();
  const [submitting, setSubmitting] = useState(false);
  const [completedAs, setCompletedAs] = useState<Destination | undefined>();
  const resetKeyRef = useRef('');
  const contextRef = useRef({ workspaceId, signalId: item.id });
  const assigneeIdPrefix = useId();

  const capture = useCaptureSourceAsIntakeMutation();
  const convert = useConvertIntakeItemMutation();
  const taskSearchResult = useTaskSearch(
    workspaceId,
    taskSearch,
    open && destination === 'existing',
  );
  const sourceUrl = safeSourceUrl(item.sourceDetail?.sourceUrl);
  const acknowledgment = [
    t('sourceIntake.acknowledgment_intro'),
    item.sourceDetail?.title,
    item.sourceDetail?.body,
    sourceUrl,
  ]
    .filter(Boolean)
    .join('\n\n');

  useEffect(() => {
    contextRef.current = { workspaceId, signalId: item.id };
    if (!open) {
      resetKeyRef.current = '';
      return;
    }
    const resetKey = `${workspaceId}:${item.id}`;
    if (resetKeyRef.current === resetKey) return;
    resetKeyRef.current = resetKey;
    setDestination('create');
    setProjectId('');
    setTitle(item.sourceDetail?.title ?? '');
    setBody(item.sourceDetail?.body ?? '');
    setAssigneeUserIds(new Set());
    setDueOn('');
    setTaskSearch('');
    setExistingTaskId('');
    setIntakeItemId(undefined);
    setCompletedTaskId(undefined);
    setCompletedAs(undefined);
    setErrorMessage(undefined);
    setTitleError(undefined);
    setProjectError(undefined);
  }, [open, workspaceId, item.id, item.sourceDetail?.title, item.sourceDetail?.body]);

  useEffect(() => {
    if (open && !projectId && projects.length > 0) {
      setProjectId(projects[0]?.id ?? '');
    }
  }, [open, projectId, projects]);

  const toggleAssignee = (userId: string): void => {
    setAssigneeUserIds((previous) => {
      const next = new Set(previous);
      if (next.has(userId)) next.delete(userId);
      else next.add(userId);
      return next;
    });
  };

  const handleSubmit = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault();
    if (submitting || completedTaskId) return;

    const operation: OperationContext = {
      workspaceId,
      signalId: item.id,
      destination,
      existingTaskId,
      projectId,
      title: title.trim(),
      body,
      assigneeUserIds: [...assigneeUserIds],
      dueOn,
    };
    const isCurrentOperation = (): boolean =>
      contextRef.current.workspaceId === operation.workspaceId &&
      contextRef.current.signalId === operation.signalId;

    if (operation.destination === 'existing' && !operation.existingTaskId) return;
    if (operation.destination === 'create' && !operation.projectId) {
      setProjectError(t('sourceIntake.project_required'));
      return;
    }
    if (operation.destination === 'create' && operation.title.length === 0) {
      setTitleError(t('sourceIntake.title_required'));
      return;
    }

    setErrorMessage(undefined);
    setSubmitting(true);
    try {
      if (operation.destination === 'existing') {
        const result = await capture.mutateAsync({
          workspaceId: operation.workspaceId,
          signalId: operation.signalId,
          existingTaskId: operation.existingTaskId,
        });
        if (isCurrentOperation()) {
          setCompletedTaskId(result.taskId ?? operation.existingTaskId);
          setCompletedAs('existing');
        }
        return;
      }

      let currentIntakeItemId = intakeItemId;
      if (!currentIntakeItemId) {
        const captured = await capture.mutateAsync({
          workspaceId: operation.workspaceId,
          signalId: operation.signalId,
        });
        currentIntakeItemId = captured.intakeItemId;
        if (isCurrentOperation()) setIntakeItemId(currentIntakeItemId);
      }

      const result = await convert.mutateAsync({
        wsId: operation.workspaceId,
        id: currentIntakeItemId,
        projectId: operation.projectId,
        title: operation.title,
        body: operation.body,
        assigneeUserIds: operation.assigneeUserIds,
        ...(operation.dueOn ? { dueOn: operation.dueOn } : {}),
      });
      if (isCurrentOperation()) {
        setCompletedTaskId(result.taskId);
        setCompletedAs('create');
      }
    } catch (error) {
      if (isCurrentOperation()) {
        setErrorMessage(formatApiError(error, t, 'sourceIntake.error'));
      }
    } finally {
      setSubmitting(false);
    }
  };

  const taskOptions = taskSearchResult.data ?? [];
  const handleClose = (): void => {
    if (!submitting) onClose();
  };

  return (
    <Dialog open={open} onClose={handleClose} title={t('sourceIntake.title')} size="lg">
      <form className={styles.form} onSubmit={(event) => void handleSubmit(event)}>
        <div className={styles.bodyScroll}>
          <section className={styles.sourceSummary} aria-label={t('sourceIntake.source_details')}>
            <p className={styles.sourceTitle}>{item.sourceDetail?.title}</p>
            {item.sourceDetail?.body ? (
              <p className={styles.sourceBody}>{item.sourceDetail.body}</p>
            ) : null}
            <div className={styles.sourceActions}>
              {sourceUrl ? (
                <a className={styles.sourceLink} href={sourceUrl} target="_blank" rel="noreferrer">
                  {t('sourceIntake.open_source')}
                </a>
              ) : null}
              {acknowledgment ? (
                <CopyButton
                  value={acknowledgment}
                  label={t('sourceIntake.copy_acknowledgment')}
                  copiedLabel={t('sourceIntake.copied')}
                  onError={() => setErrorMessage(t('sourceIntake.copy_error'))}
                  variant="ghost"
                  size="sm"
                />
              ) : null}
            </div>
          </section>

          <fieldset className={styles.fieldset}>
            <legend className={styles.legend}>{t('sourceIntake.destination')}</legend>
            <SegmentedControl<Destination>
              ariaLabel={t('sourceIntake.destination')}
              value={destination}
              onChange={(next) => {
                setDestination(next);
                setErrorMessage(undefined);
                setTitleError(undefined);
                setProjectError(undefined);
              }}
              options={[
                { value: 'create', label: t('sourceIntake.create_destination') },
                { value: 'existing', label: t('sourceIntake.existing_destination') },
              ]}
              fullWidth
              disabled={Boolean(completedTaskId)}
            />
            <p className={styles.destinationHint}>{t('sourceIntake.destination_hint')}</p>
          </fieldset>

          <fieldset className={styles.fieldset} disabled={Boolean(completedTaskId)}>
            {destination === 'create' ? (
              <div className={styles.fields}>
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
                        setTitleError(undefined);
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
                <FormField
                  label={t('intake.convert.project_label')}
                  {...(projectError ? { error: projectError } : {})}
                  {...(projects.length === 0
                    ? { description: t('intake.convert.no_projects') }
                    : {})}
                >
                  {(control) => (
                    <Select
                      {...control}
                      value={projectId}
                      onChange={(event) => {
                        setProjectId(event.target.value);
                        setProjectError(undefined);
                      }}
                    >
                      <option value="">{t('intake.convert.project_placeholder')}</option>
                      {projects.map((project) => (
                        <option key={project.id} value={project.id}>
                          {project.identifier
                            ? `${project.name} (${project.identifier})`
                            : project.name}
                        </option>
                      ))}
                    </Select>
                  )}
                </FormField>
                {members.length > 0 ? (
                  <fieldset className={styles.fieldset}>
                    <legend className={styles.legend}>{t('sourceIntake.assignees')}</legend>
                    <div className={styles.memberList}>
                      {members.map((member) => (
                        <label
                          className={styles.memberOption}
                          htmlFor={`${assigneeIdPrefix}-${member.userId}`}
                          key={member.userId}
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
                        dueOn
                          ? formatDateOnly(dueOn, locale)
                          : t('common.date.placeholder', { ns: 'common' })
                      }
                    />
                  )}
                </FormField>
              </div>
            ) : (
              <div className={styles.fields}>
                <FormField label={t('sourceIntake.task_search')}>
                  {(control) => (
                    <Input
                      {...control}
                      value={taskSearch}
                      onChange={(event) => {
                        setTaskSearch(event.target.value);
                        setExistingTaskId('');
                      }}
                      autoComplete="off"
                    />
                  )}
                </FormField>
                <div
                  className={styles.taskResults}
                  role="group"
                  aria-label={t('sourceIntake.task_results')}
                >
                  {taskOptions.map((task) => (
                    <button
                      className={`${styles.taskOption} ${existingTaskId === task.id ? styles.taskOptionSelected : ''}`}
                      key={task.id}
                      type="button"
                      aria-label={task.title}
                      aria-pressed={existingTaskId === task.id}
                      onClick={() => setExistingTaskId(task.id)}
                    >
                      <span className={styles.taskTitle}>{task.title}</span>
                      <span className={styles.taskId} aria-hidden="true">
                        {task.id.slice(0, 8)}
                      </span>
                    </button>
                  ))}
                  {taskSearch.trim().length > 0 &&
                  taskOptions.length === 0 &&
                  taskSearchResult.isFetching ? (
                    <p className={styles.muted} role="status">
                      {t('sourceIntake.searching')}
                    </p>
                  ) : null}
                  {taskSearch.trim().length > 0 &&
                  taskOptions.length === 0 &&
                  !taskSearchResult.isFetching ? (
                    <p className={styles.muted}>{t('sourceIntake.no_tasks')}</p>
                  ) : null}
                  {taskSearch.trim().length === 0 ? (
                    <p className={styles.muted}>{t('sourceIntake.search_hint')}</p>
                  ) : null}
                </div>
              </div>
            )}
          </fieldset>

          {errorMessage ? (
            <p className={styles.error} role="alert">
              {errorMessage}
            </p>
          ) : null}
          {completedTaskId ? (
            <div className={styles.success} role="status">
              <span>
                {completedAs === 'existing' ? t('sourceIntake.linked') : t('sourceIntake.created')}
              </span>
              <Link
                className={styles.successLink}
                to="/tasks/$taskId"
                params={{ taskId: completedTaskId }}
              >
                {t('sourceIntake.open_task')}
              </Link>
            </div>
          ) : null}
        </div>
        <div className={styles.footer}>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={handleClose}
            disabled={submitting}
          >
            {completedTaskId ? t('sourceIntake.close') : t('intake.convert.cancel')}
          </Button>
          {!completedTaskId ? (
            <Button
              type="submit"
              variant="primary"
              size="sm"
              disabled={submitting || (destination === 'existing' && !existingTaskId)}
              aria-busy={submitting || undefined}
            >
              {destination === 'existing'
                ? submitting
                  ? t('sourceIntake.linking')
                  : t('sourceIntake.link_task')
                : submitting
                  ? t('sourceIntake.creating')
                  : t('sourceIntake.create_task')}
            </Button>
          ) : null}
        </div>
      </form>
    </Dialog>
  );
}
