/**
 * ModelAddDialog — register a model under a provider.
 *
 * The model ID is a free-text field; the models the provider key can see
 * are offered as suggestions, minus those already registered. The POST
 * always carries the field's value, so a suggestion is only a shortcut for
 * typing and a failed or empty lookup never blocks adding.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type KeyboardEvent, type ReactElement, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import styles from './model-add-dialog.module.css';
import { useAvailableModelsQuery, useCreateProviderModel } from './models-api';

const NAME_MAX = 128;
const DISPLAY_NAME_MAX = 255;
const NO_NAMES: readonly string[] = [];

/** Model names are ASCII, so ASCII lower-casing matches the server's case-insensitive collation. */
function asciiLower(value: string): string {
  return value.replace(/[A-Z]/g, (c) => c.toLowerCase());
}

export interface ModelAddDialogProps {
  workspaceId: string;
  providerId: string;
  open: boolean;
  /** Names already registered for the provider, enabled or not. */
  registeredNames: readonly string[];
  onClose: () => void;
}

export default function ModelAddDialog({
  workspaceId,
  providerId,
  open,
  registeredNames,
  onClose,
}: ModelAddDialogProps): ReactElement {
  const { t } = useTranslation('ai');
  const available = useAvailableModelsQuery(workspaceId, providerId, open);
  const create = useCreateProviderModel(workspaceId, providerId);
  const [name, setName] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [activeIndex, setActiveIndex] = useState(-1);
  const listId = useId();

  const registered = new Set(registeredNames.map(asciiLower));
  const needle = asciiLower(name.trim());
  const suggestions = (available.data ?? NO_NAMES).filter((id) => {
    const lower = asciiLower(id);
    return !registered.has(lower) && lower.includes(needle);
  });
  const loaded = available.isSuccess;
  const noneOffered =
    loaded && !(available.data ?? NO_NAMES).some((id) => !registered.has(asciiLower(id)));
  const listOpen = suggestions.length > 0;
  const optionId = (index: number): string => `${listId}-${index}`;
  const activeOption = activeIndex >= 0 && activeIndex < suggestions.length ? activeIndex : -1;

  const reset = (): void => {
    setName('');
    setDisplayName('');
    setActiveIndex(-1);
  };

  const handleClose = (): void => {
    reset();
    onClose();
  };

  const pick = (id: string): void => {
    setName(id);
    setActiveIndex(-1);
  };

  const handleKeyDown = (e: KeyboardEvent<HTMLInputElement>): void => {
    if (!listOpen) return;
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setActiveIndex(activeOption + 1 >= suggestions.length ? 0 : activeOption + 1);
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setActiveIndex(activeOption <= 0 ? suggestions.length - 1 : activeOption - 1);
    } else if (e.key === 'Enter' && activeOption >= 0) {
      // Fills the field; the form is submitted by a second Enter.
      e.preventDefault();
      const chosen = suggestions[activeOption];
      if (chosen !== undefined) pick(chosen);
    }
  };

  const handleSubmit = (e: FormEvent<HTMLFormElement>): void => {
    e.preventDefault();
    const value = name.trim();
    if (value === '') return;
    const label = displayName.trim();
    create.mutate(
      { name: value, ...(label !== '' ? { displayName: label } : {}) },
      {
        onSuccess: handleClose,
        onError: (err) => {
          toaster.show({
            tone: 'danger',
            message: formatApiError(err, t, 'providers.toast.error'),
          });
        },
      },
    );
  };

  return (
    <Dialog open={open} onClose={handleClose} title={t('providers.models.add_dialog.title')}>
      <form onSubmit={handleSubmit} className={styles.form}>
        <FormField label={t('providers.models.add_dialog.name_label')} required>
          {(control) => (
            <Input
              {...control}
              role="combobox"
              aria-autocomplete="list"
              aria-expanded={listOpen}
              aria-controls={listId}
              {...(activeOption >= 0 ? { 'aria-activedescendant': optionId(activeOption) } : {})}
              className={styles.nameInput}
              value={name}
              maxLength={NAME_MAX}
              autoComplete="off"
              spellCheck={false}
              onChange={(e) => {
                setName(e.target.value);
                setActiveIndex(-1);
              }}
              onKeyDown={handleKeyDown}
            />
          )}
        </FormField>
        <div aria-busy={available.isFetching}>
          {listOpen ? (
            <ul
              id={listId}
              role="listbox"
              aria-label={t('providers.models.add_dialog.suggestions_label')}
              className={styles.suggestions}
            >
              {suggestions.map((id, index) => (
                // biome-ignore lint/a11y/useKeyWithClickEvents: keyboard selection runs through the combobox input
                <li
                  key={id}
                  id={optionId(index)}
                  role="option"
                  aria-selected={index === activeOption}
                  className={styles.option}
                  data-active={index === activeOption ? '' : undefined}
                  onMouseDown={(e) => {
                    // Keep focus in the input.
                    e.preventDefault();
                  }}
                  onClick={() => {
                    pick(id);
                  }}
                >
                  {id}
                </li>
              ))}
            </ul>
          ) : null}
          {available.isError ? (
            <p role="status" className={styles.note}>
              {t('providers.models.add_dialog.suggestions_failed')}
            </p>
          ) : noneOffered ? (
            <p role="status" className={styles.note}>
              {t('providers.models.add_dialog.suggestions_empty')}
            </p>
          ) : null}
        </div>
        <FormField label={t('providers.models.add_dialog.display_name_label')}>
          {(control) => (
            <Input
              {...control}
              value={displayName}
              maxLength={DISPLAY_NAME_MAX}
              autoComplete="off"
              onChange={(e) => {
                setDisplayName(e.target.value);
              }}
            />
          )}
        </FormField>
        <div className={styles.actions}>
          <Button type="button" variant="ghost" onClick={handleClose}>
            {t('providers.action.cancel')}
          </Button>
          <Button
            type="submit"
            variant="primary"
            disabled={name.trim() === '' || create.isPending}
            aria-busy={create.isPending}
          >
            {t('providers.models.add_dialog.submit')}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
