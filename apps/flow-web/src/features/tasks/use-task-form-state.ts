/**
 * @brief Local form state shared by the full task-create dialog and the
 *        quick-capture dialog: title, description, title-error, reset.
 *
 * Both dialogs duplicate the same useState wiring around `title` /
 * `description` / `titleError`. Centralising it keeps reset logic in
 * one place and lets future dialogs (template-from-task, etc.) reuse
 * the same primitives.
 */
import { useCallback, useState } from 'react';

export interface TaskFormState {
  title: string;
  description: string;
  titleError: string | null;
  setTitle: (value: string) => void;
  setDescription: (value: string) => void;
  setTitleError: (value: string | null) => void;
  reset: () => void;
}

export function useTaskFormState(): TaskFormState {
  const [title, setTitle] = useState('');
  const [description, setDescription] = useState('');
  const [titleError, setTitleError] = useState<string | null>(null);

  const reset = useCallback((): void => {
    setTitle('');
    setDescription('');
    setTitleError(null);
  }, []);

  return {
    title,
    description,
    titleError,
    setTitle,
    setDescription,
    setTitleError,
    reset,
  };
}
