/**
 * PageEditor — create / edit form for a wiki page.
 *
 * Reuses the shared MarkdownEditor from the tasks feature for the body
 * field. Supports parent page selector, optional project selector, and
 * an "Generate with AI" button that calls the generate mutation.
 */

import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Select from '@nodate-flow/ui/primitives/select';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import { type ChangeEvent, type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import AiConsequence, { AiSparkle } from '../ai-suggestions/ai-consequence';
import MarkdownEditor from '../tasks/markdown-editor';
import {
  type CreatePageInput,
  type PageItem,
  type UpdatePageInput,
  useCreatePage,
  usePagesQuery,
  useUpdatePage,
} from './api';
import PageGenerateDialog from './page-generate-dialog';
import styles from './pages.module.css';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface PageEditorProps {
  workspaceId: string;
  /** When provided the editor is in "edit" mode for this page. */
  existingPage: PageItem | undefined;
  /** Called after successful save or cancel. */
  onDone: (savedPageId: string | undefined) => void;
  /**
   * Called when AI generation has created a page on the server. Only create
   * mode offers generation; the caller moves to that page in edit mode.
   */
  onGeneratedPage?: ((page: PageItem) => void) | undefined;
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export default function PageEditor({
  workspaceId,
  existingPage,
  onDone,
  onGeneratedPage,
}: PageEditorProps): ReactElement {
  const { t } = useTranslation('pages');
  const isEditing = existingPage !== undefined;

  const [title, setTitle] = useState(existingPage?.title ?? '');
  const [body, setBody] = useState(existingPage?.body ?? '');
  const [parentPageId, setParentPageId] = useState(existingPage?.parentPageId ?? '');
  const [projectId, setProjectId] = useState(existingPage?.projectId ?? '');
  const [generateDialogOpen, setGenerateDialogOpen] = useState(false);

  const createMutation = useCreatePage(workspaceId);
  const updateMutation = useUpdatePage(workspaceId);

  const { data: rootPages } = usePagesQuery(workspaceId);

  const isSaving = createMutation.isPending || updateMutation.isPending;

  const handleSubmit = (e: FormEvent<HTMLFormElement>): void => {
    e.preventDefault();
    if (title.trim().length === 0) return;

    if (isEditing && existingPage) {
      const patch: UpdatePageInput = {};
      if (title !== existingPage.title) patch.title = title;
      if (body !== (existingPage.body ?? '')) patch.body = body;
      if (parentPageId !== (existingPage.parentPageId ?? '')) {
        // Backend treats absence as "unset"; with exactOptionalPropertyTypes
        // we must leave the key off entirely instead of writing `undefined`.
        if (parentPageId) {
          patch.parentPageId = parentPageId;
        }
      }
      updateMutation.mutate(
        { pageId: existingPage.id, patch },
        {
          onSuccess: () => {
            onDone(existingPage.id);
          },
          onError: (err) => {
            toaster.show({ tone: 'danger', message: formatApiError(err, t, 'errors.save_failed') });
          },
        },
      );
    } else {
      const input: CreatePageInput = { title };
      if (body.trim().length > 0) input.body = body;
      if (parentPageId.length > 0) input.parentPageId = parentPageId;
      if (projectId.length > 0) input.projectId = projectId;
      createMutation.mutate(
        { input },
        {
          onSuccess: (created) => {
            onDone(created.id);
          },
          onError: (err) => {
            toaster.show({
              tone: 'danger',
              message: formatApiError(err, t, 'errors.create_failed'),
            });
          },
        },
      );
    }
  };

  const handleOpenGenerate = (): void => {
    if (title.trim().length === 0) return;
    setGenerateDialogOpen(true);
  };

  const handleCloseGenerate = (): void => {
    setGenerateDialogOpen(false);
  };

  const handleGenerated = (generated: PageItem): void => {
    setBody(generated.body ?? '');
    onGeneratedPage?.(generated);
  };

  const handleCancel = (): void => {
    onDone(undefined);
  };

  const handleTitleChange = (e: ChangeEvent<HTMLInputElement>): void => {
    setTitle(e.target.value);
  };

  const handleParentChange = (e: ChangeEvent<HTMLSelectElement>): void => {
    setParentPageId(e.target.value);
  };

  const handleProjectChange = (e: ChangeEvent<HTMLSelectElement>): void => {
    setProjectId(e.target.value);
  };

  // Filter out the current page from parent candidates to prevent cycles.
  const parentCandidates = rootPages.filter((p) => p.id !== existingPage?.id);

  return (
    <div className={styles.editorContainer}>
      <form className={styles.form} onSubmit={handleSubmit}>
        {/* Title */}
        <FormField label={t('page_title_label')}>
          {(control) => (
            <Input {...control} value={title} onChange={handleTitleChange} required autoFocus />
          )}
        </FormField>

        {/* Parent + Project selectors */}
        <div className={styles.formRow}>
          <FormField label={t('parent_label')} description={t('parent_hint')}>
            {(control) => (
              <Select {...control} value={parentPageId} onChange={handleParentChange}>
                <option value="">{t('parent_none')}</option>
                {parentCandidates.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.title}
                  </option>
                ))}
              </Select>
            )}
          </FormField>
          <FormField label={t('project_label')}>
            {(control) => (
              <Select
                {...control}
                value={projectId}
                onChange={handleProjectChange}
                disabled={isEditing}
              >
                <option value="">{t('project_none')}</option>
              </Select>
            )}
          </FormField>
        </div>

        {/* Body (Markdown editor) */}
        <FormField label={t('page_body_label')}>
          {(control) => (
            /*
             * No `workspaceId`, so no mention picker. Only a task
             * description and a task comment are scanned for mentions on
             * save; offering the affordance on a page body would write a
             * chip for a notification nobody receives.
             */
            <MarkdownEditor
              id={control.id}
              value={body}
              onChange={setBody}
              rows={12}
              aria-label={t('page_body_label')}
            />
          )}
        </FormField>

        {/* Actions */}
        <div className={styles.formActions}>
          {!isEditing && (
            <>
              <Tooltip content={t('ai_generate_hint')}>
                <Button
                  type="button"
                  variant="default"
                  size="sm"
                  onClick={handleOpenGenerate}
                  disabled={title.trim().length === 0}
                  aria-describedby="page-ai-generate-effect"
                >
                  <AiSparkle kind="applies" />
                  {t('ai_generate')}
                </Button>
              </Tooltip>
              <span id="page-ai-generate-effect" className={styles.aiEffect}>
                <AiConsequence kind="applies" />
              </span>
            </>
          )}
          <span className={styles.formActionsSpacer} />
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={handleCancel}
            disabled={isSaving}
          >
            {t('common:common.cancel', { defaultValue: 'Cancel' })}
          </Button>
          <Button
            type="submit"
            variant="primary"
            size="sm"
            disabled={isSaving || title.trim().length === 0}
            aria-busy={isSaving || undefined}
          >
            {isEditing ? t('edit') : t('create')}
          </Button>
        </div>
      </form>

      {!isEditing && (
        <PageGenerateDialog
          open={generateDialogOpen}
          workspaceId={workspaceId}
          title={title}
          projectId={projectId.length > 0 ? projectId : undefined}
          onClose={handleCloseGenerate}
          onGenerated={handleGenerated}
        />
      )}
    </div>
  );
}
